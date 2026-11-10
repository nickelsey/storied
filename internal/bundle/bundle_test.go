package bundle

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestLoadValid(t *testing.T) {
	dir := filepath.Join("testdata", "valid")
	b, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	golden := filepath.Join("testdata", "valid.golden.json")
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/bundle -update to create it)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("parsed bundle differs from %s; run with -update and review the diff.\ngot:\n%s", golden, got)
	}
}

// copyValid copies the valid fixture into a temp dir so a test can break it.
func copyValid(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("testdata", "valid"))); err != nil {
		t.Fatal(err)
	}
	return dir
}

// edit rewrites one file in dir by replacing old with new.
func edit(t *testing.T, dir, name, old, new string) {
	t.Helper()
	path := filepath.Join(dir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), old) {
		t.Fatalf("%s does not contain %q", name, old)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), old, new, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func wantIssue(t *testing.T, err error, want string) {
	t.Helper()
	var issues Issues
	if !errors.As(err, &issues) {
		t.Fatalf("error = %v, want Issues containing %q", err, want)
	}
	if !strings.Contains(issues.Error(), want) {
		t.Errorf("issues:\n%v\nwant one containing %q", issues, want)
	}
}

func TestLoadInvalid(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, dir string)
		want   string
	}{
		{"missing manifest", func(t *testing.T, dir string) {
			os.Remove(filepath.Join(dir, ManifestFile))
		}, "manifest.yaml: file not found"},
		{"missing story", func(t *testing.T, dir string) {
			os.Remove(filepath.Join(dir, StoryFile))
		}, "story.md: file not found"},
		{"missing agent.md", func(t *testing.T, dir string) {
			os.Remove(filepath.Join(dir, AgentFile))
		}, "agent.md: file not found"},
		{"missing artifact file", func(t *testing.T, dir string) {
			os.Remove(filepath.Join(dir, "artifacts", "notes.md"))
		}, `manifest.yaml:22: artifact repair-notes: path "artifacts/notes.md": file not found`},
		{"artifact path escapes the bundle", func(t *testing.T, dir string) {
			edit(t, dir, ManifestFile, "path: artifacts/notes.md", "path: ../notes.md")
		}, "must be a relative path inside the bundle"},
		{"absolute artifact path", func(t *testing.T, dir string) {
			edit(t, dir, ManifestFile, "path: artifacts/notes.md", "path: /etc/passwd")
		}, "must be a relative path inside the bundle"},
		{"backslash artifact path", func(t *testing.T, dir string) {
			edit(t, dir, ManifestFile, "path: artifacts/notes.md", `path: artifacts\notes.md`)
		}, "must be a relative path inside the bundle"},
		{"artifact is a directory", func(t *testing.T, dir string) {
			edit(t, dir, ManifestFile, "path: artifacts/notes.md", "path: artifacts")
		}, "not a regular file"},
		{"checksum mismatch", func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "artifacts", "pytest.log"), []byte("tampered\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "artifact export-tests: path \"artifacts/pytest.log\": sha256 does not match"},
		{"story references unknown artifact", func(t *testing.T, dir string) {
			edit(t, dir, StoryFile, `::artifact{id="repair-notes"}`, `::artifact{id="nope"}`)
		}, `story.md:22: artifact "nope" is not in the manifest`},
		{"manifest records unknown section", func(t *testing.T, dir string) {
			edit(t, dir, ManifestFile, "  repair-pass:", "  gone:")
		}, "sections.gone: the story has no section with this id"},
		{"manifest records human section", func(t *testing.T, dir string) {
			edit(t, dir, ManifestFile, "  repair-pass:", "  why:")
		}, "sections.why: only agent sections are recorded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := copyValid(t)
			tt.mutate(t, dir)
			b, err := Load(dir)
			wantIssue(t, err, tt.want)
			if b == nil {
				t.Error("Load returned a nil bundle alongside issues")
			}
		})
	}
}

func TestLoadRejectsSymlinks(t *testing.T) {
	dir := copyValid(t)
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(dir, "artifacts", "notes.md")
	os.Remove(notes)
	if err := os.Symlink(outside, notes); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
	_, err := Load(dir)
	wantIssue(t, err, "symbolic links are not allowed")
}

func TestLoadReportsEveryIssueInOrder(t *testing.T) {
	dir := copyValid(t)
	edit(t, dir, ManifestFile, "schema: storied/v1", "schema: storied/v9")
	edit(t, dir, StoryFile, "{#why author=human}", "{#why author=robot}")
	os.Remove(filepath.Join(dir, AgentFile))
	_, err := Load(dir)
	var issues Issues
	if !errors.As(err, &issues) {
		t.Fatalf("error = %v, want Issues", err)
	}
	want := "agent.md: file not found\n" +
		"manifest.yaml:1: schema \"storied/v9\" is not supported; want storied/v1\n" +
		"story.md:8: author \"robot\" must be human or agent"
	if issues.Error() != want {
		t.Errorf("issues:\n%s\nwant:\n%s", issues.Error(), want)
	}
}
