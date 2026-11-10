package bundle

import (
	"strings"
	"testing"
)

func TestUpdateManifest(t *testing.T) {
	const sha = "3333333333333333333333333333333333333333"
	h1, h2 := strings.Repeat("a", 64), strings.Repeat("b", 64)

	t.Run("sets fields and adds sections before artifacts, leaving the rest alone", func(t *testing.T) {
		src := strings.Replace(validManifest, "artifacts:\n", "# evidence\nartifacts:\n", 1)
		got, err := UpdateManifest([]byte(src), [][2]string{{"head_sha", sha}, {"base_ref", "main"}}, map[string]string{"b": h2, "a": h1})
		if err != nil {
			t.Fatal(err)
		}
		m, issues := ParseManifest(got)
		if len(issues) > 0 {
			t.Fatal(issues)
		}
		if m.HeadSHA != sha || m.BaseRef != "main" || m.Sections["a"].Hash != h1 || m.Sections["b"].Hash != h2 || len(m.Artifacts) != 2 {
			t.Errorf("manifest = %+v", m)
		}
		if !strings.Contains(string(got), "# evidence\n") || !strings.Contains(string(got), `provenance: { class: gpu-job, job_id: "slurm-88121", config_hash: abc }`) {
			t.Errorf("comments or layout were lost:\n%s", got)
		}
	})

	t.Run("replaces an existing sections block in either style", func(t *testing.T) {
		for _, old := range []string{
			"sections:\n  old:\n    hash: " + h2 + "\n\n",
			"sections: { old: { hash: " + h2 + " } }\n",
		} {
			src := strings.Replace(validManifest, "artifacts:\n", old+"artifacts:\n", 1)
			got, err := UpdateManifest([]byte(src), nil, map[string]string{"new": h1})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(got), "old") || strings.Count(string(got), "sections:") != 1 || !strings.Contains(string(got), "  new:\n    hash: "+h1+"\n") {
				t.Errorf("after replacing %q:\n%s", old, got)
			}
		}
	})

	t.Run("no sections removes the block", func(t *testing.T) {
		src := strings.Replace(validManifest, "artifacts:\n", "sections:\n  old:\n    hash: "+h2+"\nartifacts:\n", 1)
		got, err := UpdateManifest([]byte(src), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != validManifest {
			t.Errorf("got:\n%s\nwant the manifest without sections", got)
		}
	})

	t.Run("nothing to change gives the same bytes", func(t *testing.T) {
		got, err := UpdateManifest([]byte(validManifest), [][2]string{{"pr", "7"}}, nil)
		if err != nil || string(got) != validManifest {
			t.Errorf("err %v, got:\n%s", err, got)
		}
	})

	t.Run("an edit that breaks the manifest is refused", func(t *testing.T) {
		if _, err := UpdateManifest([]byte(validManifest), [][2]string{{"head_sha", "short"}}, nil); err == nil {
			t.Error("no error")
		}
		if _, err := UpdateManifest([]byte("schema: [broken\n"), nil, nil); err == nil {
			t.Error("no error for a manifest that does not parse")
		}
	})
}

func TestSectionSpans(t *testing.T) {
	src := frontmatter + "intro\n## A {#a author=human}\ntext\n\n## B {#b author=human}\n"
	s, issues := ParseStory([]byte(src))
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	lines := strings.Split(src, "\n")
	got := s.SectionSpans(len(lines))
	if want := [][2]int{{4, 7}, {7, 9}}; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("spans = %v, want %v", got, want)
	}
}
