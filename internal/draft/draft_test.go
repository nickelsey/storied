package draft

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"storied/internal/bundle"
	"storied/internal/fixture"
	"storied/internal/lint"
)

var update = flag.Bool("update", false, "rewrite golden files")

var ctx = context.Background()

// fresh returns the fixture repository with a newly initialised bundle in
// place of the hand-written one.
func fresh(t *testing.T) *fixture.Fixture {
	t.Helper()
	f := fixture.New(t)
	if err := os.RemoveAll(f.Bundle); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(ctx, InitOptions{RepoDir: f.Repo.Dir, Title: "Repair pass", Tool: "storied/test"}); err != nil {
		t.Fatal(err)
	}
	return f
}

func read(t *testing.T, f *fixture.Fixture, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.Bundle, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func draft(t *testing.T, f *fixture.Fixture) *Result {
	t.Helper()
	res, err := Run(ctx, Options{RepoDir: f.Repo.Dir})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/draft -update to create it)", err)
	}
	if got != string(want) {
		t.Errorf("output differs from %s; run with -update and review the diff.\ngot:\n%s", path, got)
	}
}

func statuses(res *Result) string {
	var out []string
	for _, s := range res.Sections {
		out = append(out, s.Status+":"+s.ID)
	}
	return strings.Join(out, " ")
}

func TestInitAndDraftGolden(t *testing.T) {
	f := fresh(t)
	golden(t, "init.manifest.yaml", read(t, f, "manifest.yaml"))
	golden(t, "init.story.md", read(t, f, "story.md"))

	res := draft(t, f)
	if !res.Written || len(res.Attention) != 0 {
		t.Errorf("Written = %v, Attention = %v; want written and nothing to check", res.Written, res.Attention)
	}
	golden(t, "draft.manifest.yaml", read(t, f, "manifest.yaml"))
	golden(t, "draft.story.md", read(t, f, "story.md"))
	res.Bundle = "BUNDLE" // a temp dir
	js, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "draft.json", string(js)+"\n")

	// A fresh draft is a valid bundle that covers the whole diff; the only
	// thing left to do is write the prose.
	lr, err := lint.Run(ctx, lint.Options{RepoDir: f.Repo.Dir})
	if err != nil {
		t.Fatal(err)
	}
	rep := lr.Report
	if len(rep.Errors) != 0 || rep.Coverage.Uncovered != 0 || rep.Coverage.Total != 37 {
		t.Errorf("lint after draft: errors %v, coverage %+v", rep.Errors, rep.Coverage)
	}
	for _, w := range rep.Warnings {
		if !strings.Contains(w.Msg, "has anchors but no prose") {
			t.Errorf("unexpected lint warning after draft: %s", w)
		}
	}
}

func TestDraftIsStable(t *testing.T) {
	f := fresh(t)
	draft(t, f)
	story, manifest := read(t, f, "story.md"), read(t, f, "manifest.yaml")
	res := draft(t, f)
	if res.Written || read(t, f, "story.md") != story || read(t, f, "manifest.yaml") != manifest {
		t.Error("a second draft with nothing changed rewrote the bundle")
	}
	// Untouched skeletons are proposed again, so they still count as new.
	if got := statuses(res); !strings.HasPrefix(got, "kept:why new:top-level ") {
		t.Errorf("statuses = %s", got)
	}
}

