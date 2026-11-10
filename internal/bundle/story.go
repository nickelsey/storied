package bundle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"go.yaml.in/yaml/v3"
)

// Story is the parsed story.md: frontmatter plus sections in reading order.
type Story struct {
	Title    string
	Risk     string // default risk for sections that set none; may be empty
	Preamble string `json:",omitempty"` // Markdown before the first section
	Sections []Section
}

// Section is the unit of review: a level-2 heading, the code it claims, the
// artifacts it embeds, and its prose.
type Section struct {
	ID        string
	Title     string
	Author    string        // "human" or "agent"
	Risk      string        `json:",omitempty"` // "low", "medium", "high", or empty
	Anchors   []Anchor      `json:",omitempty"`
	Artifacts []ArtifactRef `json:",omitempty"`
	Body      string        // Markdown prose with directive lines removed
	Line      int           // line of the heading in story.md

	raw string // the section's source text, for Hash
}

// Hash identifies the section's source text. storied draft records it for
// agent sections, so a later run can tell whether a human edited the section.
func (s Section) Hash() string {
	sum := sha256.Sum256([]byte(s.raw))
	return hex.EncodeToString(sum[:])
}

// ArtifactRef embeds a manifest artifact in a section.
type ArtifactRef struct {
	ID   string
	Line int
}

// AnchorKind says how an anchor finds the lines it claims.
type AnchorKind string

const (
	AnchorPath    AnchorKind = "path"    // every changed line in a file
	AnchorSymbol  AnchorKind = "symbol"  // changed lines inside a named symbol
	AnchorGlob    AnchorKind = "glob"    // every changed line in matching files
	AnchorLines   AnchorKind = "lines"   // an explicit line range
	AnchorContent AnchorKind = "content" // a hunk identified by its patch-id
	AnchorCommit  AnchorKind = "commit"  // a PR commit identified by its patch-id
)

// Anchor is one ::anchor directive. Only the fields its Kind uses are set.
type Anchor struct {
	Kind    AnchorKind
	Path    string    `json:",omitempty"` // path, symbol, lines, content
	Symbol  string    `json:",omitempty"`
	Glob    string    `json:",omitempty"`
	Skim    bool      `json:",omitempty"` // glob only: counts as covered, renders collapsed
	Lines   LineRange `json:",omitzero"`
	OldSide bool      `json:",omitempty"` // lines only: the range is in the base version
	PatchID string    `json:",omitempty"` // content, commit
	Line    int
}

// LineRange is an inclusive range of 1-based line numbers.
type LineRange struct {
	Start, End int
}

const (
	authorHuman = "human"
	authorAgent = "agent"
)

var (
	sectionIDRE   = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	headingRE     = regexp.MustCompile(`^##(?:[ \t]+(.*))?$`)
	headingAttrRE = regexp.MustCompile(`^(.*?)[ \t]*\{([^{}]*)\}$`)
	directiveRE   = regexp.MustCompile(`^::([a-z][a-z0-9-]*)\{(.*)\}[ \t]*$`)
	lineRangeRE   = regexp.MustCompile(`^(\d+)(?:-(\d+))?$`)
	patchIDRE     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	risks         = map[string]bool{"low": true, "medium": true, "high": true}
)

// ParseStory parses story.md. It always returns a Story, which is complete
// only when no issues are returned.
//
// Sections start at ATX level-2 headings ("## Title {#id author=human}").
// Directives are whole lines of the form ::name{attributes}. Neither is
// recognised inside a fenced code block.
func ParseStory(data []byte) (*Story, Issues) {
	c := &collector{file: StoryFile}
	text := strings.ReplaceAll(string(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))), "\r\n", "\n")
	lines := strings.Split(text, "\n")

	s := &Story{}
	start := parseFrontmatter(lines, s, c)

	var (
		cur       *Section
		raw, body []string
		preamble  []string
		fence     string // the opening marker while inside a fenced code block
	)
	flush := func() {
		if cur == nil {
			return
		}
		cur.Body = trimBlankLines(body)
		cur.raw = trimBlankLines(raw)
		s.Sections = append(s.Sections, *cur)
	}
	prose := func(line string) {
		if cur == nil {
			preamble = append(preamble, line)
			return
		}
		raw, body = append(raw, line), append(body, line)
	}

	for i := start; i < len(lines); i++ {
		line, n := lines[i], i+1
		switch {
		case fence != "":
			if closesFence(line, fence) {
				fence = ""
			}
			prose(line)
		case opensFence(line) != "":
			fence = opensFence(line)
			prose(line)
		case headingRE.MatchString(line):
			flush()
			cur = parseHeading(headingRE.FindStringSubmatch(line)[1], n, c)
			raw, body = []string{line}, nil
		case len(line) > 2 && line[:2] == "::" && line[2] >= 'a' && line[2] <= 'z':
			if cur == nil {
				c.addf(n, "directive before the first section")
				continue
			}
			raw = append(raw, line)
			parseDirective(line, n, cur, c)
		default:
			prose(line)
		}
	}
	flush()
	s.Preamble = trimBlankLines(preamble)

	if len(s.Sections) == 0 {
		c.addf(0, "story has no sections; a section starts with \"## Title {#id author=human}\"")
	}
	seen := map[string]int{}
	for i := range s.Sections {
		sec := &s.Sections[i]
		if sec.Risk == "" {
			sec.Risk = s.Risk
		}
		if sec.ID == "" {
			continue
		}
		if first, dup := seen[sec.ID]; dup {
			c.addf(sec.Line, "section id %q is already used on line %d", sec.ID, first)
		} else {
			seen[sec.ID] = sec.Line
		}
	}
	return s, c.list
}

