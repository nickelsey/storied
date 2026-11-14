package anchor

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"storied/internal/diff"
)

// MinIDLength is the shortest block id prefix an anchor may use, and the
// shortest one storied prints.
const MinIDLength = 7

// Block is the smallest piece of a diff a story can point at: one unbroken
// run of added and deleted lines, or a whole file that changed without line
// changes. It is finer than a git hunk, which may hold several runs
// separated by context.
type Block struct {
	// ID is a hash of the file's path and the block's changed lines. It
	// does not depend on line numbers or on the commits, so it survives a
	// rebase and edits elsewhere in the file, and changes only when the
	// block's own lines do.
	ID          string
	File        int   // index into Diff.Files
	Hunk        int   // index into File.Hunks, or -1 for a whole-file block
	First, Last int   // line indexes within the hunk, inclusive
	Units       []int // indexes into Resolution.Units, ascending
}

// blocks cuts the units into blocks. byFile lists each file's unit indexes
// in diff order.
func blocks(d *diff.Diff, units []Unit, byFile [][]int) (out []Block, blockOf []int) {
	blockOf = make([]int, len(units))
	seen := map[string]int{}
	for fi, f := range d.Files {
		var cur *Block
		flush := func() {
			if cur == nil {
				return
			}
			h := sha256.New()
			fmt.Fprintf(h, "%s\x00", f.Path())
			if cur.Hunk < 0 {
				fmt.Fprintf(h, "file\x00%s\x00%s\x00%s", f.Status, f.OldMode, f.NewMode)
			} else {
				for _, l := range f.Hunks[cur.Hunk].Lines[cur.First : cur.Last+1] {
					sign := "+"
					if l.Kind == diff.Delete {
						sign = "-"
					}
					fmt.Fprintf(h, "%s%s\n", sign, l.Text)
				}
			}
			id := hex.EncodeToString(h.Sum(nil))
			// The same change made twice in one file hashes the same;
			// tell the copies apart by their order.
			n := seen[id]
			seen[id]++
			if n > 0 {
				fmt.Fprintf(h, "\x00#%d", n)
				id = hex.EncodeToString(h.Sum(nil))
			}
			cur.ID = id
			for _, u := range cur.Units {
				blockOf[u] = len(out)
			}
			out = append(out, *cur)
			cur = nil
		}
		for _, u := range byFile[fi] {
			unit := units[u]
			if cur != nil && (unit.Hunk != cur.Hunk || unit.Line != cur.Last+1) {
				flush()
			}
			if cur == nil {
				cur = &Block{File: fi, Hunk: unit.Hunk, First: unit.Line}
			}
			cur.Last = unit.Line
			cur.Units = append(cur.Units, u)
		}
		flush()
	}
	return out, blockOf
}

// shortIDs returns, for each block, the shortest prefix of its id that no
// other block shares, and never fewer than MinIDLength characters.
func shortIDs(bs []Block) []string {
	out := make([]string, len(bs))
	for n := MinIDLength; ; n++ {
		count := map[string]int{}
		for i, b := range bs {
			if out[i] == "" {
				count[b.ID[:n]]++
			}
		}
		done := true
		for i, b := range bs {
			if out[i] != "" {
				continue
			}
			if count[b.ID[:n]] == 1 || n == len(b.ID) {
				out[i] = b.ID[:n]
			} else {
				done = false
			}
		}
		if done {
			return out
		}
	}
}

// findBlocks returns the blocks whose id starts with prefix.
func (r *Resolution) findBlocks(prefix string) []int {
	var out []int
	for i, b := range r.Blocks {
		if strings.HasPrefix(b.ID, prefix) {
			out = append(out, i)
		}
	}
	return out
}

// Stat counts the added and deleted lines of a block.
func (r *Resolution) Stat(b Block) (added, deleted int) {
	if b.Hunk < 0 {
		return 0, 0
	}
	for _, l := range r.Diff.Files[b.File].Hunks[b.Hunk].Lines[b.First : b.Last+1] {
		if l.Kind == diff.Add {
			added++
		} else {
			deleted++
		}
	}
	return added, deleted
}
