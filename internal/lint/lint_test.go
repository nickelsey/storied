package lint

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"storied/internal/fixture"
)

var update = flag.Bool("update", false, "rewrite golden files")

func run(t *testing.T, f *fixture.Fixture) *Result {
	t.Helper()
	res, err := Run(context.Background(), Options{RepoDir: f.Repo.Dir})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/lint -update to create it)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("output differs from %s; run with -update and review the diff.\ngot:\n%s", path, got)
	}
}

func TestReportGolden(t *testing.T) {
	res := run(t, fixture.New(t))
	if res.Diff == nil || res.Resolution == nil {
		t.Fatal("Result has no diff or resolution for a valid bundle")
	}

	var text bytes.Buffer
	if err := res.Report.WriteText(&text); err != nil {
		t.Fatal(err)
	}
	golden(t, "fixture.txt", text.Bytes())

	js, err := json.MarshalIndent(res.Report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "fixture.json", append(js, '\n'))
}

func hasWarning(r *Report, want string) bool {
	for _, w := range r.Warnings {
		if strings.Contains(w.String(), want) {
			return true
		}
	}
	return false
}

func TestCatchAllGlob(t *testing.T) {
	f := fixture.New(t)
	f.WriteBundle("story.md", "---\ntitle: T\n---\n## All {#all author=agent}\n::anchor{glob=\"**\"}\n"+
		"::artifact{id=\"mesh-compare\"}\n::artifact{id=\"repair-notes\"}\n::artifact{id=\"drag-chart\"}\n::artifact{id=\"export-tests\"}\nProse.\n")
	rep := run(t, f).Report
	if c := rep.Coverage; c.Specific != 0 || c.Broad != c.Total || c.Total == 0 {
		t.Errorf("coverage = %+v, want everything broad and nothing specific", c)
	}
	if !hasWarning(rep, `story.md:5: anchor glob="**" matches every changed file`) {
		t.Errorf("no catch-all warning in %v", rep.Warnings)
	}
	if len(rep.Warnings) != 1 {
		t.Errorf("warnings = %v, want only the catch-all one", rep.Warnings)
	}
}

func TestCleanBundleHasNoWarnings(t *testing.T) {
	f := fixture.New(t)
	f.WriteBundle("story.md", "---\ntitle: T\n---\n## Code {#code author=human}\n"+
		"::anchor{glob=\"{core,plugins,tests,docs,tools}/**\"}\n::anchor{path=\"README.md\"}\n::anchor{glob=\"{deps.lock,logo.png}\" skim}\n"+
		"::artifact{id=\"mesh-compare\"}\n::artifact{id=\"repair-notes\"}\n::artifact{id=\"drag-chart\"}\n::artifact{id=\"export-tests\"}\nProse.\n")
	rep := run(t, f).Report
	if len(rep.Errors) != 0 || len(rep.Warnings) != 0 {
		t.Errorf("errors %v, warnings %v; want none", rep.Errors, rep.Warnings)
	}
}

