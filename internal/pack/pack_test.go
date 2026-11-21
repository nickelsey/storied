package pack

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nickelsey/storied/internal/bundle"
	"github.com/nickelsey/storied/internal/fixture"
	"github.com/nickelsey/storied/internal/lint"
)

var ctx = context.Background()

// The fixture story has warnings but no errors, and its manifest names PR 482.

// published sets up an origin for the fixture, pushes its branches there
// and returns a second clone on the PR branch, as a reviewer would have.
func published(t *testing.T) (author *fixture.Fixture, reviewer string) {
	t.Helper()
	f := fixture.New(t)
	origin := filepath.Join(t.TempDir(), "origin.git")
	f.Repo.Git("init", "-q", "--bare", origin)
	f.Repo.Git("remote", "add", "origin", origin)
	f.Repo.Git("push", "-q", "origin", "main", "repair")
	reviewer = filepath.Join(t.TempDir(), "reviewer")
	f.Repo.Git("clone", "-q", "--branch", "repair", origin, reviewer)
	return f, reviewer
}

func TestPackAndFetchRoundTrip(t *testing.T) {
	f, reviewer := published(t)
	// Things in the bundle directory that are not part of the bundle.
	f.WriteBundle("story.html", "<html>rendered</html>")
	f.WriteBundle("artifacts/scratch.txt", "not referenced")
	manifestBefore, err := os.ReadFile(filepath.Join(f.Bundle, "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	indexBefore := f.Repo.Git("status", "--porcelain")

	res, err := Pack(ctx, Options{RepoDir: f.Repo.Dir})
	if err != nil {
		t.Fatal(err)
	}
	wantFiles := "agent.md artifacts/drag.vl.json artifacts/mesh-after.png artifacts/mesh-before.png artifacts/notes.md artifacts/pytest.log manifest.yaml story.md"
	if res.Ref != "refs/storied/pr-482" || res.Remote != "origin" || strings.Join(res.Files, " ") != wantFiles {
		t.Errorf("result = %+v", res)
	}
	if got := f.Repo.Git("ls-remote", "origin", "refs/storied/pr-482"); !strings.HasPrefix(got, res.Commit) {
		t.Errorf("origin has %q, want commit %s", got, res.Commit)
	}
	if after, _ := os.ReadFile(filepath.Join(f.Bundle, "manifest.yaml")); string(after) != string(manifestBefore) {
		t.Error("pack changed the author's manifest")
	}
	if got := f.Repo.Git("status", "--porcelain"); got != indexBefore {
		t.Errorf("pack changed the index or worktree:\n%s", got)
	}
	if got := f.Repo.Git("branch", "--list", "--all"); strings.Contains(got, "storied") {
		t.Errorf("the bundle shows up as a branch:\n%s", got)
	}

	fr, err := Fetch(ctx, FetchOptions{RepoDir: reviewer, PR: 482})
	if err != nil {
		t.Fatal(err)
	}
	if fr.Commit != res.Commit || !fr.Current || fr.HeadSHA != f.Head || strings.Join(fr.Files, " ") != wantFiles {
		t.Errorf("fetch result = %+v", fr)
	}
	b, err := bundle.Load(fr.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range b.Manifest.Artifacts {
		for _, file := range a.Files() {
			if len(a.SHA256[file]) != 64 {
				t.Errorf("artifact %s: no checksum for %s in the published manifest", a.ID, file)
			}
		}
	}
	for _, name := range []string{"story.md", "agent.md", "artifacts/mesh-after.png"} {
		want, _ := os.ReadFile(filepath.Join(f.Bundle, filepath.FromSlash(name)))
		got, err := os.ReadFile(filepath.Join(fr.Bundle, filepath.FromSlash(name)))
		if err != nil || string(got) != string(want) {
			t.Errorf("%s differs after the round trip (err %v)", name, err)
		}
	}
	for _, name := range []string{"story.html", "artifacts/scratch.txt"} {
		if _, err := os.Stat(filepath.Join(fr.Bundle, filepath.FromSlash(name))); err == nil {
			t.Errorf("%s was published", name)
		}
	}

	// The reviewer's copy lints exactly like the author's, and stays out
	// of version control.
	mine, err := lint.Run(ctx, lint.Options{RepoDir: f.Repo.Dir})
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := lint.Run(ctx, lint.Options{RepoDir: reviewer})
	if err != nil {
		t.Fatal(err)
	}
	if len(theirs.Report.Errors) != 0 || theirs.Report.Coverage != mine.Report.Coverage {
		t.Errorf("reviewer's lint: errors %v, coverage %+v; author's coverage %+v", theirs.Report.Errors, theirs.Report.Coverage, mine.Report.Coverage)
	}
	out, err := os.ReadFile(filepath.Join(reviewer, ".git", "info", "exclude"))
	if err != nil || !strings.Contains(string(out), "/.storied/\n") {
		t.Errorf("the fetched bundle is not excluded (err %v):\n%s", err, out)
	}

	// The author updates the story and packs again; a second fetch
	// replaces the reviewer's copy.
	f.EditBundle("story.md", "Start with the repair pass", "Begin with the repair pass")
	res2, err := Pack(ctx, Options{RepoDir: f.Repo.Dir})
	if err != nil {
		t.Fatal(err)
	}
	fr2, err := Fetch(ctx, FetchOptions{RepoDir: reviewer, PR: 482})
	if err != nil {
		t.Fatal(err)
	}
	story, _ := os.ReadFile(filepath.Join(fr2.Bundle, "story.md"))
	if fr2.Commit != res2.Commit || !strings.Contains(string(story), "Begin with the repair pass") {
		t.Errorf("second fetch got commit %s (want %s) and an old story", fr2.Commit, res2.Commit)
	}
}

// rewrite publishes a copy of the packed bundle with one file replaced,
// as someone with push access to the ref could.
func rewrite(t *testing.T, f *fixture.Fixture, commit, path, content string, mode string) {
	t.Helper()
	t.Setenv("GIT_INDEX_FILE", filepath.Join(t.TempDir(), "index"))
	f.Repo.Git("read-tree", commit)
	src := filepath.Join(t.TempDir(), "blob")
	if err := os.WriteFile(src, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	blob := f.Repo.Git("hash-object", "-w", "--no-filters", "--", src)
	f.Repo.Git("update-index", "--add", "--cacheinfo", mode+","+blob+","+path)
	tree := f.Repo.Git("write-tree")
	forged := f.Repo.Git("commit-tree", tree, "-m", "forged")
	f.Repo.Git("push", "-q", "origin", "+"+forged+":refs/storied/pr-482")
	os.Unsetenv("GIT_INDEX_FILE")
}

func TestFetchRejectsTamperedBundles(t *testing.T) {
	tests := []struct {
		name, path, content, mode string
		want                      string
	}{
		{"artifact changed after packing", "artifacts/pytest.log", "0 passed, 12 failed\n", "100644",
			`artifact export-tests: path "artifacts/pytest.log": sha256 does not match`},
		{"symlink in the tree", "artifacts/notes.md", "/etc/passwd", "120000", "not a plain file"},
		{"invalid story", "story.md", "no frontmatter\n", "100644", "story must start with frontmatter"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, reviewer := published(t)
			res, err := Pack(ctx, Options{RepoDir: f.Repo.Dir})
			if err != nil {
				t.Fatal(err)
			}
			rewrite(t, f, res.Commit, tt.path, tt.content, tt.mode)
			_, err = Fetch(ctx, FetchOptions{RepoDir: reviewer, PR: 482})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want one containing %q", err, tt.want)
			}
			entries, _ := os.ReadDir(reviewer)
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".storied") {
					t.Errorf("a rejected bundle left %s behind", e.Name())
				}
			}
		})
	}
}

