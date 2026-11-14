// Package lint checks a bundle against the diff it describes: it validates
// the bundle, resolves its anchors and reports coverage and warnings.
package lint

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"storied/internal/anchor"
	"storied/internal/bundle"
	"storied/internal/diff"
	"storied/internal/gitx"
)

// DefaultBundleDir is where a bundle lives, relative to the worktree root.
const DefaultBundleDir = ".storied"

// LargeSection is the number of changed lines above which a section is
// reported as too large to review as one step.
const LargeSection = 400

// Options selects what to check.
type Options struct {
	RepoDir   string // any directory inside the worktree; "" means the current directory
	BundleDir string // "" means .storied at the worktree root
	Base      string // revision; "" means the manifest's base_sha
	Head      string // revision; "" means HEAD
}

// Finding is one error or warning.
type Finding struct {
	File string `json:"file,omitempty"` // bundle file, when the finding has one
	Line int    `json:"line,omitempty"`
	Msg  string `json:"message"`
}

func (f Finding) String() string {
	switch {
	case f.File != "" && f.Line > 0:
		return fmt.Sprintf("%s:%d: %s", f.File, f.Line, f.Msg)
	case f.File != "":
		return fmt.Sprintf("%s: %s", f.File, f.Msg)
	}
	return f.Msg
}

// AnchorReport is one anchor and how it resolved.
type AnchorReport struct {
	Line   int    `json:"line"`
	Kind   string `json:"kind"`
	Spec   string `json:"spec"` // the anchor as a short readable string
	State  string `json:"state"`
	Lines  int    `json:"lines"` // changed lines it claims
	Reason string `json:"reason,omitempty"`
}

// SectionReport is one story section.
type SectionReport struct {
	ID      string         `json:"id"`
	Title   string         `json:"title"`
	Author  string         `json:"author"`
	Risk    string         `json:"risk,omitempty"`
	Line    int            `json:"line"`
	Claimed int            `json:"claimed"` // changed lines any of its anchors claims
	Shown   int            `json:"shown"`   // changed lines shown under it
	Anchors []AnchorReport `json:"anchors"`
}

// FileReport is the coverage of one changed file.
type FileReport struct {
	Path string `json:"path"`
	anchor.Coverage
}

// BlockReport is one change block: the smallest piece of the diff a story
// can point at, with the id to write in ::anchor{hunk="..."}.
type BlockReport struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Line      int    `json:"line,omitempty"` // first changed line: in the new file, or in the old one for a pure deletion
	Added     int    `json:"added"`
	Deleted   int    `json:"deleted"`
	Header    string `json:"header,omitempty"`    // the enclosing function, as git reports it
	Unclaimed int    `json:"unclaimed,omitempty"` // changed lines no section claims
	Total     int    `json:"total"`               // changed lines, or 1 for a whole-file change
}

var funcLineRE = regexp.MustCompile(`^[A-Za-z_$]`)

// DescribeBlock reports block i of a resolution.
func DescribeBlock(r *anchor.Resolution, i int) BlockReport {
	b := r.Blocks[i]
	f := r.Diff.Files[b.File]
	br := BlockReport{ID: r.ShortID[i], Path: f.Path(), Total: len(b.Units)}
	br.Added, br.Deleted = r.Stat(b)
	for _, u := range b.Units {
		if r.Owner[u] < 0 {
			br.Unclaimed++
		}
	}
	if b.Hunk >= 0 {
		h := f.Hunks[b.Hunk]
		// git's header names the function above the hunk's first line.
		// A block further down may sit in a later function, so look for
		// one in the hunk first, by git's own default rule: a line that
		// starts with a letter, an underscore or a dollar sign.
		br.Header = h.Header
		for i := b.First - 1; i >= 0; i-- {
			if l := h.Lines[i]; l.Kind != diff.Delete && funcLineRE.MatchString(l.Text) {
				br.Header = strings.TrimRight(l.Text, " \t\r")
				break
			}
		}
		for _, l := range h.Lines[b.First : b.Last+1] {
			if l.New > 0 {
				br.Line = l.New
				break
			}
		}
		if br.Line == 0 {
			br.Line = h.Lines[b.First].Old
		}
	}
	return br
}

