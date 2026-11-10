package bundle

import (
	"fmt"
	"regexp"
	"strings"
)

// attr is one item of an attribute list such as
//
//	#repair-pass author=agent path="core/mesh/repair.py" skim
//
// "#id" is stored under the key "#". A bare word is a flag with no value.
type attr struct {
	Key      string
	Value    string
	HasValue bool
}

var attrKeyRE = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

func isSpace(b byte) bool { return b == ' ' || b == '\t' }

// parseAttrs splits an attribute list. Values may be bare (ending at the
// next space) or double-quoted, where \" and \\ are the only escapes.
func parseAttrs(s string) ([]attr, error) {
	var out []attr
	i := 0
	for {
		for i < len(s) && isSpace(s[i]) {
			i++
		}
		if i >= len(s) {
			return out, nil
		}
		start := i
		if s[i] == '#' {
			for i < len(s) && !isSpace(s[i]) {
				i++
			}
			out = append(out, attr{Key: "#", Value: s[start+1 : i], HasValue: true})
			continue
		}
		for i < len(s) && !isSpace(s[i]) && s[i] != '=' {
			i++
		}
		a := attr{Key: s[start:i]}
		if !attrKeyRE.MatchString(a.Key) {
			return nil, fmt.Errorf("invalid attribute name %q", a.Key)
		}
		if i < len(s) && s[i] == '=' {
			a.HasValue = true
			i++
			if i < len(s) && s[i] == '"' {
				value, after, err := readQuoted(s, i+1)
				if err != nil {
					return nil, fmt.Errorf("attribute %s: %w", a.Key, err)
				}
				a.Value, i = value, after
			} else {
				start = i
				for i < len(s) && !isSpace(s[i]) {
					i++
				}
				a.Value = s[start:i]
			}
		}
		out = append(out, a)
	}
}

// readQuoted reads a quoted value whose opening quote is just before i. It
// returns the value and the offset after the closing quote.
func readQuoted(s string, i int) (value string, after int, err error) {
	var b strings.Builder
	for i < len(s) {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\'):
			b.WriteByte(s[i+1])
			i += 2
		case c == '"':
			i++
			if i < len(s) && !isSpace(s[i]) {
				return "", 0, fmt.Errorf("expected a space after the closing quote")
			}
			return b.String(), i, nil
		default:
			b.WriteByte(c)
			i++
		}
	}
	return "", 0, fmt.Errorf("missing closing quote")
}
