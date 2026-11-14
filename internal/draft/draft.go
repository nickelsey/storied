// Package draft writes the mechanical parts of a bundle: init scaffolds it,
// and Run groups the changes no section explains yet into proposed sections
// with anchors, leaving the prose to the author or their agent.
package draft

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"storied/internal/anchor"
	"storied/internal/bundle"
	"storied/internal/diff"
	"storied/internal/gitx"
	"storied/internal/lint"
)

// Options selects what to draft.
type Options struct {
	RepoDir   string // any directory inside the worktree; "" means the current directory
	BundleDir string // "" means .storied at the worktree root
	Base      string // branch or revision the PR targets; "" means the manifest's base_ref
	Head      string // revision; "" means HEAD
	DryRun    bool   // work out the result but write nothing
	// ByFile adds a section with file anchors for every change the story
	// does not explain, grouped by directory. Without it draft writes no
	// sections: it reports the unexplained change blocks for a writer to
	// attach to the chapters of the story.
	ByFile bool
}

// Result describes the story after a draft run.
type Result struct {
	Bundle   string        `json:"bundle"`
	Base     string        `json:"base"`
	Head     string        `json:"head"`
	Written  bool          `json:"written"` // false on a dry run or when nothing changed
	Sections []SectionInfo `json:"sections"`
	// Blocks lists every change block of the diff: the worklist for
	// whoever writes the story.
	Blocks []BlockInfo `json:"blocks"`
	// Attention lists things draft cannot fix and a person should look at.
	Attention []string `json:"attention"`
	// Unanchored lists changed files whose paths the story grammar cannot name.
	Unanchored []string `json:"unanchored"`
}

// SectionInfo is one section of the drafted story and the changes shown
// under it, which is what a writer needs to fill in its prose.
type SectionInfo struct {
	ID         string              `json:"id"`
	Title      string              `json:"title"`
	Author     string              `json:"author"`
	Status     string              `json:"status"` // "new" or "kept"
	Line       int                 `json:"line"`   // of the heading in story.md
	NeedsProse bool                `json:"needs_prose"`
	Anchors    []lint.AnchorReport `json:"anchors"`
	Files      []FileInfo          `json:"files"`
}

// BlockInfo is one change block, the section it is shown under, and its
// changed lines.
type BlockInfo struct {
	lint.BlockReport
	Section   string   `json:"section,omitempty"` // id of the section it is shown under
	Lines     []string `json:"lines"`             // changed lines, each starting with + or -
	Truncated bool     `json:"truncated,omitempty"`
}

// maxBlockLines is the most changed lines listed for one block.
const maxBlockLines = 60

// FileInfo is the part of one file shown under a section.
type FileInfo struct {
	Path    string     `json:"path"`
	Status  string     `json:"status"`
	Added   int        `json:"added"`
	Deleted int        `json:"deleted"`
	Hunks   []HunkInfo `json:"hunks"`
}

// HunkInfo locates a hunk with changes shown under a section.
type HunkInfo struct {
	OldStart int    `json:"old_start"`
	OldLines int    `json:"old_lines"`
	NewStart int    `json:"new_start"`
	NewLines int    `json:"new_lines"`
	Header   string `json:"header,omitempty"`
}

// ErrNoBundle is returned when the bundle directory has no manifest or story.
var ErrNoBundle = errors.New("no bundle found")

