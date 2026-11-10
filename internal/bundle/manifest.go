package bundle

import (
	"bytes"
	"errors"
	"io"
	"regexp"
	"sort"

	"go.yaml.in/yaml/v3"
)

// SchemaV1 is the only manifest schema this version reads.
const SchemaV1 = "storied/v1"

// Manifest is the parsed manifest.yaml. It ties the bundle to one diff and
// lists the artifacts with their provenance.
type Manifest struct {
	Schema      string                   `yaml:"schema"`
	Repo        string                   `yaml:"repo" json:",omitempty"`
	PR          int                      `yaml:"pr" json:",omitempty"`
	BaseRef     string                   `yaml:"base_ref" json:",omitempty"`
	BaseSHA     string                   `yaml:"base_sha"`
	HeadSHA     string                   `yaml:"head_sha"`
	GeneratedBy GeneratedBy              `yaml:"generated_by" json:",omitzero"`
	Sections    map[string]SectionRecord `yaml:"sections" json:",omitempty"`
	Artifacts   []Artifact               `yaml:"artifacts" json:",omitempty"`
}

// GeneratedBy records what wrote the bundle.
type GeneratedBy struct {
	Tool  string `yaml:"tool" json:",omitempty"`
	Agent string `yaml:"agent" json:",omitempty"`
}

// SectionRecord is what storied draft remembers about an agent section.
type SectionRecord struct {
	Hash string `yaml:"hash"` // Section.Hash at the time it was written
}

// Artifact is one piece of evidence. Which file fields are set depends on Kind.
type Artifact struct {
	ID         string            `yaml:"id"`
	Kind       string            `yaml:"kind"`
	Path       string            `yaml:"path" json:",omitempty"`
	Data       string            `yaml:"data" json:",omitempty"`
	Before     string            `yaml:"before" json:",omitempty"`
	After      string            `yaml:"after" json:",omitempty"`
	Fallback   string            `yaml:"fallback" json:",omitempty"` // static image for clients that cannot render Kind
	SHA256     map[string]string `yaml:"sha256" json:",omitempty"`   // file path to hex digest
	Provenance Provenance        `yaml:"provenance"`
	Line       int               `yaml:"-"`
}

// Provenance is the author's claim about where an artifact came from.
type Provenance struct {
	Class      string `yaml:"class"`
	SHA        string `yaml:"sha" json:",omitempty"`
	RunURL     string `yaml:"run_url" json:",omitempty"`
	JobID      string `yaml:"job_id" json:",omitempty"`
	ConfigHash string `yaml:"config_hash" json:",omitempty"`
	Command    string `yaml:"command" json:",omitempty"`
}

// fileRef is a file an artifact refers to, with the manifest field naming it.
type fileRef struct {
	Field, Path string
}

// Files returns the bundle-relative files the artifact refers to.
func (a Artifact) Files() []string {
	var out []string
	for _, f := range a.fileRefs() {
		out = append(out, f.Path)
	}
	return out
}

func (a Artifact) fileRefs() []fileRef {
	var out []fileRef
	for _, f := range []fileRef{
		{"path", a.Path}, {"data", a.Data}, {"before", a.Before}, {"after", a.After}, {"fallback", a.Fallback},
	} {
		if f.Path != "" {
			out = append(out, f)
		}
	}
	return out
}

// artifactKinds maps each v1 kind to its required and optional file fields.
// fallback is optional on every kind.
var artifactKinds = map[string]struct{ required, optional []string }{
	"markdown":      {required: []string{"path"}},
	"image":         {required: []string{"path"}},
	"log":           {required: []string{"path"}},
	"test-report":   {required: []string{"path"}},
	"k8s-diff":      {required: []string{"path"}},
	"vega-lite":     {required: []string{"path"}, optional: []string{"data"}},
	"image-compare": {required: []string{"before", "after"}},
	"gltf-compare":  {required: []string{"before", "after"}},
}

// provenanceClasses maps each class to a field it must carry, if any.
var provenanceClasses = map[string]string{
	"ci":             "run_url",
	"gpu-job":        "job_id",
	"author":         "",
	"reviewer-agent": "",
}

var (
	repoRE   = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	shaRE    = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
	sha256RE = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// ParseManifest parses and validates manifest.yaml without touching the
// filesystem; Load checks that the artifact files exist. The manifest is nil
// when the YAML itself cannot be decoded.
func ParseManifest(data []byte) (*Manifest, Issues) {
	c := &collector{file: ManifestFile}
	var m Manifest
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		if errors.Is(err, io.EOF) {
			c.addf(0, "file is empty")
		} else {
			c.addYAML(err, 0)
		}
		return nil, c.list
	}
	keyLines, artifactLines := locate(data)
	for i := range m.Artifacts {
		if i < len(artifactLines) {
			m.Artifacts[i].Line = artifactLines[i]
		}
	}
	m.validate(c, keyLines)
	return &m, c.list
}