func TestFetchRequiresChecksums(t *testing.T) {
	f, reviewer := published(t)
	res, err := Pack(ctx, Options{RepoDir: f.Repo.Dir})
	if err != nil {
		t.Fatal(err)
	}
	// Publish the author's own manifest, which has no checksums.
	plain, _ := os.ReadFile(filepath.Join(f.Bundle, "manifest.yaml"))
	rewrite(t, f, res.Commit, "manifest.yaml", string(plain), "100644")
	var issues bundle.Issues
	if _, err := Fetch(ctx, FetchOptions{RepoDir: reviewer, PR: 482}); !errors.As(err, &issues) || !strings.Contains(err.Error(), "a published bundle must have one") {
		t.Errorf("err = %v", err)
	}
}

func TestFetchProtectsUnfetchedBundle(t *testing.T) {
	f, _ := published(t)
	if _, err := Pack(ctx, Options{RepoDir: f.Repo.Dir}); err != nil {
		t.Fatal(err)
	}
	// The author's own directory holds a bundle that was never fetched.
	before, _ := os.ReadFile(filepath.Join(f.Bundle, "manifest.yaml"))
	if _, err := Fetch(ctx, FetchOptions{RepoDir: f.Repo.Dir, PR: 482}); err == nil || !strings.Contains(err.Error(), "was not fetched") {
		t.Errorf("err = %v", err)
	}
	if after, _ := os.ReadFile(filepath.Join(f.Bundle, "manifest.yaml")); string(after) != string(before) {
		t.Error("fetch replaced the author's bundle")
	}
	if _, err := Fetch(ctx, FetchOptions{RepoDir: f.Repo.Dir, PR: 482, Force: true, Local: true}); err != nil {
		t.Errorf("fetch --force --local: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.Bundle, MarkerFile)); err != nil {
		t.Errorf("no marker after a forced fetch: %v", err)
	}
}

func TestPackRefusals(t *testing.T) {
	t.Run("invalid bundle", func(t *testing.T) {
		f, _ := published(t)
		f.EditBundle("story.md", "author=human}", "author=robot}")
		var issues bundle.Issues
		if _, err := Pack(ctx, Options{RepoDir: f.Repo.Dir}); !errors.As(err, &issues) {
			t.Errorf("err = %v, want bundle issues", err)
		}
	})
	t.Run("bundle written for another head", func(t *testing.T) {
		f, _ := published(t)
		f.Repo.Write("README.md", "newer\n")
		f.Repo.Commit("newer")
		if _, err := Pack(ctx, Options{RepoDir: f.Repo.Dir}); err == nil || !strings.Contains(err.Error(), "run storied draft") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("no pr number", func(t *testing.T) {
		f, _ := published(t)
		f.EditBundle("manifest.yaml", "pr: 482\n", "")
		if _, err := Pack(ctx, Options{RepoDir: f.Repo.Dir}); err == nil || !strings.Contains(err.Error(), "--pr") {
			t.Errorf("err = %v", err)
		}
		res, err := Pack(ctx, Options{RepoDir: f.Repo.Dir, PR: 9, NoPush: true})
		if err != nil || res.Ref != "refs/storied/pr-9" || res.Remote != "" {
			t.Fatalf("res = %+v, err = %v", res, err)
		}
		if got := f.Repo.Git("ls-remote", "origin", "refs/storied/*"); got != "" {
			t.Errorf("--no-push pushed: %s", got)
		}
		// The published manifest carries the number the author passed.
		if got := f.Repo.Git("show", res.Commit+":manifest.yaml"); !strings.HasSuffix(got, "\npr: 9") {
			t.Errorf("published manifest:\n%s", got)
		}
	})
	t.Run("push fails", func(t *testing.T) {
		f := fixture.New(t) // no remote at all
		res, err := Pack(ctx, Options{RepoDir: f.Repo.Dir})
		if err == nil || !strings.Contains(err.Error(), "committed locally as refs/storied/pr-482") || res == nil {
			t.Errorf("res = %+v, err = %v", res, err)
		}
	})
	t.Run("nothing published for the pr", func(t *testing.T) {
		_, reviewer := published(t)
		if _, err := Fetch(ctx, FetchOptions{RepoDir: reviewer, PR: 482}); err == nil || !strings.Contains(err.Error(), "no bundle for PR 482") {
			t.Errorf("err = %v", err)
		}
	})
}