// Run brings the bundle up to date with the branch and reports every change
// block with the section that explains it, if any.
//
// It never invents chapters: which changes belong together is for the
// author to say. With ByFile it does the mechanical fallback and appends a
// section with file anchors and no prose for each group of unexplained
// files. Sections a person wrote or edited are then kept as they are, while
// an agent section whose text still matches the hash recorded when draft
// wrote it has not been touched since, so it is dropped and proposed afresh.
func Run(ctx context.Context, opts Options) (*Result, error) {
	repo, dir, err := lint.Locate(ctx, opts.RepoDir, opts.BundleDir)
	if err != nil {
		return nil, err
	}
	manifestPath := filepath.Join(dir, bundle.ManifestFile)
	storyPath := filepath.Join(dir, bundle.StoryFile)
	manifestSrc, err := os.ReadFile(manifestPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w in %s; run storied init first", ErrNoBundle, dir)
	} else if err != nil {
		return nil, err
	}
	storySrc, err := os.ReadFile(storyPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w in %s; run storied init first", ErrNoBundle, dir)
	} else if err != nil {
		return nil, err
	}
	m, issues := bundle.ParseManifest(manifestSrc)
	if len(issues) > 0 {
		return nil, issues
	}
	story, issues := bundle.ParseStory(storySrc)
	if len(issues) > 0 {
		return nil, issues
	}

	d, baseRef, err := computeDiff(ctx, repo, m, opts)
	if err != nil {
		return nil, err
	}
	res := &Result{Bundle: dir, Base: d.Base, Head: d.Head, Sections: []SectionInfo{}, Blocks: []BlockInfo{}, Attention: []string{}, Unanchored: []string{}}

	// Sort the sections into those to keep and those to write afresh.
	keep := make([]bool, len(story.Sections))
	kept := &bundle.Story{Title: story.Title, Risk: story.Risk}
	taken := map[string]bool{}
	hashes := map[string]string{}
	for i, sec := range story.Sections {
		rec, recorded := m.Sections[sec.ID]
		keep[i] = !(opts.ByFile && sec.Author == "agent" && recorded && rec.Hash == sec.Hash())
		if keep[i] {
			kept.Sections = append(kept.Sections, sec)
			taken[sec.ID] = true
			if recorded && sec.Author == "agent" {
				hashes[sec.ID] = rec.Hash
			}
		}
	}

	// Whatever the kept sections do not claim needs a new section.
	r := anchor.Resolve(d, kept, d.Base, d.Head)
	uncovered := make([]int, len(d.Files))
	for u, owner := range r.Owner {
		if owner < 0 {
			uncovered[r.Units[u].File]++
		}
	}
	var files []changed
	for i, n := range uncovered {
		if n > 0 {
			files = append(files, changed{path: d.Files[i].Path(), units: n})
		}
	}
	var proposals []proposal
	if opts.ByFile {
		var unanchored []string
		proposals, unanchored = propose(files, taken)
		res.Unanchored = append(res.Unanchored, unanchored...)
		if len(kept.Sections) == 0 && len(proposals) == 0 {
			return nil, errors.New("nothing to draft: the diff is empty and the story has no sections of its own")
		}
	}

	// Rebuild the story source: kept sections stay byte for byte, the
	// untouched agent sections go, the proposals are appended.
	lines := strings.Split(string(storySrc), "\n")
	var out []string
	next := 0
	for i, span := range story.SectionSpans(len(lines)) {
		if !keep[i] {
			out = append(out, lines[next:span[0]]...)
			next = span[1]
		}
	}
	out = append(out, lines[next:]...)
	newSrc := strings.TrimRight(strings.Join(out, "\n"), "\r\n \t") + "\n"
	isNew := map[string]bool{}
	for _, p := range proposals {
		newSrc += "\n" + p.text()
		isNew[p.id] = true
	}

	newStory, issues := bundle.ParseStory([]byte(newSrc))
	if len(issues) > 0 {
		return nil, fmt.Errorf("draft produced a story that does not parse (this is a bug in storied):\n%w", issues)
	}
	for _, sec := range newStory.Sections {
		if isNew[sec.ID] {
			hashes[sec.ID] = sec.Hash()
		}
	}
	fields := [][2]string{{"base_sha", d.Base}, {"head_sha", d.Head}}
	if opts.Base != "" {
		fields = append(fields, [2]string{"base_ref", yamlString(baseRef)})
	}
	newManifest, err := bundle.UpdateManifest(manifestSrc, fields, hashes)
	if err != nil {
		return nil, err
	}

	final := anchor.Resolve(d, newStory, d.Base, d.Head)
	for i, sec := range newStory.Sections {
		info := SectionInfo{
			ID: sec.ID, Title: sec.Title, Author: sec.Author, Status: "kept", Line: sec.Line,
			NeedsProse: sec.Body == "", Anchors: []lint.AnchorReport{}, Files: filesOf(final, final.Sections[i].Primary),
		}
		if isNew[sec.ID] {
			info.Status = "new"
		}
		for _, c := range final.Sections[i].Claims {
			info.Anchors = append(info.Anchors, lint.AnchorReport{
				Line: c.Anchor.Line, Kind: string(c.Anchor.Kind), Spec: lint.Spec(c.Anchor),
				State: string(c.State), Lines: len(c.Units), Reason: c.Reason,
			})
			if isNew[sec.ID] {
				continue
			}
			// A line range written for other commits now points at
			// whatever happens to be on those lines.
			moved := (c.Anchor.OldSide && d.Base != m.BaseSHA) || (!c.Anchor.OldSide && d.Head != m.HeadSHA)
			switch {
			case c.State == anchor.Stale:
				res.Attention = append(res.Attention, fmt.Sprintf("story.md:%d: section %s: stale anchor %s: %s",
					c.Anchor.Line, sec.ID, lint.Spec(c.Anchor), c.Reason))
			case c.Anchor.Kind == bundle.AnchorLines && moved:
				res.Attention = append(res.Attention, fmt.Sprintf("story.md:%d: section %s: anchor %s was written for other commits; check that the range still covers what the section explains",
					c.Anchor.Line, sec.ID, lint.Spec(c.Anchor)))
			}
		}
		res.Sections = append(res.Sections, info)
	}
	for i, b := range final.Blocks {
		info := BlockInfo{BlockReport: lint.DescribeBlock(final, i), Lines: []string{}}
		for _, u := range b.Units {
			if owner := final.Owner[u]; owner >= 0 {
				info.Section = newStory.Sections[owner].ID
				break
			}
		}
		if b.Hunk >= 0 {
			for _, l := range d.Files[b.File].Hunks[b.Hunk].Lines[b.First : b.Last+1] {
				if len(info.Lines) == maxBlockLines {
					info.Truncated = true
					break
				}
				sign := "+"
				if l.Kind == diff.Delete {
					sign = "-"
				}
				info.Lines = append(info.Lines, sign+strings.TrimSuffix(l.Text, "\r"))
			}
		}
		res.Blocks = append(res.Blocks, info)
	}
	for _, p := range res.Unanchored {
		res.Attention = append(res.Attention, fmt.Sprintf("%s: the path cannot be written in an anchor, so no section covers it", p))
	}

	if opts.DryRun {
		return res, nil
	}
	if newSrc != string(storySrc) {
		if err := os.WriteFile(storyPath, []byte(newSrc), 0o644); err != nil {
			return nil, err
		}
		res.Written = true
	}
	if string(newManifest) != string(manifestSrc) {
		if err := os.WriteFile(manifestPath, newManifest, 0o644); err != nil {
			return nil, err
		}
		res.Written = true
	}
	return res, nil
}