// Report is the outcome of a lint run. When Errors is not empty the bundle
// is invalid and the other fields may be incomplete.
type Report struct {
	Title      string          `json:"title,omitempty"`
	Base       string          `json:"base,omitempty"`
	Head       string          `json:"head,omitempty"`
	BundleHead string          `json:"bundle_head,omitempty"` // set only when it differs from Head
	Coverage   anchor.Coverage `json:"coverage"`
	Sections   []SectionReport `json:"sections"`
	Files      []FileReport    `json:"files"`
	Unreviewed []BlockReport   `json:"unreviewed"` // blocks with lines no section claims
	Errors     []Finding       `json:"errors"`
	Warnings   []Finding       `json:"warnings"`
}

// Result is everything a lint run worked out, for commands that go on to
// use it. Diff and Resolution are nil when the report has errors.
type Result struct {
	Bundle     *bundle.Bundle
	Diff       *diff.Diff
	Resolution *anchor.Resolution
	Report     *Report
}

// Locate returns the repository that repoDir is in ("" means the current
// directory) and the bundle directory to use: bundleDir, or .storied at the
// worktree root when bundleDir is "".
func Locate(ctx context.Context, repoDir, bundleDir string) (gitx.Repo, string, error) {
	if repoDir == "" {
		repoDir = "."
	}
	repo := gitx.Repo{Dir: repoDir}
	root, err := repo.Root(ctx)
	if err != nil {
		return repo, "", err
	}
	if bundleDir == "" {
		bundleDir = filepath.Join(filepath.FromSlash(root), DefaultBundleDir)
	}
	return repo, bundleDir, nil
}

// Run checks the bundle. Problems with the bundle are reported in the
// Report; the error is for things that stop the check altogether, such as
// not being in a git repository.
func Run(ctx context.Context, opts Options) (*Result, error) {
	repo, bundleDir, err := Locate(ctx, opts.RepoDir, opts.BundleDir)
	if err != nil {
		return nil, err
	}

	rep := &Report{Sections: []SectionReport{}, Files: []FileReport{}, Unreviewed: []BlockReport{}, Errors: []Finding{}, Warnings: []Finding{}}
	res := &Result{Report: rep}

	b, err := bundle.Load(bundleDir)
	var issues bundle.Issues
	if errors.As(err, &issues) {
		for _, i := range issues {
			rep.Errors = append(rep.Errors, Finding{File: i.File, Line: i.Line, Msg: i.Msg})
		}
	} else if err != nil {
		return nil, err
	}
	res.Bundle = b
	if b.Story != nil {
		rep.Title = b.Story.Title
	}
	if len(rep.Errors) > 0 {
		return res, nil
	}

	base, head := opts.Base, opts.Head
	if base == "" {
		base = b.Manifest.BaseSHA
	}
	if head == "" {
		head = "HEAD"
	}
	d, err := diff.Compute(ctx, repo, base, head)
	if err != nil {
		var gitErr *gitx.Error
		if !errors.As(err, &gitErr) {
			return nil, err
		}
		// Most often the bundle was written against commits this clone
		// does not have.
		rep.Errors = append(rep.Errors, Finding{Msg: fmt.Sprintf("cannot diff %s..%s: %v", short(base), short(head), err)})
		return res, nil
	}
	rep.Base, rep.Head = d.Base, d.Head
	if d.Head != b.Manifest.HeadSHA {
		rep.BundleHead = b.Manifest.HeadSHA
	}

	r := anchor.Resolve(d, b.Story, b.Manifest.BaseSHA, b.Manifest.HeadSHA)
	res.Diff, res.Resolution = d, r
	rep.Coverage = r.Coverage()
	for i, c := range r.FileCoverage() {
		rep.Files = append(rep.Files, FileReport{Path: d.Files[i].Path(), Coverage: c})
	}
	for i, sec := range b.Story.Sections {
		sr := SectionReport{
			ID: sec.ID, Title: sec.Title, Author: sec.Author, Risk: sec.Risk, Line: sec.Line,
			Claimed: len(r.Sections[i].Units), Shown: len(r.Sections[i].Primary),
			Anchors: []AnchorReport{},
		}
		for _, c := range r.Sections[i].Claims {
			sr.Anchors = append(sr.Anchors, AnchorReport{
				Line: c.Anchor.Line, Kind: string(c.Anchor.Kind), Spec: Spec(c.Anchor),
				State: string(c.State), Lines: len(c.Units), Reason: c.Reason,
			})
		}
		rep.Sections = append(rep.Sections, sr)
	}
	for i := range r.Blocks {
		if br := DescribeBlock(r, i); br.Unclaimed > 0 {
			rep.Unreviewed = append(rep.Unreviewed, br)
		}
	}
	rep.Warnings = warnings(b, r, rep)
	return res, nil
}

