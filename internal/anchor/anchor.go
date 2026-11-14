package anchor

import (
	"fmt"
	"sort"

	"github.com/bmatcuk/doublestar/v4"

	"storied/internal/bundle"
	"storied/internal/diff"
)

// State says how an anchor fared against the diff.
type State string

const (
	Exact       State = "exact"       // resolved to changed lines
	Moved       State = "moved"       // resolved, but to other lines than when the bundle was written
	Stale       State = "stale"       // resolves to no changed lines
	Unsupported State = "unsupported" // a kind this version cannot resolve yet
)

// Class says how well a changed unit is explained by the story.
type Class int

const (
	Uncovered Class = iota // no section claims it
	Skim                   // claimed only by skim globs
	Broad                  // claimed only by path or glob anchors
	Specific               // claimed by a lines, hunk, symbol or commit anchor
)

func (c Class) String() string {
	return [...]string{"uncovered", "skim", "broad", "specific"}[c]
}

// Unit is the unit of coverage: one added or deleted line, or a whole file
// when the file changed without line changes (binary, mode-only, pure rename,
// empty file).
type Unit struct {
	File int // index into Diff.Files
	Hunk int // index into File.Hunks, or -1 for a whole-file unit
	Line int // index into Hunk.Lines, or -1 for a whole-file unit
}

// WholeFile reports whether the unit stands for a file rather than a line.
func (u Unit) WholeFile() bool { return u.Hunk < 0 }

// Claim is one anchor and the units it resolved to.
type Claim struct {
	Anchor bundle.Anchor
	State  State
	Units  []int  // indexes into Resolution.Units, ascending
	Reason string // why the anchor is stale or unsupported
	// EveryFile is set on a glob that matched every file of a diff with more
	// than one file, which explains nothing in particular.
	EveryFile bool
}

// Section holds the claims of one story section.
type Section struct {
	Claims  []Claim
	Units   []int // every unit any of its anchors claims, ascending
	Primary []int // the units shown under this section, ascending
}

// Resolution is a story resolved against a diff.
type Resolution struct {
	Diff     *diff.Diff
	Units    []Unit
	Blocks   []Block   // the units grouped into change blocks, in diff order
	ShortID  []string  // per block: its id cut to the shortest unique prefix
	BlockOf  []int     // per unit: index of its block
	Sections []Section // parallel to the story's sections
	Owner    []int     // per unit: index of its primary section, or -1
	Class    []Class   // per unit
}

// rank orders anchors from least to most specific. A unit's primary section
// is the claimant with the highest rank, first in story order on a tie.
func rank(a bundle.Anchor) int {
	switch a.Kind {
	case bundle.AnchorLines:
		return 6
	case bundle.AnchorHunk:
		return 5
	case bundle.AnchorSymbol:
		return 4
	case bundle.AnchorCommit:
		return 3
	case bundle.AnchorPath:
		return 2
	case bundle.AnchorGlob:
		if a.Skim {
			return 0
		}
		return 1
	}
	return -1
}

func classOf(a bundle.Anchor) Class {
	switch r := rank(a); {
	case r >= 3:
		return Specific
	case r >= 1:
		return Broad
	}
	return Skim
}

// Resolve matches every anchor of the story against the diff. bundleBase
// and bundleHead are the commits the bundle was written for. Line numbers
// only mean something at the commit they were written against, so a lines
// anchor is stale when its side of the diff is a different commit.
func Resolve(d *diff.Diff, story *bundle.Story, bundleBase, bundleHead string) *Resolution {
	r := &Resolution{Diff: d, Sections: make([]Section, len(story.Sections))}
	current := sides{old: d.Base == bundleBase, new: d.Head == bundleHead}

	// byFile[i] lists the unit indexes of file i, in diff order.
	byFile := make([][]int, len(d.Files))
	for fi, f := range d.Files {
		for hi, h := range f.Hunks {
			for li, l := range h.Lines {
				if l.Kind != diff.Context {
					byFile[fi] = append(byFile[fi], len(r.Units))
					r.Units = append(r.Units, Unit{File: fi, Hunk: hi, Line: li})
				}
			}
		}
		if len(byFile[fi]) == 0 {
			byFile[fi] = append(byFile[fi], len(r.Units))
			r.Units = append(r.Units, Unit{File: fi, Hunk: -1, Line: -1})
		}
	}

	r.Blocks, r.BlockOf = blocks(d, r.Units, byFile)
	r.ShortID = shortIDs(r.Blocks)

	r.Owner = make([]int, len(r.Units))
	r.Class = make([]Class, len(r.Units))
	best := make([]int, len(r.Units))
	for i := range r.Owner {
		r.Owner[i], best[i] = -1, -1
	}

	for si, sec := range story.Sections {
		out := &r.Sections[si]
		seen := map[int]bool{}
		for _, a := range sec.Anchors {
			c := r.resolve(a, byFile, current)
			out.Claims = append(out.Claims, c)
			for _, u := range c.Units {
				if !seen[u] {
					seen[u] = true
					out.Units = append(out.Units, u)
				}
				if rk := rank(a); rk > best[u] {
					best[u], r.Owner[u] = rk, si
				}
				if cl := classOf(a); cl > r.Class[u] {
					r.Class[u] = cl
				}
			}
		}
		sort.Ints(out.Units)
	}
	for u, si := range r.Owner {
		if si >= 0 {
			r.Sections[si].Primary = append(r.Sections[si].Primary, u)
		}
	}
	return r
}