// computeDiff works out the commits to compare. The base is the merge base
// of the target branch and the head, recomputed on every run so that a
// rebase onto a newer target does not leave the bundle diffing against the
// old one. It falls back to the manifest's base_sha when no target branch
// is known or it does not exist in this clone.
func computeDiff(ctx context.Context, repo gitx.Repo, m *bundle.Manifest, opts Options) (*diff.Diff, string, error) {
	head := opts.Head
	if head == "" {
		head = "HEAD"
	}
	headSHA, err := repo.RevParse(ctx, head)
	if err != nil {
		return nil, "", fmt.Errorf("resolve head: %w", err)
	}
	baseRef, base := opts.Base, m.BaseSHA
	if baseRef == "" {
		baseRef = m.BaseRef
	}
	if baseRef != "" {
		mb, err := repo.MergeBase(ctx, baseRef, headSHA)
		switch {
		case err == nil:
			base = mb
		case opts.Base != "":
			return nil, "", fmt.Errorf("no common ancestor of %s and %s: %w", baseRef, head, err)
		}
	}
	d, err := diff.Compute(ctx, repo, base, headSHA)
	return d, baseRef, err
}

func filesOf(r *anchor.Resolution, units []int) []FileInfo {
	out := []FileInfo{}
	lastHunk := -1
	for _, u := range units {
		unit := r.Units[u]
		f := r.Diff.Files[unit.File]
		if len(out) == 0 || out[len(out)-1].Path != f.Path() {
			out = append(out, FileInfo{Path: f.Path(), Status: f.Status.String(), Hunks: []HunkInfo{}})
			lastHunk = -1
		}
		fi := &out[len(out)-1]
		if unit.WholeFile() {
			continue
		}
		h := f.Hunks[unit.Hunk]
		if h.Lines[unit.Line].Kind == diff.Add {
			fi.Added++
		} else {
			fi.Deleted++
		}
		if unit.Hunk != lastHunk {
			lastHunk = unit.Hunk
			fi.Hunks = append(fi.Hunks, HunkInfo{h.OldStart, h.OldLines, h.NewStart, h.NewLines, h.Header})
		}
	}
	return out
}
