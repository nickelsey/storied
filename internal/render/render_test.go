package render

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2/lexers"

	"github.com/nickelsey/storied/internal/diff"
	"github.com/nickelsey/storied/internal/fixture"
	"github.com/nickelsey/storied/internal/lint"
)

var update = flag.Bool("update", false, "rewrite golden files")

func renderFixture(t *testing.T, f *fixture.Fixture) string {
	t.Helper()
	res, err := lint.Run(context.Background(), lint.Options{RepoDir: f.Repo.Dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Report.Errors) > 0 {
		t.Fatalf("bundle errors: %v", res.Report.Errors)
	}
	var buf bytes.Buffer
	if err := Page(&buf, res); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// body returns the page without the embedded stylesheet and script, which
// are checked in as their own files.
func body(t *testing.T, page string) string {
	t.Helper()
	for _, tag := range []string{"style", "script"} {
		start := strings.Index(page, "<"+tag+">")
		end := strings.Index(page, "</"+tag+">")
		if start < 0 || end < start {
			t.Fatalf("page has no <%s> element", tag)
		}
		page = page[:start+len(tag)+2] + page[end:]
	}
	return page
}

func TestPageGolden(t *testing.T) {
	got := body(t, renderFixture(t, fixture.New(t)))
	golden := filepath.Join("testdata", "fixture.html")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/render -update to create it)", err)
	}
	if got != string(want) {
		t.Errorf("page differs from %s; run with -update and review the diff", golden)
	}
}

// The page must work from disk with no network: nothing may load from
// another origin, and the policy in the page enforces that in the browser.
func TestPageIsSelfContained(t *testing.T) {
	page := renderFixture(t, fixture.New(t))
	if !strings.Contains(page, `content="default-src 'none'; img-src data:; style-src 'unsafe-inline'; script-src 'unsafe-inline'"`) {
		t.Error("page has no content security policy")
	}
	for _, re := range []string{`<link\b`, `<script[^>]+src=`, `<iframe\b`, `\bsrc="(?:[^d]|d[^a])`, `url\(`, `@import`} {
		if m := regexp.MustCompile(re).FindString(page); m != "" {
			t.Errorf("page contains %q, which could load an external resource", m)
		}
	}
}

func TestPageEscapesBundleContent(t *testing.T) {
	f := fixture.New(t)
	f.EditBundle("story.md", "title: Switch mesh export to watertight repair pass", `title: "<script>alert(1)</script>"`)
	f.EditBundle("story.md", "## Why {#why author=human}", "## Why <img src=x onerror=alert(2)> {#why author=human}")
	f.WriteBundle("artifacts/pytest.log", "<script>alert(3)</script>\n")
	f.WriteBundle("artifacts/notes.md", "<script>alert(4)</script>\n\n[x](javascript:alert(5)) ![remote](https://example.com/a.png)\n")
	f.WriteBundle("agent.md", "<script>alert(6)</script>\n")
	page := body(t, renderFixture(t, f))
	for _, bad := range []string{"<script>alert", "<img src=x", "javascript:alert"} {
		if strings.Contains(page, bad) {
			t.Errorf("page contains unescaped %q", bad)
		}
	}
	if !strings.Contains(page, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Error("the title is missing from the page")
	}
}

