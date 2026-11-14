package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"storied/internal/fixture"
)

func runCLI(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = Run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestLintCommand(t *testing.T) {
	f := fixture.New(t)
	repo := "--repo=" + f.Repo.Dir

	code, out, _ := runCLI("lint", repo)
	if code != ExitOK || !strings.HasSuffix(out, "0 errors, 4 warnings\n") {
		t.Errorf("lint: exit %d, output:\n%s", code, out)
	}
	if code, _, _ := runCLI("lint", repo, "--strict"); code != ExitError {
		t.Errorf("lint --strict with warnings: exit %d, want %d", code, ExitError)
	}

	code, out, _ = runCLI("lint", repo, "--json")
	var rep struct {
		Coverage struct{ Total, Uncovered int }
		Warnings []struct{ Message string }
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("lint --json: %v\n%s", err, out)
	}
	if code != ExitOK || rep.Coverage.Total != 37 || rep.Coverage.Uncovered != 7 || len(rep.Warnings) != 4 {
		t.Errorf("lint --json: exit %d, report %+v", code, rep)
	}

	f.EditBundle("story.md", "author=human}", "author=robot}")
	code, out, errOut := runCLI("lint", repo)
	if code != ExitError || !strings.Contains(out, `error: story.md:8: author "robot" must be human or agent`) || errOut != "" {
		t.Errorf("lint on an invalid bundle: exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	code, out, _ = runCLI("lint", repo, "--json")
	if code != ExitError || !strings.Contains(out, `"errors": [`) {
		t.Errorf("lint --json on an invalid bundle: exit %d\n%s", code, out)
	}
}

func TestRenderCommand(t *testing.T) {
	f := fixture.New(t)
	repo := "--repo=" + f.Repo.Dir

	code, out, errOut := runCLI("render", repo)
	// git reports the worktree root with symlinks resolved, which on macOS
	// differs from the temp dir path the fixture was given.
	root, err := filepath.EvalSymlinks(f.Repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, ".storied", "story.html")
	if code != ExitOK || out != "wrote "+want+"\n" || !strings.Contains(errOut, "4 lint warnings") {
		t.Errorf("render: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	page, err := os.ReadFile(want)
	if err != nil || !bytes.HasPrefix(page, []byte("<!doctype html>")) {
		t.Errorf("render wrote %q..., err %v", page[:min(len(page), 40)], err)
	}

	elsewhere := filepath.Join(t.TempDir(), "out.html")
	if code, _, _ := runCLI("render", repo, "-o", elsewhere); code != ExitOK {
		t.Errorf("render -o: exit %d", code)
	}
	if other, err := os.ReadFile(elsewhere); err != nil || !bytes.Equal(other, page) {
		t.Errorf("render -o wrote a different page (err %v)", err)
	}
	if code, out, _ := runCLI("render", repo, "-o", "-"); code != ExitOK || out != string(page) {
		t.Errorf("render -o -: exit %d, %d bytes on stdout, want the page", code, len(out))
	}

	// The page it just wrote into the bundle must not upset the next run.
	if code, _, _ := runCLI("lint", repo); code != ExitOK {
		t.Errorf("lint after render: exit %d", code)
	}

	f.EditBundle("story.md", "author=human}", "author=robot}")
	os.Remove(want)
	code, out, errOut = runCLI("render", repo)
	if code != ExitError || out != "" || !strings.Contains(errOut, `error: story.md:8: author "robot"`) {
		t.Errorf("render on an invalid bundle: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if _, err := os.Stat(want); err == nil {
		t.Error("render wrote a page for an invalid bundle")
	}
}

func TestCommandUsage(t *testing.T) {
	tests := []struct {
		args []string
		code int
		want string // in stderr
	}{
		{[]string{"lint", "--nope"}, ExitUsage, "flag provided but not defined: -nope"},
		{[]string{"lint", "extra"}, ExitUsage, `storied lint: unexpected argument "extra"`},
		{[]string{"render", "-o"}, ExitUsage, "flag needs an argument: -o"},
		{[]string{"lint", "-h"}, ExitOK, "Usage: storied lint [flags]"},
		{[]string{"lint", "--repo", t.TempDir()}, ExitError, "storied lint: git rev-parse --show-toplevel:"},
	}
	for _, tt := range tests {
		code, out, errOut := runCLI(tt.args...)
		if code != tt.code || !strings.Contains(errOut, tt.want) || out != "" {
			t.Errorf("storied %s: exit %d, want %d\nstdout: %s\nstderr: %s\nwant stderr containing %q",
				strings.Join(tt.args, " "), code, tt.code, out, errOut, tt.want)
		}
	}
}

func TestInitAndDraftCommands(t *testing.T) {
	f := fixture.New(t)
	repo := "--repo=" + f.Repo.Dir
	if code, _, errOut := runCLI("init", repo); code != ExitError || !strings.Contains(errOut, "already has a manifest.yaml") {
		t.Errorf("init over a bundle: exit %d, stderr: %s", code, errOut)
	}
	os.RemoveAll(f.Bundle)
	if code, _, errOut := runCLI("draft", repo); code != ExitError || !strings.Contains(errOut, "run storied init first") {
		t.Errorf("draft without a bundle: exit %d, stderr: %s", code, errOut)
	}

	code, out, errOut := runCLI("init", repo, "--title", "Repair pass", "--github", "org/cad", "--pr", "7")
	for _, want := range []string{"  title  Repair pass\n", "  base   main (" + f.Base[:9] + ")\n", "  repo   org/cad\n", "added the bundle directory to .git/info/exclude\n", "next: storied draft\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("init output lacks %q:\n%s", want, out)
		}
	}
	if code != ExitOK || errOut != "" {
		t.Errorf("init: exit %d, stderr: %s", code, errOut)
	}

	// On its own, draft lists the blocks and leaves the story alone.
	code, out, _ = runCLI("draft", repo)
	for _, want := range []string{
		"  kept  why  (0 files, 0 lines)\n",
		"13 of 13 change blocks not in the story yet:\n",
		"  4cadd9b  core/mesh/repair.py:8             +3 -2       def weld(vertices, tol=1e-6):\n",
		"  36ad9b6  docs/new-name.md                  whole file\n",
		`attach each to the section that explains it with ::anchor{hunk="<id>"}`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("draft output lacks %q:\n%s", want, out)
		}
	}
	if code != ExitOK || strings.Contains(out, "new section") {
		t.Errorf("draft: exit %d\n%s", code, out)
	}

	code, out, _ = runCLI("draft", repo, "--by-file", "--dry-run")
	if code != ExitOK || !strings.Contains(out, "dry run: would write 8 new sections; nothing written") {
		t.Errorf("draft --by-file --dry-run: exit %d\n%s", code, out)
	}
	code, out, _ = runCLI("draft", repo, "--by-file")
	for _, want := range []string{
		"  kept  why  (0 files, 0 lines)\n",
		"  new   core-mesh  (2 files, 19 lines)  needs prose\n",
		"every one of the 13 change blocks is in the story\n",
		"wrote 8 new sections to ",
		"next: write the prose for 8 sections, then run storied lint\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("draft output lacks %q:\n%s", want, out)
		}
	}
	if code != ExitOK {
		t.Errorf("draft: exit %d", code)
	}

	code, out, _ = runCLI("draft", repo, "--json")
	var res struct {
		Written  bool
		Sections []struct {
			ID         string
			NeedsProse bool `json:"needs_prose"`
		}
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("draft --json: %v\n%s", err, out)
	}
	if code != ExitOK || res.Written || len(res.Sections) != 9 || !res.Sections[1].NeedsProse {
		t.Errorf("draft --json: exit %d, %+v", code, res)
	}

	// The drafted bundle lints without errors and renders.
	if code, out, _ := runCLI("lint", repo); code != ExitOK || !strings.Contains(out, "uncovered   0   0%") {
		t.Errorf("lint after draft: exit %d\n%s", code, out)
	}
	if code, _, _ := runCLI("render", repo, "-o", filepath.Join(t.TempDir(), "p.html")); code != ExitOK {
		t.Errorf("render after draft: exit %d", code)
	}

	f.EditBundle("story.md", "author=human}", "author=robot}")
	if code, _, errOut := runCLI("draft", repo); code != ExitError || !strings.Contains(errOut, `error: story.md:5: author "robot" must be human or agent`) {
		t.Errorf("draft on an invalid story: exit %d, stderr: %s", code, errOut)
	}
}