// parseFrontmatter reads the YAML block at the top of the story and returns
// the index of the first line after it.
func parseFrontmatter(lines []string, s *Story, c *collector) int {
	if strings.TrimRight(lines[0], " \t") != "---" {
		c.addf(1, "story must start with frontmatter: a \"---\" line, then title, then \"---\"")
		return 0
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t") == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		c.addf(1, "frontmatter is not closed with a \"---\" line")
		return len(lines)
	}
	var fm struct {
		Title string `yaml:"title"`
		Risk  string `yaml:"risk"`
	}
	dec := yaml.NewDecoder(strings.NewReader(strings.Join(lines[1:end], "\n")))
	dec.KnownFields(true)
	// An empty block decodes to io.EOF, which the title check below reports.
	if err := dec.Decode(&fm); err != nil && !errors.Is(err, io.EOF) {
		c.addYAML(err, 1)
		return end + 1
	}
	s.Title, s.Risk = strings.TrimSpace(fm.Title), fm.Risk
	if s.Title == "" {
		c.addf(1, "frontmatter needs a title")
	}
	if s.Risk != "" && !risks[s.Risk] {
		c.addf(1, "frontmatter risk %q must be low, medium or high", s.Risk)
		s.Risk = ""
	}
	return end + 1
}

// parseHeading reads the text after "##": a title and an attribute block.
func parseHeading(rest string, n int, c *collector) *Section {
	sec := &Section{Line: n}
	rest = strings.TrimSpace(rest)
	m := headingAttrRE.FindStringSubmatch(rest)
	if m == nil {
		sec.Title = rest
		c.addf(n, "section heading needs an attribute block such as {#id author=human}")
		return sec
	}
	sec.Title = strings.TrimSpace(m[1])
	if sec.Title == "" {
		c.addf(n, "section heading has no title")
	}
	attrs, err := parseAttrs(m[2])
	if err != nil {
		c.addf(n, "section heading: %v", err)
		return sec
	}
	seen := map[string]bool{}
	for _, a := range attrs {
		if seen[a.Key] {
			c.addf(n, "section heading sets %s twice", attrName(a.Key))
			continue
		}
		seen[a.Key] = true
		switch a.Key {
		case "#":
			sec.ID = a.Value
			if !sectionIDRE.MatchString(a.Value) {
				c.addf(n, "section id %q must be lower-case letters, digits and single hyphens", a.Value)
			}
		case "author":
			sec.Author = a.Value
			if a.Value != authorHuman && a.Value != authorAgent {
				c.addf(n, "author %q must be human or agent", a.Value)
			}
		case "risk":
			if risks[a.Value] {
				sec.Risk = a.Value
			} else {
				c.addf(n, "risk %q must be low, medium or high", a.Value)
			}
		default:
			c.addf(n, "unknown section attribute %q", a.Key)
		}
	}
	if !seen["#"] {
		c.addf(n, "section heading needs an id, written #like-this")
	}
	if !seen["author"] {
		c.addf(n, "section heading needs author=human or author=agent")
	}
	return sec
}

func attrName(key string) string {
	if key == "#" {
		return "the id"
	}
	return key
}

func parseDirective(line string, n int, sec *Section, c *collector) {
	m := directiveRE.FindStringSubmatch(line)
	if m == nil {
		c.addf(n, "malformed directive; expected ::name{key=\"value\"}")
		return
	}
	name := m[1]
	if name != "anchor" && name != "artifact" {
		c.addf(n, "unknown directive ::%s; v1 has ::anchor and ::artifact", name)
		return
	}
	list, err := parseAttrs(m[2])
	if err != nil {
		c.addf(n, "::%s: %v", name, err)
		return
	}
	attrs := map[string]attr{}
	for _, a := range list {
		if _, dup := attrs[a.Key]; dup {
			c.addf(n, "::%s sets %s twice", name, a.Key)
			return
		}
		attrs[a.Key] = a
	}
	if name == "artifact" {
		id, ok := attrs["id"]
		if !ok || id.Value == "" || len(attrs) != 1 {
			c.addf(n, "::artifact takes exactly one attribute, id=\"...\"")
			return
		}
		sec.Artifacts = append(sec.Artifacts, ArtifactRef{ID: id.Value, Line: n})
		return
	}
	if a, ok := parseAnchor(attrs, n, c); ok {
		sec.Anchors = append(sec.Anchors, a)
	}
}

