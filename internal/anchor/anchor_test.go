package anchor_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"storied/internal/anchor"
	"storied/internal/bundle"
	"storied/internal/diff"
	"storied/internal/fixture"
	"storied/internal/gitx"
)

func resolve(t *testing.T, f *fixture.Fixture, story string, current bool) (*anchor.Resolution, *bundle.Story) {
	t.Helper()
	d, err := diff.Compute(context.Background(), gitx.Repo{Dir: f.Repo.Dir}, f.Base, f.Head)
	if err != nil {
		t.Fatal(err)
	}
	s, issues := bundle.ParseStory([]byte(story))
	if len(issues) != 0 {
		t.Fatalf("story issues: %v", issues)
	}
	bundleHead := f.Head
	if !current {
		bundleHead = f.Base // any other commit
	}
	return anchor.Resolve(d, s, f.Base, bundleHead), s
}

// describe names a unit as path:side+line, or path for a whole-file unit.
func describe(r *anchor.Resolution, u int) string {
	unit := r.Units[u]
	f := r.Diff.Files[unit.File]
	if unit.WholeFile() {
		return f.Path()
	}
	l := f.Hunks[unit.Hunk].Lines[unit.Line]
	if l.Kind == diff.Delete {
		return fmt.Sprintf("%s:-%d", f.Path(), l.Old)
	}
	return fmt.Sprintf("%s:+%d", f.Path(), l.New)
}

func describeAll(r *anchor.Resolution, units []int) string {
	out := make([]string, len(units))
	for i, u := range units {
		out[i] = describe(r, u)
	}
	return strings.Join(out, " ")
}

func TestResolveFixtureStory(t *testing.T) {
	f := fixture.New(t)
	r, _ := resolve(t, f, fixture.Story, true)

	// One row per anchor, in story order.
	type row struct {
		state anchor.State
		units string
	}
	want := [][]row{
		{}, // why
		{ // repair-pass
			{anchor.Exact, "core/mesh/repair.py:+21 core/mesh/repair.py:+22 core/mesh/repair.py:+23 core/mesh/repair.py:+24 " +
				"core/mesh/repair.py:+25 core/mesh/repair.py:+26 core/mesh/repair.py:+27 core/mesh/repair.py:+28"},
			{anchor.Exact, "plugins/blender/export.py:+2 plugins/blender/export.py:-4 plugins/blender/export.py:+5 " +
				"plugins/blender/export.py:+7 plugins/blender/export.py:+8"},
			{anchor.Unsupported, ""},
		},
		{ // weld
			{anchor.Exact, "core/mesh/repair.py:-8 core/mesh/repair.py:-9 core/mesh/repair.py:+8 core/mesh/repair.py:+9 core/mesh/repair.py:+10 " +
				"core/mesh/repair.py:+21 core/mesh/repair.py:+22 core/mesh/repair.py:+23 core/mesh/repair.py:+24 " +
				"core/mesh/repair.py:+25 core/mesh/repair.py:+26 core/mesh/repair.py:+27 core/mesh/repair.py:+28"},
			{anchor.Exact, "core/mesh/legacy.py:-1 core/mesh/legacy.py:-2 core/mesh/legacy.py:-3 core/mesh/legacy.py:-4"},
		},
		{ // tests
			{anchor.Exact, "tests/export/test_repair.py:+1 tests/export/test_repair.py:+2 tests/export/test_repair.py:+3 " +
				"tests/export/test_repair.py:+4 tests/export/test_repair.py:+5"},
			{anchor.Exact, "tests/fixtures/meshes/cube.obj:+3"},
			{anchor.Exact, "deps.lock:-1 deps.lock:+1"},
			{anchor.Stale, ""},
		},
	}
	for si, sec := range r.Sections {
		if len(sec.Claims) != len(want[si]) {
			t.Fatalf("section %d has %d claims, want %d", si, len(sec.Claims), len(want[si]))
		}
		for ci, c := range sec.Claims {
			got := row{c.State, describeAll(r, c.Units)}
			if got != want[si][ci] {
				t.Errorf("section %d anchor %d:\n got %+v\nwant %+v", si, ci, got, want[si][ci])
			}
			if (c.State == anchor.Exact) != (c.Reason == "") {
				t.Errorf("section %d anchor %d: state %s with reason %q", si, ci, c.State, c.Reason)
			}
		}
	}

	// The lines anchor outranks the path anchor on the same file, so the
	// new function is shown under repair-pass and only the rest under weld.
	if got, want := describeAll(r, r.Sections[2].Primary),
		"core/mesh/legacy.py:-1 core/mesh/legacy.py:-2 core/mesh/legacy.py:-3 core/mesh/legacy.py:-4 "+
			"core/mesh/repair.py:-8 core/mesh/repair.py:-9 core/mesh/repair.py:+8 core/mesh/repair.py:+9 core/mesh/repair.py:+10"; got != want {
		t.Errorf("weld shows\n %s\nwant\n %s", got, want)
	}
	if got := len(r.Sections[1].Primary); got != 13 {
		t.Errorf("repair-pass shows %d units, want 13", got)
	}

	var uncovered []int
	for u, owner := range r.Owner {
		if owner < 0 {
			uncovered = append(uncovered, u)
		}
	}
	if got, want := describeAll(r, uncovered),
		"README.md:-3 README.md:+3 core/mesh/legacy.py:-5 core/mesh/legacy.py:-6 docs/new-name.md logo.png tools/run.sh"; got != want {
		t.Errorf("uncovered:\n %s\nwant\n %s", got, want)
	}

	if got, want := r.Coverage(), (anchor.Coverage{Total: 37, Specific: 12, Broad: 15, Skim: 3, Uncovered: 7}); got != want {
		t.Errorf("Coverage = %+v, want %+v", got, want)
	}
	var sum anchor.Coverage
	for _, c := range r.FileCoverage() {
		sum.Total += c.Total
		sum.Specific += c.Specific
		sum.Broad += c.Broad
		sum.Skim += c.Skim
		sum.Uncovered += c.Uncovered
	}
	if sum != r.Coverage() {
		t.Errorf("file coverage sums to %+v, want %+v", sum, r.Coverage())
	}
}