// locate finds the line of each top-level key and of each artifact entry.
func locate(data []byte) (keys map[string]int, artifacts []int) {
	keys = map[string]int{}
	var root yaml.Node
	if yaml.Unmarshal(data, &root) != nil || len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return keys, nil
	}
	top := root.Content[0].Content
	for i := 0; i+1 < len(top); i += 2 {
		keys[top[i].Value] = top[i].Line
		if top[i].Value == "artifacts" && top[i+1].Kind == yaml.SequenceNode {
			for _, item := range top[i+1].Content {
				artifacts = append(artifacts, item.Line)
			}
		}
	}
	return keys, artifacts
}

func (m *Manifest) validate(c *collector, lines map[string]int) {
	if m.Schema != SchemaV1 {
		c.addf(lines["schema"], "schema %q is not supported; want %s", m.Schema, SchemaV1)
	}
	// repo and pr are optional: a bundle can be written before the branch
	// has a remote or a PR.
	if m.Repo != "" && !repoRE.MatchString(m.Repo) {
		c.addf(lines["repo"], "repo %q must look like owner/name", m.Repo)
	}
	if m.PR < 0 {
		c.addf(lines["pr"], "pr must be a positive number")
	}
	if !shaRE.MatchString(m.BaseSHA) {
		c.addf(lines["base_sha"], "base_sha %q must be a full-length commit SHA", m.BaseSHA)
	}
	if !shaRE.MatchString(m.HeadSHA) {
		c.addf(lines["head_sha"], "head_sha %q must be a full-length commit SHA", m.HeadSHA)
	}

	ids := make([]string, 0, len(m.Sections))
	for id := range m.Sections {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if !sha256RE.MatchString(m.Sections[id].Hash) {
			c.addf(lines["sections"], "sections.%s.hash must be a SHA-256 hex digest", id)
		}
	}

	seen := map[string]int{}
	for _, a := range m.Artifacts {
		a.validate(c)
		if a.ID == "" {
			continue
		}
		if first, dup := seen[a.ID]; dup {
			c.addf(a.Line, "artifact id %q is already used on line %d", a.ID, first)
		} else {
			seen[a.ID] = a.Line
		}
	}
}

func (a Artifact) validate(c *collector) {
	name := "artifact " + a.ID
	if !sectionIDRE.MatchString(a.ID) {
		name = "artifact"
		c.addf(a.Line, "artifact id %q must be lower-case letters, digits and single hyphens", a.ID)
	}

	set := map[string]bool{}
	for _, f := range a.fileRefs() {
		set[f.Field] = true
	}
	kind, known := artifactKinds[a.Kind]
	if !known {
		c.addf(a.Line, "%s: unknown kind %q", name, a.Kind)
	} else {
		for _, field := range kind.required {
			if !set[field] {
				c.addf(a.Line, "%s: kind %s needs %s", name, a.Kind, field)
			}
		}
		for _, field := range []string{"path", "data", "before", "after"} {
			if set[field] && !contains(kind.required, field) && !contains(kind.optional, field) {
				c.addf(a.Line, "%s: kind %s does not use %s", name, a.Kind, field)
			}
		}
	}

	files := a.Files()
	paths := make([]string, 0, len(a.SHA256))
	for p := range a.SHA256 {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if !contains(files, p) {
			c.addf(a.Line, "%s: sha256 lists %q, which the artifact does not use", name, p)
		}
		if !sha256RE.MatchString(a.SHA256[p]) {
			c.addf(a.Line, "%s: sha256 of %q must be a SHA-256 hex digest", name, p)
		}
	}

	p := a.Provenance
	need, known := provenanceClasses[p.Class]
	switch {
	case !known:
		c.addf(a.Line, "%s: provenance class %q must be ci, gpu-job, author or reviewer-agent", name, p.Class)
	case need == "run_url" && p.RunURL == "", need == "job_id" && p.JobID == "":
		c.addf(a.Line, "%s: provenance class %s needs %s", name, p.Class, need)
	}
	if p.SHA != "" && !shaRE.MatchString(p.SHA) {
		c.addf(a.Line, "%s: provenance sha %q must be a full-length commit SHA", name, p.SHA)
	}
}
