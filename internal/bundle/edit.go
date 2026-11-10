package bundle

import (
	"fmt"
	"sort"
	"strings"
)

// UpdateManifest returns manifest source with top-level scalar fields set
// and the sections block replaced by one recording the given hashes. It
// edits the text line by line, so comments, artifacts and layout the author
// wrote are left as they are. The result is parsed again, and an edit that
// would break the manifest is refused.
func UpdateManifest(data []byte, fields [][2]string, sections map[string]string) ([]byte, error) {
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n"), "\n")

	find := func(key string) int {
		for i, l := range lines {
			if l == key+":" || strings.HasPrefix(l, key+": ") || strings.HasPrefix(l, key+":\t") {
				return i
			}
		}
		return -1
	}
	// blockEnd returns the line after a top-level key's value: the next
	// line that starts a new top-level entry.
	blockEnd := func(start int) int {
		end := start + 1
		for end < len(lines) && (lines[end] == "" || lines[end][0] == ' ' || lines[end][0] == '\t') {
			end++
		}
		for end > start+1 && strings.TrimSpace(lines[end-1]) == "" {
			end--
		}
		return end
	}
	// insertAt is where a missing key goes: before artifacts, else at the end.
	insertAt := func() int {
		if i := find("artifacts"); i >= 0 {
			return i
		}
		return len(lines)
	}
	replace := func(start, end int, with []string) {
		lines = append(lines[:start], append(append([]string{}, with...), lines[end:]...)...)
	}

	for _, f := range fields {
		line := []string{f[0] + ": " + f[1]}
		if i := find(f[0]); i >= 0 {
			replace(i, blockEnd(i), line)
		} else {
			at := insertAt()
			replace(at, at, line)
		}
	}

	var block []string
	if len(sections) > 0 {
		ids := make([]string, 0, len(sections))
		for id := range sections {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		block = append(block, "sections:")
		for _, id := range ids {
			block = append(block, "  "+id+":", "    hash: "+sections[id])
		}
	}
	if i := find("sections"); i >= 0 {
		replace(i, blockEnd(i), block)
	} else {
		at := insertAt()
		replace(at, at, block)
	}

	out := []byte(strings.Join(lines, "\n") + "\n")
	if _, issues := ParseManifest(out); len(issues) > 0 {
		return nil, fmt.Errorf("cannot update %s in place; fix it by hand first:\n%w", ManifestFile, issues)
	}
	return out, nil
}

// SectionSpans returns, for each section of a story that parsed without
// issues, the half-open range of source lines it occupies (0-based), given
// the total number of lines in the source.
func (s *Story) SectionSpans(totalLines int) [][2]int {
	out := make([][2]int, len(s.Sections))
	for i, sec := range s.Sections {
		end := totalLines
		if i+1 < len(s.Sections) {
			end = s.Sections[i+1].Line - 1
		}
		out[i] = [2]int{sec.Line - 1, end}
	}
	return out
}
