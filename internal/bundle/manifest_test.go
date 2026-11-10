package bundle

import (
	"strings"
	"testing"
)

const validManifest = `schema: storied/v1
repo: org/name
pr: 7
base_sha: 1111111111111111111111111111111111111111
head_sha: 2222222222222222222222222222222222222222
artifacts:
  - id: chart
    kind: vega-lite
    path: artifacts/chart.vl.json
    data: artifacts/chart.csv
    fallback: artifacts/chart.png
    provenance: { class: gpu-job, job_id: "slurm-88121", config_hash: abc }
  - id: shots
    kind: image-compare
    before: artifacts/a.png
    after: artifacts/b.png
    provenance: { class: author }
`

func TestParseManifestValid(t *testing.T) {
	m, issues := ParseManifest([]byte(validManifest))
	if len(issues) != 0 {
		t.Fatalf("issues: %v", issues)
	}
	if m.Repo != "org/name" || m.PR != 7 || len(m.Artifacts) != 2 {
		t.Fatalf("manifest = %+v", m)
	}
	chart := m.Artifacts[0]
	if chart.Line != 7 || chart.Provenance.JobID != "slurm-88121" {
		t.Errorf("chart = %+v, want Line 7 and the job id", chart)
	}
	want := []string{"artifacts/chart.vl.json", "artifacts/chart.csv", "artifacts/chart.png"}
	if got := chart.Files(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Files() = %v, want %v", got, want)
	}
}

func TestParseManifestIssues(t *testing.T) {
	// Each case replaces one fragment of the valid manifest.
	tests := []struct {
		name     string
		old, new string
		want     string
	}{
		{"unsupported schema", "storied/v1", "storied/v2", `manifest.yaml:1: schema "storied/v2" is not supported`},
		{"bad repo", "repo: org/name", "repo: name", `manifest.yaml:2: repo "name" must look like owner/name`},
		{"negative pr", "pr: 7", "pr: -1", "manifest.yaml:3: pr must be a positive number"},
		{"pr not a number", "pr: 7", "pr: seven", "manifest.yaml:3: cannot unmarshal"},
		{"short base sha", "base_sha: 1111111111111111111111111111111111111111", "base_sha: 3f1c9a2", `manifest.yaml:4: base_sha "3f1c9a2" must be a full-length commit SHA`},
		{"missing head sha", "head_sha: 2222222222222222222222222222222222222222\n", "", `head_sha "" must be a full-length commit SHA`},
		{"unknown top-level field", "pr: 7", "pull: 7", "manifest.yaml:3: field pull not found"},
		{"unknown artifact field", "    data: artifacts/chart.csv", "    datum: artifacts/chart.csv", "manifest.yaml:10: field datum not found"},
		{"bad section hash", "artifacts:", "sections: { intro: { hash: abc } }\nartifacts:", "manifest.yaml:6: sections.intro.hash must be a SHA-256 hex digest"},
		{"bad artifact id", "id: chart", "id: Chart!", `manifest.yaml:7: artifact id "Chart!" must be`},
		{"duplicate artifact id", "id: shots", "id: chart", `manifest.yaml:13: artifact id "chart" is already used on line 7`},
		{"unknown kind", "kind: vega-lite", "kind: hologram", `artifact chart: unknown kind "hologram"`},
		{"missing required file", "    path: artifacts/chart.vl.json\n", "", "artifact chart: kind vega-lite needs path"},
		{"field the kind does not use", "    before: artifacts/a.png", "    before: artifacts/a.png\n    data: artifacts/x.csv", "artifact shots: kind image-compare does not use data"},
		{"checksum for unused file", "    after: artifacts/b.png", "    after: artifacts/b.png\n    sha256: { artifacts/zzz.png: " + strings.Repeat("0", 64) + " }", `sha256 lists "artifacts/zzz.png", which the artifact does not use`},
		{"malformed checksum", "    after: artifacts/b.png", "    after: artifacts/b.png\n    sha256: { artifacts/b.png: abc }", `sha256 of "artifacts/b.png" must be a SHA-256 hex digest`},
		{"unknown provenance class", "class: author", "class: vibes", `provenance class "vibes" must be`},
		{"missing provenance", "    provenance: { class: author }\n", "", `artifact shots: provenance class "" must be`},
		{"gpu-job without job id", `job_id: "slurm-88121", `, "", "provenance class gpu-job needs job_id"},
		{"ci without run url", "class: author", "class: ci", "provenance class ci needs run_url"},
		{"short provenance sha", "class: author", "class: author, sha: 9be04d7", `provenance sha "9be04d7" must be a full-length commit SHA`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(validManifest, tt.old) {
				t.Fatalf("valid manifest does not contain %q", tt.old)
			}
			_, issues := ParseManifest([]byte(strings.Replace(validManifest, tt.old, tt.new, 1)))
			if !strings.Contains(issues.Error(), tt.want) {
				t.Errorf("issues:\n%v\nwant one containing %q", issues, tt.want)
			}
		})
	}
}

func TestParseManifestUndecodable(t *testing.T) {
	for name, src := range map[string]string{
		"empty":                              "",
		"not yaml":                           "schema: [unclosed\n",
		"not a map":                          "- a\n- b\n",
		"two documents concatenated wrongly": "schema: storied/v1\n  repo: x\n",
	} {
		t.Run(name, func(t *testing.T) {
			m, issues := ParseManifest([]byte(src))
			if m != nil || len(issues) == 0 {
				t.Errorf("ParseManifest = %+v, %v; want nil manifest and at least one issue", m, issues)
			}
		})
	}
}
