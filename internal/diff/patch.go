package diff

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
)

// patchSection is what the patch says about one "diff --git" section. Paths
// and modes are not read here; they come from the raw output.
type patchSection struct {
	binary bool
	hunks  []Hunk
}

var hunkHeaderRE = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@ ?(.*)$`)

type patchParser struct {
	data []byte
	pos  int
	line int // 1-based number of the line last returned by next
}

func (p *patchParser) more() bool { return p.pos < len(p.data) }

// peek returns the next line without its terminator, and the offset after it.
func (p *patchParser) peek() (line []byte, after int) {
	end := bytes.IndexByte(p.data[p.pos:], '\n')
	if end < 0 {
		return p.data[p.pos:], len(p.data)
	}
	return p.data[p.pos : p.pos+end], p.pos + end + 1
}

func (p *patchParser) next() []byte {
	line, after := p.peek()
	p.pos = after
	p.line++
	return line
}

func (p *patchParser) errorf(format string, args ...any) error {
	return fmt.Errorf("git diff patch line %d: %s", p.line, fmt.Sprintf(format, args...))
}

// parsePatch reads the output of git diff --patch into one section per
// "diff --git" header, in order.
func parsePatch(data []byte) ([]patchSection, error) {
	p := &patchParser{data: data}
	var sections []patchSection
	for p.more() {
		line, _ := p.peek()
		switch {
		case bytes.HasPrefix(line, []byte("diff --git ")):
			p.next()
			sections = append(sections, patchSection{})
		case len(sections) == 0:
			p.next()
			return nil, p.errorf("expected a diff --git header, got %q", line)
		case bytes.HasPrefix(line, []byte("@@ ")):
			h, err := p.hunk()
			if err != nil {
				return nil, err
			}
			cur := &sections[len(sections)-1]
			cur.hunks = append(cur.hunks, h)
		case bytes.HasPrefix(line, []byte("Binary files ")), bytes.Equal(line, []byte("GIT binary patch")):
			p.next()
			sections[len(sections)-1].binary = true
		default:
			// Extended header line: index, modes, similarity, rename, ---, +++.
			p.next()
		}
	}
	return sections, nil
}

// hunk parses a hunk header and exactly as many lines as it announces, so
// file content that looks like a header is never mistaken for one.
func (p *patchParser) hunk() (Hunk, error) {
	header := p.next()
	m := hunkHeaderRE.FindSubmatch(header)
	if m == nil {
		return Hunk{}, p.errorf("malformed hunk header %q", header)
	}
	h := Hunk{
		OldStart: atoi(m[1]), OldLines: count(m[2]),
		NewStart: atoi(m[3]), NewLines: count(m[4]),
		Header: string(m[5]),
	}
	oldNo, newNo := h.OldStart, h.NewStart
	oldLeft, newLeft := h.OldLines, h.NewLines
	for oldLeft > 0 || newLeft > 0 {
		if !p.more() {
			return Hunk{}, p.errorf("hunk ends early: %d old and %d new lines missing", oldLeft, newLeft)
		}
		line := p.next()
		// With diff.suppressBlankEmpty, git writes an empty context line
		// with no leading space.
		marker, text := byte(' '), ""
		if len(line) > 0 {
			marker, text = line[0], string(line[1:])
		}
		switch marker {
		case ' ':
			h.Lines = append(h.Lines, Line{Kind: Context, Old: oldNo, New: newNo, Text: text})
			oldNo, newNo = oldNo+1, newNo+1
			oldLeft, newLeft = oldLeft-1, newLeft-1
		case '-':
			h.Lines = append(h.Lines, Line{Kind: Delete, Old: oldNo, Text: text})
			oldNo, oldLeft = oldNo+1, oldLeft-1
		case '+':
			h.Lines = append(h.Lines, Line{Kind: Add, New: newNo, Text: text})
			newNo, newLeft = newNo+1, newLeft-1
		case '\\':
			if err := p.markNoEOL(&h); err != nil {
				return Hunk{}, err
			}
		default:
			return Hunk{}, p.errorf("unexpected line in hunk: %q", line)
		}
		if oldLeft < 0 || newLeft < 0 {
			return Hunk{}, p.errorf("hunk has more lines than its header announces")
		}
	}
	// A "\ No newline at end of file" marker may follow the last line.
	if line, _ := p.peek(); p.more() && len(line) > 0 && line[0] == '\\' {
		p.next()
		if err := p.markNoEOL(&h); err != nil {
			return Hunk{}, err
		}
	}
	return h, nil
}

func (p *patchParser) markNoEOL(h *Hunk) error {
	if len(h.Lines) == 0 {
		return p.errorf("no-newline marker before any line")
	}
	h.Lines[len(h.Lines)-1].NoEOL = true
	return nil
}

func atoi(b []byte) int {
	n, _ := strconv.Atoi(string(b)) // the regexp guarantees digits
	return n
}

// count reads a hunk line count, which defaults to 1 when omitted.
func count(b []byte) int {
	if b == nil {
		return 1
	}
	return atoi(b)
}