const head = "---\ntitle: T\n---\n"

func TestResolveLinesAfterHeadMoved(t *testing.T) {
	f := fixture.New(t)
	story := head + "## A {#a author=human}\n::anchor{path=\"core/mesh/repair.py\" lines=\"21-28\"}\n::anchor{path=\"README.md\"}\n" +
		"::anchor{path=\"README.md\" lines=\"3\" side=\"old\"}\n"
	r, _ := resolve(t, f, story, false)
	claims := r.Sections[0].Claims
	if claims[2].State != anchor.Exact {
		t.Errorf("old-side lines anchor = %+v, want exact; the base did not move", claims[2])
	}
	if claims[0].State != anchor.Stale || len(claims[0].Units) != 0 {
		t.Errorf("lines anchor = %+v, want stale with no units", claims[0])
	}
	if claims[1].State != anchor.Exact {
		t.Errorf("path anchor = %+v, want exact; a path survives a new head", claims[1])
	}
}

func TestResolveEdgeCases(t *testing.T) {
	f := fixture.New(t)
	tests := []struct {
		name      string
		anchors   string
		state     anchor.State
		units     string
		everyFile bool
	}{
		{"deleted file by old path", `::anchor{path="core/mesh/legacy.py"}`, anchor.Exact,
			"core/mesh/legacy.py:-1 core/mesh/legacy.py:-2 core/mesh/legacy.py:-3 core/mesh/legacy.py:-4 core/mesh/legacy.py:-5 core/mesh/legacy.py:-6", false},
		{"renamed file by new path", `::anchor{path="docs/new-name.md"}`, anchor.Exact, "docs/new-name.md", false},
		{"renamed file by old path", `::anchor{path="docs/old-name.md"}`, anchor.Stale, "", false},
		{"binary file", `::anchor{path="logo.png"}`, anchor.Exact, "logo.png", false},
		{"mode change", `::anchor{glob="tools/*"}`, anchor.Exact, "tools/run.sh", false},
		{"single line", `::anchor{path="README.md" lines="3"}`, anchor.Exact, "README.md:+3", false},
		{"old side of a modified file", `::anchor{path="README.md" lines="1-9" side="old"}`, anchor.Exact, "README.md:-3", false},
		{"range with only context lines", `::anchor{path="README.md" lines="1-2"}`, anchor.Stale, "", false},
		{"new side of a deleted file", `::anchor{path="core/mesh/legacy.py" lines="1-4"}`, anchor.Stale, "", false},
		{"lines never claim a whole-file unit", `::anchor{path="logo.png" lines="1-9"}`, anchor.Stale, "", false},
		{"glob does not cross directories with one star", `::anchor{glob="*.py"}`, anchor.Stale, "", false},
		{"glob alternatives", `::anchor{glob="{README.md,deps.lock}"}`, anchor.Exact, "README.md:-3 README.md:+3 deps.lock:-1 deps.lock:+1", false},
		{"content is not resolved yet", `::anchor{content="1111111111111111111111111111111111111111" path="README.md"}`, anchor.Unsupported, "", false},
		{"commit is not resolved yet", `::anchor{commit="1111111111111111111111111111111111111111"}`, anchor.Unsupported, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := resolve(t, f, head+"## A {#a author=human}\n"+tt.anchors+"\n", true)
			c := r.Sections[0].Claims[0]
			if c.State != tt.state || describeAll(r, c.Units) != tt.units || c.EveryFile != tt.everyFile {
				t.Errorf("got %s %q everyFile=%v\nwant %s %q everyFile=%v", c.State, describeAll(r, c.Units), c.EveryFile, tt.state, tt.units, tt.everyFile)
			}
		})
	}
}

