package diff

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"storied/internal/gittest"
	"storied/internal/gitx"
)

func numbered(prefix string, n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "%s line %d\n", prefix, i)
	}
	return b.String()
}

// fixture builds a base and a head commit that together exercise every kind
// of file change the parser must handle.
func fixture(t *testing.T) (repo *gittest.Repo, base, head string) {
	r := gittest.New(t)
	r.Write("mod.txt", "one\ntwo\nthree\n")
	r.Write("del.txt", "gone\n")
	r.Write("old/name.txt", numbered("name", 10))
	r.Write("redit.txt", numbered("redit", 20))
	r.Write("bin.dat", "a\x00b\x00c")
	r.Write("script.sh", "#!/bin/sh\n")
	r.Write("crlf.txt", "a\r\nb\r\n")
	r.Write("dir with space/file name.txt", "x\n")
	r.Write("noeol.txt", "x\ny")
	r.Write("ünï.txt", "u\n")
	r.Write("link", "mod.txt")
	r.Git("add", "-A")
	// Record link as a symlink in the index without needing a real one,
	// which Windows may not allow.
	r.Git("update-index", "--cacheinfo", "120000,"+r.Git("rev-parse", ":link")+",link")
	base = r.CommitIndex("base")

	r.Write("mod.txt", "one\n2\nthree\nfour\n")
	r.Remove("del.txt")
	r.Remove("old/name.txt")
	r.Write("new/name.txt", numbered("name", 10))
	r.Remove("redit.txt")
	r.Write("redit2.txt", strings.Replace(numbered("redit", 20), "redit line 7\n", "changed\n", 1))
	r.Write("bin.dat", "a\x00b\x00d")
	r.Write("crlf.txt", "a\r\nB\r\n")
	r.Write("dir with space/file name.txt", "y\n")
	r.Write("noeol.txt", "x\nz")
	r.Write("ünï.txt", "v\n")
	r.Write("add.txt", "new\n")
	r.Write("empty.txt", "")
	r.Git("add", "-A")
	// Where symlinks are unsupported (Windows), add keeps link recorded as
	// a symlink, so say outright that it is now a regular file.
	r.Git("update-index", "--cacheinfo", "100644,"+r.Git("rev-parse", ":link")+",link")
	// core.fileMode is off on Windows, so set the executable bit in the index.
	r.Git("update-index", "--chmod=+x", "script.sh")
	head = r.CommitIndex("head")
	return r, base, head
}

func summarize(f File) string {
	added, deleted := f.Stat()
	s := fmt.Sprintf("%s %s +%d -%d", f.Status, f.Path(), added, deleted)
	if f.OldPath != "" && f.NewPath != "" && f.OldPath != f.NewPath {
		s += " from " + f.OldPath
	}
	if f.OldMode != f.NewMode {
		s += fmt.Sprintf(" mode %q->%q", f.OldMode, f.NewMode)
	}
	if f.Binary {
		s += " binary"
	}
	return s
}