func TestPageFlags(t *testing.T) {
	t.Run("head moved on", func(t *testing.T) {
		f := fixture.New(t)
		f.Repo.Write("README.md", "# cad\n\nMesh tools, repaired.\n")
		head := f.Repo.Commit("reword")
		page := renderFixture(t, f)
		for _, want := range []string{
			"This story was written for <code>" + f.Head[:9] + "</code>. The diff below ends at <code>" + head[:9] + "</code>",
			`Stale anchor <code>path=&#34;core/mesh/repair.py&#34; lines=&#34;21-28&#34;</code>`,
			`<span class="outdated">produced at ` + f.Head[:9] + `, not the commit shown here</span>`,
		} {
			if !strings.Contains(page, want) {
				t.Errorf("page lacks %q", want)
			}
		}
	})

	t.Run("fallback image", func(t *testing.T) {
		f := fixture.New(t)
		f.EditBundle("manifest.yaml", "    path: artifacts/drag.vl.json\n", "    path: artifacts/drag.vl.json\n    fallback: artifacts/mesh-after.png\n")
		page := renderFixture(t, f)
		if strings.Contains(page, "cannot be shown on a static page") {
			t.Error("placeholder shown although the artifact has a fallback image")
		}
		if got := strings.Count(page, `<img src="data:image/png;base64,`); got != 3 {
			t.Errorf("%d inline images, want 3", got)
		}
	})

	t.Run("long log is cut", func(t *testing.T) {
		f := fixture.New(t)
		f.WriteBundle("artifacts/pytest.log", strings.Repeat("x", maxText+10))
		page := renderFixture(t, f)
		if !strings.Contains(page, "Cut off here") || strings.Contains(page, strings.Repeat("x", maxText+1)) {
			t.Error("long log was not cut at maxText")
		}
	})

	t.Run("no pr means no links", func(t *testing.T) {
		f := fixture.New(t)
		f.EditBundle("manifest.yaml", "pr: 482\n", "")
		if page := renderFixture(t, f); strings.Contains(page, "/pull/") {
			t.Error("page links to a PR the manifest does not name")
		}
	})
}

func TestPageRefusesInvalidBundle(t *testing.T) {
	f := fixture.New(t)
	f.EditBundle("story.md", "author=human}", "author=robot}")
	res, err := lint.Run(context.Background(), lint.Options{RepoDir: f.Repo.Dir})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Page(&buf, res); err == nil || buf.Len() != 0 {
		t.Errorf("Page = %v with %d bytes written; want an error and no output", err, buf.Len())
	}
}

