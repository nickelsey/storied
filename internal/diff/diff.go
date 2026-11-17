package diff

import (
	"context"
	"fmt"

	"github.com/nickelsey/storied/internal/gitx"
)

// Status says how a file changed between base and head.
type Status int

const (
	Modified Status = iota
	Added
	Deleted
	Renamed
	Copied
	TypeChanged // for example a symlink replaced by a regular file
)

func (s Status) String() string {
	switch s {
	case Modified:
		return "modified"
	case Added:
		return "added"
	case Deleted:
		return "deleted"
	case Renamed:
		return "renamed"
	case Copied:
		return "copied"
	case TypeChanged:
		return "type-changed"
	}
	return fmt.Sprintf("Status(%d)", int(s))
}

// Kind is the role of a line within a hunk.
type Kind int

const (
	Context Kind = iota
	Add
	Delete
)

// Diff is the set of changes between two commits.
type Diff struct {
	Base, Head string // full commit SHAs
	Files      []File
}

// File is one changed path. Paths are repo-relative with forward slashes.
type File struct {
	OldPath string // empty for added files
	NewPath string // empty for deleted files
	OldMode string // octal, empty for added files
	NewMode string // octal, empty for deleted files
	Status  Status
	Binary  bool
	Hunks   []Hunk
}

// Path returns the file's path at head, or at base if it was deleted.
func (f File) Path() string {
	if f.NewPath != "" {
		return f.NewPath
	}
	return f.OldPath
}

// Stat counts the added and deleted lines in the file.
func (f File) Stat() (added, deleted int) {
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			switch l.Kind {
			case Add:
				added++
			case Delete:
				deleted++
			}
		}
	}
	return added, deleted
}

// Stat counts the added and deleted lines across all files.
func (d *Diff) Stat() (added, deleted int) {
	for _, f := range d.Files {
		a, del := f.Stat()
		added += a
		deleted += del
	}
	return added, deleted
}

// Hunk is one contiguous block of changes with its surrounding context.
type Hunk struct {
	OldStart, OldLines int
	NewStart, NewLines int
	Header             string // text after the second @@, usually the enclosing function
	Lines              []Line
}

// Line is one line of a hunk. Old is 0 for added lines and New is 0 for
// deleted lines. Text excludes the leading marker and the line terminator,
// but keeps a trailing carriage return if the file has CRLF endings.
type Line struct {
	Kind     Kind
	Old, New int
	Text     string
	NoEOL    bool // the file has no newline after this line
}

// contextLines is the number of unchanged lines shown around each change.
const contextLines = "3"

// Compute returns the diff from base to head. Callers that want what a PR
// shows should pass the merge base of the target branch and head as base.
func Compute(ctx context.Context, repo gitx.Repo, base, head string) (*Diff, error) {
	baseSHA, err := repo.RevParse(ctx, base)
	if err != nil {
		return nil, fmt.Errorf("resolve base: %w", err)
	}
	headSHA, err := repo.RevParse(ctx, head)
	if err != nil {
		return nil, fmt.Errorf("resolve head: %w", err)
	}

	// Paths and statuses come from the NUL-separated raw output, which never
	// quotes or abbreviates. The patch is read only for hunks; its file
	// sections come in the same order as the raw entries.
	rawOut, err := repo.Run(ctx, "diff", "--raw", "-z", "--no-abbrev", "--no-relative", "-M",
		baseSHA, headSHA, "--")
	if err != nil {
		return nil, err
	}
	entries, err := parseRaw(rawOut)
	if err != nil {
		return nil, err
	}
	patchOut, err := repo.Run(ctx, "diff", "--patch", "--no-color", "--no-ext-diff", "--no-textconv",
		"--no-relative", "--submodule=short", "--inter-hunk-context=0", "-M", "-U"+contextLines,
		baseSHA, headSHA, "--")
	if err != nil {
		return nil, err
	}
	sections, err := parsePatch(patchOut)
	if err != nil {
		return nil, err
	}

	d := &Diff{Base: baseSHA, Head: headSHA, Files: make([]File, 0, len(entries))}
	next := 0
	for _, e := range entries {
		// git writes a type change as a deletion followed by an addition.
		n := 1
		if e.status == TypeChanged {
			n = 2
		}
		if next+n > len(sections) {
			return nil, fmt.Errorf("git diff: %d raw entries but only %d patch sections", len(entries), len(sections))
		}
		f := File{OldPath: e.oldPath, NewPath: e.newPath, OldMode: e.oldMode, NewMode: e.newMode, Status: e.status}
		for _, s := range sections[next : next+n] {
			f.Binary = f.Binary || s.binary
			f.Hunks = append(f.Hunks, s.hunks...)
		}
		next += n
		d.Files = append(d.Files, f)
	}
	if next != len(sections) {
		return nil, fmt.Errorf("git diff: %d patch sections left over after %d raw entries", len(sections)-next, len(entries))
	}
	return d, nil
}
