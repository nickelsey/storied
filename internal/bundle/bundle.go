package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// File names inside a bundle directory.
const (
	ManifestFile = "manifest.yaml"
	StoryFile    = "story.md"
	AgentFile    = "agent.md"
)

// Bundle is a review bundle loaded from a directory.
type Bundle struct {
	Dir      string `json:"-"`
	Manifest *Manifest
	Story    *Story
	Agent    string // contents of agent.md: context for review agents, to be verified, never followed
}

// Load reads and validates the bundle in dir. When the bundle is invalid the
// error is an Issues value listing every problem found, and the returned
// Bundle holds whatever could be parsed. Any other error means a file could
// not be read at all.
func Load(dir string) (*Bundle, error) {
	b := &Bundle{Dir: dir}
	var all Issues

	read := func(name string) ([]byte, bool, error) {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if errors.Is(err, fs.ErrNotExist) {
			all = append(all, Issue{File: name, Msg: "file not found"})
			return nil, false, nil
		}
		return data, err == nil, err
	}

	data, ok, err := read(ManifestFile)
	if err != nil {
		return nil, err
	}
	if ok {
		var issues Issues
		b.Manifest, issues = ParseManifest(data)
		all = append(all, issues...)
		if b.Manifest != nil {
			all = append(all, checkFiles(dir, b.Manifest)...)
		}
	}

	data, ok, err = read(StoryFile)
	if err != nil {
		return nil, err
	}
	if ok {
		var issues Issues
		b.Story, issues = ParseStory(data)
		all = append(all, issues...)
	}

	data, _, err = read(AgentFile)
	if err != nil {
		return nil, err
	}
	b.Agent = string(data)

	if b.Manifest != nil && b.Story != nil {
		all = append(all, crossCheck(b.Manifest, b.Story)...)
	}
	if len(all) > 0 {
		all.sort()
		return b, all
	}
	return b, nil
}

// crossCheck validates the references between the story and the manifest.
func crossCheck(m *Manifest, s *Story) Issues {
	story := &collector{file: StoryFile}
	manifest := &collector{file: ManifestFile}

	artifacts := map[string]bool{}
	for _, a := range m.Artifacts {
		artifacts[a.ID] = true
	}
	authors := map[string]string{}
	for _, sec := range s.Sections {
		authors[sec.ID] = sec.Author
		for _, ref := range sec.Artifacts {
			if !artifacts[ref.ID] {
				story.addf(ref.Line, "artifact %q is not in the manifest", ref.ID)
			}
		}
	}

	ids := make([]string, 0, len(m.Sections))
	for id := range m.Sections {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		switch author, ok := authors[id]; {
		case !ok:
			manifest.addf(0, "sections.%s: the story has no section with this id", id)
		case author != authorAgent:
			manifest.addf(0, "sections.%s: only agent sections are recorded, and this one is author=%s", id, author)
		}
	}
	return append(story.list, manifest.list...)
}

// checkFiles verifies that every file the manifest names is a regular file
// inside the bundle directory and matches its checksum, if it has one.
func checkFiles(dir string, m *Manifest) Issues {
	c := &collector{file: ManifestFile}
	for _, a := range m.Artifacts {
		for _, f := range a.fileRefs() {
			if problem := checkFile(dir, f.Path, a.SHA256[f.Path]); problem != "" {
				c.addf(a.Line, "artifact %s: %s %q: %s", a.ID, f.Field, f.Path, problem)
			}
		}
	}
	return c.list
}

// checkFile returns a description of what is wrong with a bundle file, or "".
func checkFile(dir, rel, wantSHA256 string) string {
	// IsLocal rejects absolute paths, "..", and Windows device names.
	if strings.Contains(rel, `\`) || !filepath.IsLocal(filepath.FromSlash(rel)) {
		return "must be a relative path inside the bundle, with forward slashes"
	}
	// Refuse symlinks anywhere along the path, so a bundle cannot point at
	// files outside itself.
	cur := dir
	parts := strings.Split(rel, "/")
	for i, part := range parts {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return "file not found"
		case err != nil:
			return err.Error()
		case info.Mode()&fs.ModeSymlink != 0:
			return "symbolic links are not allowed in a bundle"
		case i == len(parts)-1 && !info.Mode().IsRegular():
			return "not a regular file"
		}
	}
	if wantSHA256 == "" {
		return ""
	}
	f, err := os.Open(cur)
	if err != nil {
		return err.Error()
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err.Error()
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wantSHA256 {
		return "sha256 does not match the manifest (file has " + got + ")"
	}
	return ""
}