func warnings(b *bundle.Bundle, r *anchor.Resolution, rep *Report) []Finding {
	out := []Finding{}
	story := func(line int, format string, args ...any) {
		out = append(out, Finding{File: bundle.StoryFile, Line: line, Msg: fmt.Sprintf(format, args...)})
	}

	if rep.BundleHead != "" {
		out = append(out, Finding{File: bundle.ManifestFile, Msg: fmt.Sprintf(
			"head_sha is %s but the diff ends at %s; the bundle may be out of date", short(rep.BundleHead), short(rep.Head))})
	}
	if len(r.Units) == 0 {
		out = append(out, Finding{Msg: "the diff is empty"})
	}

	used := map[string]bool{}
	for i, sec := range b.Story.Sections {
		for _, ref := range sec.Artifacts {
			used[ref.ID] = true
		}
		for _, c := range r.Sections[i].Claims {
			switch {
			case c.State == anchor.Stale:
				story(c.Anchor.Line, "stale anchor %s: %s", Spec(c.Anchor), c.Reason)
			case c.State == anchor.Unsupported:
				story(c.Anchor.Line, "anchor %s claims nothing: %s", Spec(c.Anchor), c.Reason)
			case c.EveryFile && !c.Anchor.Skim:
				story(c.Anchor.Line, "anchor %s matches every changed file, so it explains none of them in particular", Spec(c.Anchor))
			}
		}
		if len(sec.Anchors) > 0 && sec.Body == "" {
			story(sec.Line, "section %s has anchors but no prose", sec.ID)
		}
		if n := len(r.Sections[i].Units); n > LargeSection {
			story(sec.Line, "section %s claims %d changed lines; more than %d is hard to review as one step", sec.ID, n, LargeSection)
		}
	}
	for _, a := range b.Manifest.Artifacts {
		if !used[a.ID] {
			out = append(out, Finding{File: bundle.ManifestFile, Line: a.Line, Msg: fmt.Sprintf("artifact %s is not used by any section", a.ID)})
		}
	}
	if n := rep.Coverage.Uncovered; n > 0 {
		out = append(out, Finding{Msg: fmt.Sprintf("%d of %d changed lines are not covered by the story", n, rep.Coverage.Total)})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File > out[j].File // story.md, manifest.yaml, then general
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// Spec renders an anchor the way it is written in the story, without the
// directive name.
func Spec(a bundle.Anchor) string {
	var parts []string
	add := func(k, v string) { parts = append(parts, fmt.Sprintf("%s=%q", k, v)) }
	switch a.Kind {
	case bundle.AnchorCommit:
		add("commit", a.PatchID)
	case bundle.AnchorHunk:
		add("hunk", a.Hunk)
	case bundle.AnchorGlob:
		add("glob", a.Glob)
	}
	if a.Path != "" {
		add("path", a.Path)
	}
	switch a.Kind {
	case bundle.AnchorSymbol:
		add("symbol", a.Symbol)
	case bundle.AnchorLines:
		if a.Lines.Start == a.Lines.End {
			add("lines", fmt.Sprint(a.Lines.Start))
		} else {
			add("lines", fmt.Sprintf("%d-%d", a.Lines.Start, a.Lines.End))
		}
		if a.OldSide {
			add("side", "old")
		}
	}
	if a.Skim {
		parts = append(parts, "skim")
	}
	return strings.Join(parts, " ")
}

func short(sha string) string {
	if len(sha) == 40 {
		return sha[:9]
	}
	return sha
}