func TestCompute(t *testing.T) {
	r, base, head := fixture(t)
	d, err := Compute(context.Background(), gitx.Repo{Dir: r.Dir}, base, head)
	if err != nil {
		t.Fatal(err)
	}
	if d.Base != base || d.Head != head {
		t.Errorf("Base, Head = %s, %s; want %s, %s", d.Base, d.Head, base, head)
	}

	want := []string{
		`added add.txt +1 -0 mode ""->"100644"`,
		`modified bin.dat +0 -0 binary`,
		`modified crlf.txt +1 -1`,
		`deleted del.txt +0 -1 mode "100644"->""`,
		`modified dir with space/file name.txt +1 -1`,
		`added empty.txt +0 -0 mode ""->"100644"`,
		`type-changed link +1 -1 mode "120000"->"100644"`,
		`modified mod.txt +2 -1`,
		`renamed new/name.txt +0 -0 from old/name.txt`,
		`modified noeol.txt +1 -1`,
		`renamed redit2.txt +1 -1 from redit.txt`,
		`modified script.sh +0 -0 mode "100644"->"100755"`,
		`modified ünï.txt +1 -1`,
	}
	var got []string
	for _, f := range d.Files {
		got = append(got, summarize(f))
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("files:\n got  %s\n want %s", strings.Join(got, "\n      "), strings.Join(want, "\n      "))
	}

	files := map[string]File{}
	for _, f := range d.Files {
		files[f.Path()] = f
	}

	t.Run("line numbers", func(t *testing.T) {
		want := []Line{
			{Kind: Context, Old: 1, New: 1, Text: "one"},
			{Kind: Delete, Old: 2, Text: "two"},
			{Kind: Add, New: 2, Text: "2"},
			{Kind: Context, Old: 3, New: 3, Text: "three"},
			{Kind: Add, New: 4, Text: "four"},
		}
		hunks := files["mod.txt"].Hunks
		if len(hunks) != 1 || !reflect.DeepEqual(hunks[0].Lines, want) {
			t.Errorf("mod.txt hunks = %+v, want one hunk with lines %+v", hunks, want)
		}
		if h := hunks[0]; h.OldStart != 1 || h.OldLines != 3 || h.NewStart != 1 || h.NewLines != 4 {
			t.Errorf("mod.txt hunk range = -%d,%d +%d,%d; want -1,3 +1,4", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
		}
	})

	t.Run("rename with edit keeps both line numbers", func(t *testing.T) {
		var changed []Line
		for _, l := range files["redit2.txt"].Hunks[0].Lines {
			if l.Kind != Context {
				changed = append(changed, l)
			}
		}
		want := []Line{
			{Kind: Delete, Old: 7, Text: "redit line 7"},
			{Kind: Add, New: 7, Text: "changed"},
		}
		if !reflect.DeepEqual(changed, want) {
			t.Errorf("changed lines = %+v, want %+v", changed, want)
		}
	})

	t.Run("CRLF content keeps its carriage return", func(t *testing.T) {
		var texts []string
		for _, l := range files["crlf.txt"].Hunks[0].Lines {
			texts = append(texts, l.Text)
		}
		if want := []string{"a\r", "b\r", "B\r"}; !reflect.DeepEqual(texts, want) {
			t.Errorf("line texts = %q, want %q", texts, want)
		}
	})

	t.Run("missing final newline is recorded on both sides", func(t *testing.T) {
		want := []Line{
			{Kind: Context, Old: 1, New: 1, Text: "x"},
			{Kind: Delete, Old: 2, Text: "y", NoEOL: true},
			{Kind: Add, New: 2, Text: "z", NoEOL: true},
		}
		if got := files["noeol.txt"].Hunks[0].Lines; !reflect.DeepEqual(got, want) {
			t.Errorf("lines = %+v, want %+v", got, want)
		}
	})

	t.Run("totals match git numstat", func(t *testing.T) {
		// numstat reports a type change as 0/0, while the patch shows the
		// old content removed and the new content added, so leave it out.
		var wantAdded, wantDeleted int
		for _, row := range strings.Split(r.Git("diff", "--numstat", "-M", base, head), "\n") {
			cols := strings.SplitN(row, "\t", 3)
			if cols[0] == "-" { // binary
				continue
			}
			a, _ := strconv.Atoi(cols[0])
			del, _ := strconv.Atoi(cols[1])
			wantAdded, wantDeleted = wantAdded+a, wantDeleted+del
		}
		added, deleted := d.Stat()
		linkAdded, linkDeleted := files["link"].Stat()
		added, deleted = added-linkAdded, deleted-linkDeleted
		if added != wantAdded || deleted != wantDeleted {
			t.Errorf("Stat() = +%d -%d, numstat says +%d -%d", added, deleted, wantAdded, wantDeleted)
		}
	})
}

func TestComputeHonoursUserDiffConfig(t *testing.T) {
	r, base, head := fixture(t)
	plain, err := Compute(context.Background(), gitx.Repo{Dir: r.Dir}, base, head)
	if err != nil {
		t.Fatal(err)
	}
	// Settings a user might have that change how git prints a patch.
	for _, kv := range [][2]string{
		{"diff.noprefix", "true"},
		{"diff.mnemonicPrefix", "true"},
		{"diff.suppressBlankEmpty", "true"},
		{"diff.context", "10"},
		{"diff.renames", "false"},
		{"core.quotePath", "true"},
		{"color.ui", "always"},
	} {
		r.Git("config", kv[0], kv[1])
	}
	configured, err := Compute(context.Background(), gitx.Repo{Dir: r.Dir}, base, head)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plain, configured) {
		t.Error("diff changed when the user's git diff settings changed")
	}
}