func TestSegments(t *testing.T) {
	// One letter per hunk line: space for context, x for a change.
	hunk := func(shape string) diff.Hunk {
		var h diff.Hunk
		for _, c := range shape {
			k := diff.Context
			if c == 'x' {
				k = diff.Add
			}
			h.Lines = append(h.Lines, diff.Line{Kind: k})
		}
		return h
	}
	tests := []struct {
		name  string
		shape string
		owned []int
		want  [][2]int
	}{
		{"whole hunk", "   xx   ", []int{3, 4}, [][2]int{{0, 8}}},
		{"context is capped at three", "     x     ", []int{5}, [][2]int{{2, 9}}},
		{"stops at a change it does not own", " x x ", []int{1}, [][2]int{{0, 3}}},
		{"adjacent foreign change is left out", "  xx  ", []int{3}, [][2]int{{3, 6}}},
		{"two owned runs around a foreign one", "x x x", []int{0, 4}, [][2]int{{0, 2}, {3, 5}}},
		{"owned runs with shared context merge", "x  x", []int{0, 3}, [][2]int{{0, 4}}},
		{"distant owned runs stay apart", "x        x", []int{0, 9}, [][2]int{{0, 4}, {6, 10}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owned := map[int]bool{}
			for _, i := range tt.owned {
				owned[i] = true
			}
			if got := segments(hunk(tt.shape), owned); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("segments = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHighlightLines(t *testing.T) {
	py := lexers.Match("a.py")
	if py == nil {
		t.Fatal("no lexer for Python")
	}
	t.Run("multi-line string keeps its colour on every line", func(t *testing.T) {
		got := highlightLines(py, []string{`x = """a`, `b <c>`, `d"""`})
		if len(got) != 3 || !strings.Contains(got[1], `<span class="hs">b &lt;c&gt;</span>`) {
			t.Errorf("got %q", got)
		}
	})
	t.Run("blank and trailing blank lines keep their place", func(t *testing.T) {
		got := highlightLines(py, []string{"", "x = 1", "", ""})
		if len(got) != 4 || got[0] != "" || got[2] != "" || got[3] != "" || !strings.Contains(got[1], `<span class="hn">1</span>`) {
			t.Errorf("got %q", got)
		}
	})
	t.Run("unknown file type is escaped and left plain", func(t *testing.T) {
		got := highlightLines(lexers.Match("data.unknown-ext"), []string{"a < b", "c & d"})
		if want := []string{"a &lt; b", "c &amp; d"}; !reflect.DeepEqual(got, want) {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("no lines", func(t *testing.T) {
		if got := highlightLines(py, nil); len(got) != 0 {
			t.Errorf("got %q", got)
		}
	})
}

func TestHunkHighlightsEachSide(t *testing.T) {
	h := diff.Hunk{Lines: []diff.Line{
		{Kind: diff.Context, Text: "def f():"},
		{Kind: diff.Delete, Text: "    return 1\r"},
		{Kind: diff.Add, Text: "    return 'one'"},
	}}
	got := newHighlighter().hunk("a.py", h)
	if len(got) != 3 || !strings.Contains(string(got[1]), `<span class="hn">1</span>`) ||
		!strings.Contains(string(got[2]), `<span class="hs">&#39;one&#39;</span>`) || strings.Contains(string(got[1]), "\r") {
		t.Errorf("got %q", got)
	}
}

func TestPageKeepsWrittenOrder(t *testing.T) {
	f := fixture.New(t)
	res, err := lint.Run(context.Background(), lint.Options{RepoDir: f.Repo.Dir})
	if err != nil {
		t.Fatal(err)
	}
	r := res.Resolution
	// Two blocks of one file and one of another, written a2, b, a1.
	a1, a2, b := -1, -1, -1
	first := map[int]int{}
	for i, blk := range r.Blocks {
		if blk.Hunk < 0 {
			continue
		}
		if j, ok := first[blk.File]; ok && a2 < 0 {
			a1, a2 = j, i
		} else if !ok {
			first[blk.File] = i
		}
	}
	for i, blk := range r.Blocks {
		if a1 >= 0 && blk.Hunk >= 0 && blk.File != r.Blocks[a1].File {
			b = i
			break
		}
	}
	if a2 < 0 || b < 0 {
		t.Fatal("the fixture has no file with two change blocks")
	}
	story := "---\ntitle: T\n---\n\n## Walk {#walk author=human}\n\nAcross two files and back.\n\n"
	for _, i := range []int{a2, b, a1} {
		story += "::anchor{hunk=\"" + r.ShortID[i] + "\"}\n"
	}
	f.WriteBundle("story.md", story)

	page := renderFixture(t, f)
	sec := page[strings.Index(page, `id="s-walk"`):strings.Index(page, `id="uncovered"`)]
	var paths []string
	for _, m := range regexp.MustCompile(`class="path">([^<]*)<`).FindAllStringSubmatch(sec, -1) {
		paths = append(paths, m[1])
	}
	pa, pb := r.Diff.Files[r.Blocks[a1].File].Path(), r.Diff.Files[r.Blocks[b].File].Path()
	if want := []string{pa, pb, pa}; !reflect.DeepEqual(paths, want) {
		t.Errorf("files in the section: %q, want %q", paths, want)
	}
	// Each block's first line is linked to GitHub; the links must come in
	// the written order.
	link := func(i int) int {
		blk := r.Blocks[i]
		l := r.Diff.Files[blk.File].Hunks[blk.Hunk].Lines[blk.First]
		ref := "#diff-" + pathHash(r.Diff.Files[blk.File].Path()) + "R" + strconv.Itoa(l.New) + `"`
		if l.Kind == diff.Delete {
			ref = "#diff-" + pathHash(r.Diff.Files[blk.File].Path()) + "L" + strconv.Itoa(l.Old) + `"`
		}
		at := strings.Index(sec, ref)
		if at < 0 {
			t.Fatalf("section has no link %s", ref)
		}
		return at
	}
	if x, y, z := link(a2), link(b), link(a1); !(x < y && y < z) {
		t.Errorf("blocks appear at %d, %d, %d; want them in the written order", x, y, z)
	}
}