func TestResolveCatchAllGlob(t *testing.T) {
	f := fixture.New(t)
	r, _ := resolve(t, f, head+"## A {#a author=human}\n::anchor{glob=\"**\"}\n", true)
	c := r.Sections[0].Claims[0]
	if !c.EveryFile || len(c.Units) != len(r.Units) {
		t.Errorf("claim = state %s, %d units, everyFile=%v; want every unit and everyFile", c.State, len(c.Units), c.EveryFile)
	}
	if got, want := r.Coverage(), (anchor.Coverage{Total: 37, Broad: 37}); got != want {
		t.Errorf("Coverage = %+v, want %+v", got, want)
	}
}

func TestPrimarySection(t *testing.T) {
	f := fixture.New(t)
	// Every section claims README.md line 3; which one shows it?
	tests := []struct {
		name  string
		a, b  string
		owner int
		class anchor.Class
	}{
		{"more specific wins over earlier", `::anchor{path="README.md"}`, `::anchor{path="README.md" lines="3"}`, 1, anchor.Specific},
		{"path beats glob", `::anchor{glob="*.md"}`, `::anchor{path="README.md"}`, 1, anchor.Broad},
		{"glob beats skim glob", `::anchor{glob="*.md" skim}`, `::anchor{glob="*.md"}`, 1, anchor.Broad},
		{"earlier wins a tie", `::anchor{path="README.md"}`, `::anchor{path="README.md"}`, 0, anchor.Broad},
		{"skim alone is skim", `::anchor{glob="*.md" skim}`, ``, 0, anchor.Skim},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := resolve(t, f, head+"## A {#a author=human}\n"+tt.a+"\n## B {#b author=human}\n"+tt.b+"\n", true)
			for u := range r.Units {
				if describe(r, u) != "README.md:+3" {
					continue
				}
				if r.Owner[u] != tt.owner || r.Class[u] != tt.class {
					t.Errorf("owner %d class %s, want owner %d class %s", r.Owner[u], r.Class[u], tt.owner, tt.class)
				}
				return
			}
			t.Fatal("README.md:+3 is not a unit")
		})
	}
}

func TestUnitsFollowDiffOrder(t *testing.T) {
	f := fixture.New(t)
	r, _ := resolve(t, f, head+"## A {#a author=human}\n", true)
	var files []int
	for _, u := range r.Units {
		if len(files) == 0 || files[len(files)-1] != u.File {
			files = append(files, u.File)
		}
	}
	want := make([]int, len(r.Diff.Files))
	for i := range want {
		want[i] = i
	}
	if !reflect.DeepEqual(files, want) {
		t.Errorf("unit files = %v, want each file once in order %v", files, want)
	}
}