func TestComputeEmptyAndErrors(t *testing.T) {
	r := gittest.New(t)
	r.Write("a.txt", "a\n")
	sha := r.Commit("only")
	repo := gitx.Repo{Dir: r.Dir}

	d, err := Compute(context.Background(), repo, sha, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Files) != 0 {
		t.Errorf("diff of a commit with itself has %d files, want 0", len(d.Files))
	}

	if _, err := Compute(context.Background(), repo, "no-such-ref", sha); err == nil {
		t.Error("unknown base: got nil error")
	}
	if _, err := Compute(context.Background(), repo, sha, "--output=x"); err == nil {
		t.Error("option-like head: got nil error")
	}
}

func TestParsePatch(t *testing.T) {
	t.Run("content that looks like a header stays inside its hunk", func(t *testing.T) {
		patch := "diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1,2 +1,2 @@\n-diff --git a/x b/x\n+@@ -1 +1 @@\n \n"
		sections, err := parsePatch([]byte(patch))
		if err != nil {
			t.Fatal(err)
		}
		if len(sections) != 1 || len(sections[0].hunks) != 1 {
			t.Fatalf("got %d sections, want 1 with 1 hunk: %+v", len(sections), sections)
		}
		want := []Line{
			{Kind: Delete, Old: 1, Text: "diff --git a/x b/x"},
			{Kind: Add, New: 1, Text: "@@ -1 +1 @@"},
			{Kind: Context, Old: 2, New: 2, Text: ""},
		}
		if got := sections[0].hunks[0].Lines; !reflect.DeepEqual(got, want) {
			t.Errorf("lines = %+v, want %+v", got, want)
		}
	})

	t.Run("blank context line without its leading space", func(t *testing.T) {
		patch := "diff --git a/f b/f\n@@ -1,3 +1,3 @@ func f()\n a\n\n-b\n+c\n"
		sections, err := parsePatch([]byte(patch))
		if err != nil {
			t.Fatal(err)
		}
		h := sections[0].hunks[0]
		if h.Header != "func f()" {
			t.Errorf("Header = %q, want %q", h.Header, "func f()")
		}
		if len(h.Lines) != 4 || h.Lines[1].Kind != Context || h.Lines[1].Old != 2 || h.Lines[1].New != 2 {
			t.Errorf("lines = %+v, want a blank context line at 2/2", h.Lines)
		}
	})

	t.Run("omitted count means one line", func(t *testing.T) {
		sections, err := parsePatch([]byte("diff --git a/f b/f\n@@ -1 +1 @@\n-a\n+b\n"))
		if err != nil {
			t.Fatal(err)
		}
		if h := sections[0].hunks[0]; h.OldLines != 1 || h.NewLines != 1 || len(h.Lines) != 2 {
			t.Errorf("hunk = %+v, want 1 old and 1 new line", h)
		}
	})

	for name, patch := range map[string]string{
		"hunk before any file": "@@ -1 +1 @@\n-a\n+b\n",
		"truncated hunk":       "diff --git a/f b/f\n@@ -1,2 +1,2 @@\n-a\n",
		"bad hunk header":      "diff --git a/f b/f\n@@ nonsense @@\n",
		"bad line marker":      "diff --git a/f b/f\n@@ -1 +1 @@\n*a\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parsePatch([]byte(patch)); err == nil {
				t.Error("got nil error")
			}
		})
	}
}

func TestParseRawErrors(t *testing.T) {
	for name, raw := range map[string]string{
		"no colon":        "100644 100644 a b M\x00f\x00",
		"missing path":    ":100644 100644 a b M\x00",
		"unknown status":  ":100644 100644 a b X\x00f\x00",
		"rename one path": ":100644 100644 a b R100\x00f\x00",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseRaw([]byte(raw)); err == nil {
				t.Error("got nil error")
			}
		})
	}
}