func TestWarnings(t *testing.T) {
	t.Run("unused artifact", func(t *testing.T) {
		f := fixture.New(t)
		f.EditBundle("story.md", "::artifact{id=\"export-tests\"}\n", "")
		if rep := run(t, f).Report; !hasWarning(rep, "manifest.yaml:24: artifact export-tests is not used by any section") {
			t.Errorf("warnings = %v", rep.Warnings)
		}
	})

	t.Run("head moved on", func(t *testing.T) {
		f := fixture.New(t)
		f.Repo.Write("README.md", "# cad\n\nMesh tools, repaired.\n")
		newHead := f.Repo.Commit("reword")
		rep := run(t, f).Report
		if rep.Head != newHead || rep.BundleHead != f.Head {
			t.Errorf("Head = %s, BundleHead = %s; want %s and %s", rep.Head, rep.BundleHead, newHead, f.Head)
		}
		if !hasWarning(rep, "manifest.yaml: head_sha is "+f.Head[:9]+" but the diff ends at "+newHead[:9]) {
			t.Errorf("no head warning in %v", rep.Warnings)
		}
		if !hasWarning(rep, `stale anchor path="core/mesh/repair.py" lines="21-28": line numbers were written for another head commit`) {
			t.Errorf("no stale lines warning in %v", rep.Warnings)
		}
		// The path anchor on the same file now shows those lines instead,
		// and the old-side lines anchor still holds because the base did
		// not move.
		if got := rep.Sections[2].Shown; got != 17 {
			t.Errorf("weld shows %d lines, want 17", got)
		}
	})

	t.Run("explicit head keeps the bundle current", func(t *testing.T) {
		f := fixture.New(t)
		f.Repo.Write("README.md", "later\n")
		f.Repo.Commit("later")
		res, err := Run(context.Background(), Options{RepoDir: f.Repo.Dir, Head: f.Head})
		if err != nil {
			t.Fatal(err)
		}
		if res.Report.BundleHead != "" || res.Report.Head != f.Head {
			t.Errorf("Head = %s, BundleHead = %q; want the bundle's head and no mismatch", res.Report.Head, res.Report.BundleHead)
		}
	})

	t.Run("large section", func(t *testing.T) {
		f := fixture.New(t)
		f.Repo.Write("big.txt", strings.Repeat("line\n", LargeSection+1))
		head := f.Repo.Commit("big")
		f.EditBundle("manifest.yaml", "head_sha: "+f.Head, "head_sha: "+head)
		f.EditBundle("story.md", "## Why {#why author=human}\n", "## Why {#why author=human}\n::anchor{path=\"big.txt\"}\n")
		if rep := run(t, f).Report; !hasWarning(rep, "story.md:8: section why claims 401 changed lines; more than 400") {
			t.Errorf("warnings = %v", rep.Warnings)
		}
	})

	t.Run("empty diff", func(t *testing.T) {
		f := fixture.New(t)
		res, err := Run(context.Background(), Options{RepoDir: f.Repo.Dir, Base: f.Head})
		if err != nil {
			t.Fatal(err)
		}
		if !hasWarning(res.Report, "the diff is empty") {
			t.Errorf("warnings = %v", res.Report.Warnings)
		}
		if !hasWarning(res.Report, `side="old": line numbers were written for another base commit`) {
			t.Errorf("no stale old-side warning in %v", res.Report.Warnings)
		}
	})
}

func TestErrors(t *testing.T) {
	t.Run("invalid bundle", func(t *testing.T) {
		f := fixture.New(t)
		f.EditBundle("story.md", "author=human}", "author=robot}")
		f.EditBundle("manifest.yaml", "schema: storied/v1", "schema: storied/v9")
		res := run(t, f)
		if res.Resolution != nil {
			t.Error("an invalid bundle was resolved")
		}
		var got []string
		for _, e := range res.Report.Errors {
			got = append(got, e.String())
		}
		want := "manifest.yaml:1: schema \"storied/v9\" is not supported; want storied/v1\n" +
			"story.md:8: author \"robot\" must be human or agent"
		if strings.Join(got, "\n") != want {
			t.Errorf("errors:\n%s\nwant:\n%s", strings.Join(got, "\n"), want)
		}
		var text bytes.Buffer
		if err := res.Report.WriteText(&text); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(text.String(), "error: manifest.yaml:1:") || !strings.Contains(text.String(), "2 errors") {
			t.Errorf("text report:\n%s", text.String())
		}
	})

	t.Run("no bundle", func(t *testing.T) {
		f := fixture.New(t)
		res, err := Run(context.Background(), Options{RepoDir: f.Repo.Dir, BundleDir: filepath.Join(f.Repo.Dir, "nope")})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Report.Errors) != 3 {
			t.Errorf("errors = %v, want one per missing bundle file", res.Report.Errors)
		}
	})

	t.Run("commits not in this clone", func(t *testing.T) {
		f := fixture.New(t)
		f.EditBundle("manifest.yaml", "base_sha: "+f.Base, "base_sha: "+strings.Repeat("a", 40))
		rep := run(t, f).Report
		if len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0].Msg, "cannot diff aaaaaaaaa..HEAD") {
			t.Errorf("errors = %v", rep.Errors)
		}
	})

	t.Run("not a repository", func(t *testing.T) {
		if _, err := Run(context.Background(), Options{RepoDir: t.TempDir()}); err == nil {
			t.Error("Run outside a repository returned no error")
		}
	})
}

func TestBundleInSubdirectoryOfRepo(t *testing.T) {
	// Run from a subdirectory: the default bundle is still at the root.
	f := fixture.New(t)
	res, err := Run(context.Background(), Options{RepoDir: filepath.Join(f.Repo.Dir, "core", "mesh")})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Report.Errors) != 0 || res.Report.Coverage.Total != 37 {
		t.Errorf("report = %+v", res.Report)
	}
}