// sides says, for each side of the diff, whether it is the commit the bundle
// was written for.
type sides struct{ old, new bool }

func (r *Resolution) resolve(a bundle.Anchor, byFile [][]int, current sides) Claim {
	c := Claim{Anchor: a, State: Exact}
	stale := func(format string, args ...any) Claim {
		c.State, c.Reason = Stale, fmt.Sprintf(format, args...)
		return c
	}

	switch a.Kind {
	case bundle.AnchorPath:
		fi := r.file(a.Path)
		if fi < 0 {
			return stale("%s is not changed in this diff", a.Path)
		}
		c.Units = byFile[fi]

	case bundle.AnchorGlob:
		files := 0
		for fi, f := range r.Diff.Files {
			// The pattern was validated when the story was parsed.
			if ok, _ := doublestar.Match(a.Glob, f.Path()); ok {
				files++
				c.Units = append(c.Units, byFile[fi]...)
			}
		}
		if files == 0 {
			return stale("no changed file matches %s", a.Glob)
		}
		c.EveryFile = files == len(r.Diff.Files) && files > 1

	case bundle.AnchorLines:
		side, ok := "new", current.new
		if a.OldSide {
			side, ok = "old", current.old
		}
		if !ok {
			which := map[string]string{"new": "head", "old": "base"}[side]
			return stale("line numbers were written for another %s commit", which)
		}
		fi := r.sideFile(a.Path, a.OldSide)
		if fi < 0 {
			return stale("%s is not changed in this diff", a.Path)
		}
		for _, u := range byFile[fi] {
			unit := r.Units[u]
			if unit.WholeFile() {
				continue
			}
			l := r.Diff.Files[fi].Hunks[unit.Hunk].Lines[unit.Line]
			n := l.New
			if a.OldSide {
				n = l.Old
			}
			if n >= a.Lines.Start && n <= a.Lines.End {
				c.Units = append(c.Units, u)
			}
		}
		if len(c.Units) == 0 {
			return stale("no changed line on the %s side of %s within %d-%d", side, a.Path, a.Lines.Start, a.Lines.End)
		}

	case bundle.AnchorHunk:
		switch found := r.findBlocks(a.Hunk); len(found) {
		case 0:
			return stale("no change block has the id %s; the code it pointed at was edited or is no longer part of the diff", a.Hunk)
		case 1:
			c.Units = r.Blocks[found[0]].Units
		default:
			return stale("%d change blocks have an id starting with %s; write more of the id", len(found), a.Hunk)
		}

	default:
		c.State = Unsupported
		c.Reason = fmt.Sprintf("%s anchors are not resolved by this version yet", a.Kind)
	}
	return c
}

// file finds a file by its path at head, or at base if it was deleted.
func (r *Resolution) file(path string) int {
	for i, f := range r.Diff.Files {
		if f.Path() == path {
			return i
		}
	}
	return -1
}

// sideFile finds a file by its path on one side of the diff.
func (r *Resolution) sideFile(path string, old bool) int {
	for i, f := range r.Diff.Files {
		if (old && f.OldPath == path) || (!old && f.NewPath == path) {
			return i
		}
	}
	return -1
}

// Coverage counts changed units by how well the story explains them.
type Coverage struct {
	Total     int `json:"total"`
	Specific  int `json:"specific"`
	Broad     int `json:"broad"`
	Skim      int `json:"skim"`
	Uncovered int `json:"uncovered"`
}

// Add counts one unit of the given class.
func (c *Coverage) Add(cl Class) {
	c.Total++
	switch cl {
	case Specific:
		c.Specific++
	case Broad:
		c.Broad++
	case Skim:
		c.Skim++
	default:
		c.Uncovered++
	}
}

// Coverage returns the coverage of the whole diff.
func (r *Resolution) Coverage() Coverage {
	var c Coverage
	for _, cl := range r.Class {
		c.Add(cl)
	}
	return c
}

// FileCoverage returns the coverage of each file, parallel to Diff.Files.
func (r *Resolution) FileCoverage() []Coverage {
	out := make([]Coverage, len(r.Diff.Files))
	for u, cl := range r.Class {
		out[r.Units[u].File].Add(cl)
	}
	return out
}
