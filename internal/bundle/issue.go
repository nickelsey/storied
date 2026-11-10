package bundle

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Issue is one problem found in a bundle file.
type Issue struct {
	File string // name relative to the bundle directory
	Line int    // 1-based, or 0 when the problem has no single line
	Msg  string
}

func (i Issue) String() string {
	if i.Line > 0 {
		return fmt.Sprintf("%s:%d: %s", i.File, i.Line, i.Msg)
	}
	return fmt.Sprintf("%s: %s", i.File, i.Msg)
}

// Issues is a list of problems. It is the error type Load returns when a
// bundle is invalid.
type Issues []Issue

func (is Issues) Error() string {
	lines := make([]string, len(is))
	for i, issue := range is {
		lines[i] = issue.String()
	}
	return strings.Join(lines, "\n")
}

func (is Issues) sort() {
	sort.SliceStable(is, func(a, b int) bool {
		if is[a].File != is[b].File {
			return is[a].File < is[b].File
		}
		return is[a].Line < is[b].Line
	})
}

// collector gathers issues for one file.
type collector struct {
	file string
	list Issues
}

func (c *collector) addf(line int, format string, args ...any) {
	c.list = append(c.list, Issue{File: c.file, Line: line, Msg: fmt.Sprintf(format, args...)})
}

var (
	yamlLineRE = regexp.MustCompile(`line (\d+): (.*)$`)
	goTypeRE   = regexp.MustCompile(` in type \S+`)
)

// addYAML records a YAML decoding error, one issue per reported problem.
// offset is added to the line numbers the YAML library reports.
func (c *collector) addYAML(err error, offset int) {
	for _, msg := range strings.Split(err.Error(), "\n") {
		msg = goTypeRE.ReplaceAllString(strings.TrimSpace(msg), "")
		if m := yamlLineRE.FindStringSubmatch(msg); m != nil {
			n, _ := strconv.Atoi(m[1])
			c.addf(n+offset, "%s", m[2])
		} else if msg != "" && msg != "yaml: unmarshal errors:" {
			c.addf(0, "%s", strings.TrimPrefix(msg, "yaml: "))
		}
	}
}