// anchorForms lists, for each kind, the attribute that selects it and the
// attributes it accepts. Order matters: the first form whose selector is
// present wins, so path comes last because most forms also use it.
var anchorForms = []struct {
	kind     AnchorKind
	selector string
	allowed  []string
	needPath bool
}{
	{AnchorCommit, "commit", []string{"commit"}, false},
	{AnchorContent, "content", []string{"content", "path"}, true},
	{AnchorGlob, "glob", []string{"glob", "skim"}, false},
	{AnchorSymbol, "symbol", []string{"symbol", "path"}, true},
	{AnchorLines, "lines", []string{"lines", "side", "path"}, true},
	{AnchorPath, "path", []string{"path"}, false},
}

func parseAnchor(attrs map[string]attr, n int, c *collector) (Anchor, bool) {
	a := Anchor{Line: n}
	ok := true
	fail := func(format string, args ...any) {
		c.addf(n, "::anchor: "+format, args...)
		ok = false
	}

	// Every attribute but the skim flag needs a non-empty value.
	for key, at := range attrs {
		switch {
		case key == "skim" && at.HasValue:
			fail("skim is a flag and takes no value")
		case key != "skim" && at.Value == "":
			fail("%s needs a value", key)
		}
	}
	if !ok {
		return a, false
	}

	form := -1
	for i, f := range anchorForms {
		if _, has := attrs[f.selector]; has {
			form = i
			break
		}
	}
	if form < 0 {
		fail("needs one of path, glob, content or commit")
		return a, false
	}
	f := anchorForms[form]
	a.Kind = f.kind
	for key := range attrs {
		if !contains(f.allowed, key) {
			fail("%s cannot be used on a %s anchor", key, f.kind)
		}
	}
	if _, has := attrs["path"]; f.needPath && !has {
		fail("a %s anchor also needs path", f.kind)
	}
	if !ok {
		return a, false
	}

	if p, has := attrs["path"]; has {
		a.Path = p.Value
		if !validRepoPath(p.Value) || path.Clean(p.Value) != p.Value {
			fail("path %q must be a clean repo-relative path with forward slashes", p.Value)
		}
	}
	switch f.kind {
	case AnchorSymbol:
		a.Symbol = attrs["symbol"].Value
	case AnchorGlob:
		a.Glob = attrs["glob"].Value
		_, a.Skim = attrs["skim"]
		if !validRepoPath(a.Glob) {
			fail("glob %q must be a repo-relative pattern with forward slashes", a.Glob)
		} else if !doublestar.ValidatePattern(a.Glob) {
			fail("glob %q is not a valid pattern", a.Glob)
		}
	case AnchorLines:
		m := lineRangeRE.FindStringSubmatch(attrs["lines"].Value)
		if m == nil {
			fail("lines %q must look like 12 or 12-40", attrs["lines"].Value)
			break
		}
		a.Lines.Start, _ = strconv.Atoi(m[1])
		a.Lines.End = a.Lines.Start
		if m[2] != "" {
			a.Lines.End, _ = strconv.Atoi(m[2])
		}
		if a.Lines.Start < 1 || a.Lines.End < a.Lines.Start {
			fail("lines %q must start at 1 or later and not run backwards", attrs["lines"].Value)
		}
		switch side := attrs["side"]; {
		case !side.HasValue, side.Value == "new":
		case side.Value == "old":
			a.OldSide = true
		default:
			fail("side %q must be old or new", side.Value)
		}
	case AnchorContent, AnchorCommit:
		a.PatchID = attrs[f.selector].Value
		if !patchIDRE.MatchString(a.PatchID) {
			fail("%s %q must be a 40-character patch-id", f.selector, a.PatchID)
		}
	}
	return a, ok
}

// validRepoPath reports whether p stays inside the repository and uses
// forward slashes.
func validRepoPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, `\`) {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." || part == "." {
			return false
		}
	}
	return true
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// opensFence returns the fence marker if line opens a fenced code block.
func opensFence(line string) string {
	t := strings.TrimLeft(line, " ")
	if len(line)-len(t) > 3 || len(t) < 3 || (t[0] != '`' && t[0] != '~') {
		return ""
	}
	n := 0
	for n < len(t) && t[n] == t[0] {
		n++
	}
	if n < 3 {
		return ""
	}
	return t[:n]
}

// closesFence reports whether line closes a block opened with fence: the
// same character, at least as many of them, and nothing else on the line.
func closesFence(line, fence string) bool {
	t := strings.TrimLeft(line, " ")
	if len(line)-len(t) > 3 {
		return false
	}
	t = strings.TrimRight(t, " \t")
	return len(t) >= len(fence) && strings.Trim(t, fence[:1]) == ""
}

func trimBlankLines(lines []string) string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}
