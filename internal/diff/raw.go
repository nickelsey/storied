package diff

import (
	"bytes"
	"fmt"
	"strings"
)

type rawEntry struct {
	oldMode, newMode string
	status           Status
	oldPath, newPath string
}

// parseRaw reads the output of git diff --raw -z. Each entry is
// ":<oldmode> <newmode> <oldsha> <newsha> <status>\0<path>\0", with a second
// path for renames and copies.
func parseRaw(data []byte) ([]rawEntry, error) {
	fields := bytes.Split(data, []byte{0})
	var entries []rawEntry
	for i := 0; i < len(fields) && len(fields[i]) > 0; {
		meta := string(fields[i])
		parts := strings.Fields(strings.TrimPrefix(meta, ":"))
		if !strings.HasPrefix(meta, ":") || len(parts) != 5 {
			return nil, fmt.Errorf("git diff --raw: unexpected entry %q", meta)
		}
		e := rawEntry{oldMode: mode(parts[0]), newMode: mode(parts[1])}
		paths := 1
		switch parts[4][0] {
		case 'M':
			e.status = Modified
		case 'A':
			e.status = Added
		case 'D':
			e.status = Deleted
		case 'T':
			e.status = TypeChanged
		case 'R':
			e.status, paths = Renamed, 2
		case 'C':
			e.status, paths = Copied, 2
		default:
			return nil, fmt.Errorf("git diff --raw: unsupported status %q in %q", parts[4], meta)
		}
		// The split leaves an empty field after the final NUL, so a missing
		// path shows up as an empty one.
		for k := 1; k <= paths; k++ {
			if i+k >= len(fields) || len(fields[i+k]) == 0 {
				return nil, fmt.Errorf("git diff --raw: entry %q is missing its path", meta)
			}
		}
		first := string(fields[i+1])
		switch {
		case paths == 2:
			e.oldPath, e.newPath = first, string(fields[i+2])
		case e.status == Added:
			e.newPath = first
		case e.status == Deleted:
			e.oldPath = first
		default:
			e.oldPath, e.newPath = first, first
		}
		entries = append(entries, e)
		i += 1 + paths
	}
	return entries, nil
}

// mode returns "" for the all-zero mode git uses for a missing side.
func mode(m string) string {
	if strings.Trim(m, "0") == "" {
		return ""
	}
	return m
}