func TestDraftKeepsEditedSections(t *testing.T) {
	f := fresh(t)
	draft(t, f)

	// A person rewrites the Why section; an agent fills in one skeleton and
	// narrows another to a line range.
	f.EditBundle("story.md", "What problem does this change solve, and why this way? Replace this paragraph.",
		"Exports were not watertight.\r\n\r\nTrailing spaces stay.   ")
	f.EditBundle("story.md", "::anchor{path=\"plugins/blender/export.py\"}\n",
		"::anchor{path=\"plugins/blender/export.py\"}\n\nThe exporter repairs before writing.\n")
	f.EditBundle("story.md", "::anchor{path=\"core/mesh/legacy.py\"}\n::anchor{path=\"core/mesh/repair.py\"}\n",
		"::anchor{path=\"core/mesh/repair.py\" lines=\"21-28\"}\n\nThe new entry point.\n")
	edited := read(t, f, "story.md")
	section := func(src, id string) string {
		start := strings.Index(src, "{#"+id+" ")
		if start < 0 {
			t.Fatalf("no section %s", id)
		}
		start = strings.LastIndex(src[:start], "\n## ") + 1
		end := strings.Index(src[start+1:], "\n## ")
		if end < 0 {
			return src[start:]
		}
		return src[start : start+1+end]
	}

	res := draft(t, f)
	after := read(t, f, "story.md")
	for _, id := range []string{"why", "plugins-blender-export-py", "core-mesh"} {
		if section(after, id) != section(edited, id) {
			t.Errorf("section %s changed:\n%q\nwas:\n%q", id, section(after, id), section(edited, id))
		}
	}
	// core-mesh no longer explains legacy.py or the weld change, so draft
	// proposes sections for them under ids that are still free.
	want := "kept:why kept:core-mesh kept:plugins-blender-export-py new:top-level new:core-mesh-2 new:tests-core-mesh-repair-py " +
		"new:docs-new-name-md new:tools-run-sh new:tests-fixtures-meshes new:generated"
	if got := statuses(res); got != want {
		t.Errorf("statuses:\n %s\nwant:\n %s", got, want)
	}
	if !strings.Contains(after, "## Changes in core/mesh {#core-mesh-2 author=agent}\n::anchor{path=\"core/mesh/legacy.py\"}\n::anchor{path=\"core/mesh/repair.py\"}\n") {
		t.Errorf("no new section for the rest of core/mesh:\n%s", after)
	}

	// The edited agent sections keep their old hash, which no longer
	// matches, so every later run leaves them alone too.
	b, err := bundle.Load(f.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	for _, sec := range b.Story.Sections {
		rec, ok := b.Manifest.Sections[sec.ID]
		switch sec.ID {
		case "why":
			if ok {
				t.Error("the manifest records the human section")
			}
		case "plugins-blender-export-py", "core-mesh":
			if !ok || rec.Hash == sec.Hash() {
				t.Errorf("section %s: record %v, want the stale hash from before the edit", sec.ID, rec)
			}
		default:
			if !ok || rec.Hash != sec.Hash() {
				t.Errorf("section %s: record %v does not match its text", sec.ID, rec)
			}
		}
	}

	if again := draft(t, f); again.Written || read(t, f, "story.md") != after {
		t.Error("a further draft rewrote the bundle")
	}
}

func TestDraftAfterNewCommits(t *testing.T) {
	f := fresh(t)
	draft(t, f)
	f.EditBundle("story.md", "::anchor{path=\"core/mesh/legacy.py\"}\n::anchor{path=\"core/mesh/repair.py\"}\n",
		"::anchor{path=\"core/mesh/repair.py\" lines=\"21-28\"}\n::anchor{path=\"core/mesh/legacy.py\"}\n::anchor{path=\"core/mesh/gone.py\"}\n\nProse.\n")

	f.Repo.Write("core/mesh/io.py", "def read(path):\n    return open(path).read()\n")
	f.Repo.Write("deps.lock", "numpy==2.1.0\n") // back to the base version: no longer in the diff
	head := f.Repo.Commit("add io")

	res := draft(t, f)
	if res.Head != head || !strings.Contains(read(t, f, "manifest.yaml"), "head_sha: "+head+"\n") {
		t.Errorf("head not updated to %s:\n%s", head, read(t, f, "manifest.yaml"))
	}
	story := read(t, f, "story.md")
	if strings.Contains(story, "deps.lock") {
		t.Error("the untouched skim section outlived the change it covered")
	}
	if !strings.Contains(story, "{#core-mesh-2 author=agent}\n::anchor{path=\"core/mesh/io.py\"}\n::anchor{path=\"core/mesh/repair.py\"}\n") {
		t.Errorf("no section for the new file:\n%s", story)
	}
	want := []string{
		`story.md:10: section core-mesh: anchor path="core/mesh/repair.py" lines="21-28" was written for other commits; check that the range still covers what the section explains`,
		`story.md:12: section core-mesh: stale anchor path="core/mesh/gone.py": core/mesh/gone.py is not changed in this diff`,
	}
	if strings.Join(res.Attention, "\n") != strings.Join(want, "\n") {
		t.Errorf("attention:\n%s\nwant:\n%s", strings.Join(res.Attention, "\n"), strings.Join(want, "\n"))
	}
}

func TestDraftFollowsRebase(t *testing.T) {
	f := fresh(t)
	draft(t, f)
	// The target branch moves on and the PR branch is rebased onto it.
	f.Repo.Git("checkout", "-q", "main")
	f.Repo.Write("unrelated.txt", "on main\n")
	newMain := f.Repo.Commit("main moves")
	f.Repo.Git("checkout", "-q", "repair")
	f.Repo.Git("rebase", "-q", "main")

	res := draft(t, f)
	if res.Base != newMain {
		t.Errorf("base = %s, want the new merge base %s", res.Base, newMain)
	}
	if strings.Contains(read(t, f, "story.md"), "unrelated.txt") {
		t.Error("the target branch's own change was drafted into the story")
	}
}

func TestDraftDryRun(t *testing.T) {
	f := fresh(t)
	story, manifest := read(t, f, "story.md"), read(t, f, "manifest.yaml")
	res, err := Run(ctx, Options{RepoDir: f.Repo.Dir, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Written || len(res.Sections) != 9 || read(t, f, "story.md") != story || read(t, f, "manifest.yaml") != manifest {
		t.Errorf("dry run: written %v, %d sections, or files changed", res.Written, len(res.Sections))
	}
}

func TestDraftErrors(t *testing.T) {
	t.Run("no bundle", func(t *testing.T) {
		f := fixture.New(t)
		os.RemoveAll(f.Bundle)
		if _, err := Run(ctx, Options{RepoDir: f.Repo.Dir}); !errors.Is(err, ErrNoBundle) {
			t.Errorf("err = %v, want ErrNoBundle", err)
		}
	})
	t.Run("invalid story is left alone", func(t *testing.T) {
		f := fresh(t)
		f.EditBundle("story.md", "author=human}", "author=robot}")
		before := read(t, f, "story.md")
		var issues bundle.Issues
		if _, err := Run(ctx, Options{RepoDir: f.Repo.Dir}); !errors.As(err, &issues) {
			t.Errorf("err = %v, want bundle issues", err)
		}
		if read(t, f, "story.md") != before {
			t.Error("draft rewrote a story it could not parse")
		}
	})
	t.Run("unknown base", func(t *testing.T) {
		f := fresh(t)
		if _, err := Run(ctx, Options{RepoDir: f.Repo.Dir, Base: "no-such-branch"}); err == nil {
			t.Error("no error for a base that does not exist")
		}
	})
	t.Run("missing artifacts do not stop a draft", func(t *testing.T) {
		f := fresh(t)
		f.EditBundle("manifest.yaml", "artifacts: []", "artifacts:\n  - id: later\n    kind: log\n    path: artifacts/later.log\n    provenance: { class: author }")
		if _, err := Run(ctx, Options{RepoDir: f.Repo.Dir}); err != nil {
			t.Errorf("err = %v", err)
		}
		if !strings.Contains(read(t, f, "manifest.yaml"), "    hash: ") || !strings.HasSuffix(read(t, f, "manifest.yaml"), "provenance: { class: author }\n") {
			t.Errorf("manifest after draft:\n%s", read(t, f, "manifest.yaml"))
		}
	})
}

func TestInit(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		f := fixture.New(t)
		os.RemoveAll(f.Bundle)
		res, err := Init(ctx, InitOptions{RepoDir: f.Repo.Dir})
		if err != nil {
			t.Fatal(err)
		}
		// Two commits on the branch, so the title is the branch name.
		if res.BaseRef != "main" || res.BaseSHA != f.Base || res.HeadSHA != f.Head || res.Title != "repair" || res.GitHub != "" || !res.Excluded {
			t.Errorf("result = %+v", res)
		}
		if _, err := bundle.Load(f.Bundle); err != nil {
			t.Errorf("the scaffold is not a valid bundle: %v", err)
		}
		if got := f.Repo.Git("status", "--porcelain"); got != "" {
			t.Errorf("git status shows the bundle:\n%s", got)
		}
	})

	t.Run("refuses an existing bundle", func(t *testing.T) {
		f := fixture.New(t)
		before := read(t, f, "story.md")
		if _, err := Init(ctx, InitOptions{RepoDir: f.Repo.Dir}); err == nil || !strings.Contains(err.Error(), "already has a manifest.yaml") {
			t.Errorf("err = %v", err)
		}
		if read(t, f, "story.md") != before {
			t.Error("init overwrote a story")
		}
	})

	t.Run("github remote, pr and awkward title", func(t *testing.T) {
		for _, url := range []string{"git@github.com:org/cad-integration.git", "https://github.com/org/cad-integration"} {
			f := fixture.New(t)
			os.RemoveAll(f.Bundle)
			f.Repo.Git("remote", "add", "origin", url)
			res, err := Init(ctx, InitOptions{RepoDir: f.Repo.Dir, PR: 482, Title: `fix: "quotes" # and: colons`})
			if err != nil {
				t.Fatal(err)
			}
			if res.GitHub != "org/cad-integration" {
				t.Errorf("remote %s: GitHub = %q", url, res.GitHub)
			}
			b, err := bundle.Load(f.Bundle)
			if err != nil {
				t.Fatal(err)
			}
			if b.Manifest.Repo != "org/cad-integration" || b.Manifest.PR != 482 || b.Story.Title != `fix: "quotes" # and: colons` {
				t.Errorf("manifest %+v, title %q", b.Manifest, b.Story.Title)
			}
		}
	})

	t.Run("one commit gives its subject as the title", func(t *testing.T) {
		f := fixture.New(t)
		os.RemoveAll(f.Bundle)
		res, err := Init(ctx, InitOptions{RepoDir: f.Repo.Dir, Base: f.Head + "~1"})
		if err != nil {
			t.Fatal(err)
		}
		if res.Title != "tests, docs and housekeeping" {
			t.Errorf("Title = %q", res.Title)
		}
	})

	t.Run("custom bundle directory is not excluded", func(t *testing.T) {
		f := fixture.New(t)
		dir := filepath.Join(t.TempDir(), "b")
		res, err := Init(ctx, InitOptions{RepoDir: f.Repo.Dir, BundleDir: dir})
		if err != nil || res.Excluded {
			t.Errorf("res = %+v, err = %v", res, err)
		}
		if _, err := bundle.Load(dir); err != nil {
			t.Error(err)
		}
	})

	t.Run("exclude entry is added once", func(t *testing.T) {
		f := fixture.New(t)
		os.RemoveAll(f.Bundle)
		for range 2 {
			os.RemoveAll(f.Bundle)
			if _, err := Init(ctx, InitOptions{RepoDir: f.Repo.Dir}); err != nil {
				t.Fatal(err)
			}
		}
		data, err := os.ReadFile(filepath.Join(f.Repo.Dir, ".git", "info", "exclude"))
		if err != nil || bytes.Count(data, []byte("/.storied/\n")) != 1 {
			t.Errorf("exclude file (err %v):\n%s", err, data)
		}
	})

	t.Run("errors", func(t *testing.T) {
		f := fixture.New(t)
		os.RemoveAll(f.Bundle)
		for name, opts := range map[string]InitOptions{
			"head is on the base branch": {Base: "repair"},
			"unknown base":               {Base: "nope"},
			"bad github name":            {GitHub: "just-a-name"},
		} {
			opts.RepoDir = f.Repo.Dir
			if _, err := Init(ctx, opts); err == nil {
				t.Errorf("%s: no error", name)
			}
			if _, err := os.Stat(filepath.Join(f.Bundle, "manifest.yaml")); err == nil {
				t.Fatalf("%s: a bundle was written anyway", name)
			}
		}
		f.Repo.Git("checkout", "-q", "main")
		if _, err := Init(ctx, InitOptions{RepoDir: f.Repo.Dir}); err == nil || !strings.Contains(err.Error(), "pass --base") {
			t.Errorf("on the default branch: err = %v", err)
		}
	})
}
