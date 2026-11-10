package bundle

import (
	"reflect"
	"strings"
	"testing"
)

const frontmatter = "---\ntitle: T\n---\n"

func TestParseStorySections(t *testing.T) {
	src := frontmatter + `
## One {#one author=human}
First.

## Two {#two author=agent risk=high}
::anchor{path="a.go"}
Second.
`
	s, issues := ParseStory([]byte(src))
	if len(issues) != 0 {
		t.Fatalf("issues: %v", issues)
	}
	if s.Title != "T" || len(s.Sections) != 2 {
		t.Fatalf("story = %+v", s)
	}
	two := s.Sections[1]
	if two.ID != "two" || two.Title != "Two" || two.Author != "agent" || two.Risk != "high" || two.Line != 8 {
		t.Errorf("section two = %+v", two)
	}
	if two.Body != "Second." {
		t.Errorf("Body = %q, want the prose without the directive line", two.Body)
	}
	if want := []Anchor{{Kind: AnchorPath, Path: "a.go", Line: 9}}; !reflect.DeepEqual(two.Anchors, want) {
		t.Errorf("Anchors = %+v, want %+v", two.Anchors, want)
	}
}

func TestParseStoryNormalisesLineEndings(t *testing.T) {
	src := frontmatter + "## One {#one author=human}\n::anchor{path=\"a.go\"}\nText.\n"
	unix, issues := ParseStory([]byte(src))
	if len(issues) != 0 {
		t.Fatalf("issues: %v", issues)
	}
	windows, issues := ParseStory([]byte("\xef\xbb\xbf" + strings.ReplaceAll(src, "\n", "\r\n")))
	if len(issues) != 0 {
		t.Fatalf("issues with BOM and CRLF: %v", issues)
	}
	if !reflect.DeepEqual(unix, windows) {
		t.Errorf("CRLF story parsed differently:\n%+v\n%+v", unix, windows)
	}
	if unix.Sections[0].Hash() != windows.Sections[0].Hash() {
		t.Error("section hash depends on line endings")
	}
}

func TestSectionHash(t *testing.T) {
	parse := func(body string) Section {
		t.Helper()
		s, issues := ParseStory([]byte(frontmatter + "## One {#one author=agent}\n" + body))
		if len(issues) != 0 {
			t.Fatalf("issues: %v", issues)
		}
		return s.Sections[0]
	}
	base := parse("::anchor{path=\"a.go\"}\nText.\n")
	if got := parse("::anchor{path=\"a.go\"}\nText.\n\n\n"); got.Hash() != base.Hash() {
		t.Error("trailing blank lines changed the hash")
	}
	if got := parse("::anchor{path=\"a.go\"}\nText!\n"); got.Hash() == base.Hash() {
		t.Error("edited prose did not change the hash")
	}
	if got := parse("::anchor{path=\"b.go\"}\nText.\n"); got.Hash() == base.Hash() {
		t.Error("edited anchor did not change the hash")
	}
}

func TestParseStoryDefaultRisk(t *testing.T) {
	src := "---\ntitle: T\nrisk: medium\n---\n## A {#a author=human}\n## B {#b author=human risk=low}\n"
	s, issues := ParseStory([]byte(src))
	if len(issues) != 0 {
		t.Fatalf("issues: %v", issues)
	}
	if s.Sections[0].Risk != "medium" || s.Sections[1].Risk != "low" {
		t.Errorf("risks = %q, %q; want medium (inherited), low", s.Sections[0].Risk, s.Sections[1].Risk)
	}
}

func TestParseStoryIssues(t *testing.T) {
	section := frontmatter + "## One {#one author=human}\n" // directive lines land on line 5
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"no frontmatter", "## One {#one author=human}\n", "story.md:1: story must start with frontmatter"},
		{"unclosed frontmatter", "---\ntitle: T\n", "story.md:1: frontmatter is not closed"},
		{"no title", "---\nrisk: low\n---\n## One {#one author=human}\n", "story.md:1: frontmatter needs a title"},
		{"empty frontmatter", "---\n---\n## One {#one author=human}\n", "story.md:1: frontmatter needs a title"},
		{"bad frontmatter risk", "---\ntitle: T\nrisk: scary\n---\n## One {#one author=human}\n", `frontmatter risk "scary"`},
		{"unknown frontmatter key", "---\ntitle: T\nowner: me\n---\n## One {#one author=human}\n", "story.md:3: field owner not found"},
		{"no sections", frontmatter + "Just prose.\n", "story.md: story has no sections"},
		{"heading without attributes", frontmatter + "## One\n", "story.md:4: section heading needs an attribute block"},
		{"heading without id", frontmatter + "## One {author=human}\n", "story.md:4: section heading needs an id"},
		{"heading without author", frontmatter + "## One {#one}\n", "story.md:4: section heading needs author="},
		{"heading without title", frontmatter + "## {#one author=human}\n", "story.md:4: section heading has no title"},
		{"bad id", frontmatter + "## One {#One_1 author=human}\n", `section id "One_1" must be`},
		{"bad author", frontmatter + "## One {#one author=bot}\n", `author "bot" must be human or agent`},
		{"bad risk", frontmatter + "## One {#one author=human risk=extreme}\n", `risk "extreme" must be`},
		{"unknown heading attribute", frontmatter + "## One {#one author=human owner=me}\n", `unknown section attribute "owner"`},
		{"repeated heading attribute", frontmatter + "## One {#one author=human author=agent}\n", "sets author twice"},
		{"duplicate id", section + "## Again {#one author=agent}\n", `story.md:5: section id "one" is already used on line 4`},
		{"directive before first section", frontmatter + "::anchor{path=\"a.go\"}\n## One {#one author=human}\n", "story.md:4: directive before the first section"},
		{"unknown directive", section + "::include{path=\"a.md\"}\n", "story.md:5: unknown directive ::include"},
		{"malformed directive", section + "::anchor path=a.go\n", "story.md:5: malformed directive"},
		{"unterminated quote", section + "::anchor{path=\"a.go}\n", "missing closing quote"},
		{"repeated directive attribute", section + "::anchor{path=\"a.go\" path=\"b.go\"}\n", "::anchor sets path twice"},
		{"artifact without id", section + "::artifact{}\n", "::artifact takes exactly one attribute"},
		{"artifact with extra attribute", section + "::artifact{id=\"x\" width=\"3\"}\n", "::artifact takes exactly one attribute"},
		{"anchor with nothing", section + "::anchor{}\n", "needs one of path, glob, content or commit"},
		{"anchor with empty value", section + "::anchor{path=\"\"}\n", "path needs a value"},
		{"symbol without path", section + "::anchor{symbol=\"f\"}\n", "a symbol anchor also needs path"},
		{"lines without path", section + "::anchor{lines=\"1-2\"}\n", "a lines anchor also needs path"},
		{"content without path", section + "::anchor{content=\"1111111111111111111111111111111111111111\"}\n", "a content anchor also needs path"},
		{"glob with path", section + "::anchor{glob=\"**\" path=\"a.go\"}\n", "path cannot be used on a glob anchor"},
		{"skim without glob", section + "::anchor{path=\"a.go\" skim}\n", "skim cannot be used on a path anchor"},
		{"skim with a value", section + "::anchor{glob=\"**\" skim=yes}\n", "skim is a flag"},
		{"side without lines", section + "::anchor{path=\"a.go\" side=\"old\"}\n", "side cannot be used on a path anchor"},
		{"commit with path", section + "::anchor{commit=\"2222222222222222222222222222222222222222\" path=\"a.go\"}\n", "path cannot be used on a commit anchor"},
		{"short patch-id", section + "::anchor{commit=\"abc123\"}\n", "must be a 40-character patch-id"},
		{"bad lines", section + "::anchor{path=\"a.go\" lines=\"ten\"}\n", `lines "ten" must look like`},
		{"backwards lines", section + "::anchor{path=\"a.go\" lines=\"9-3\"}\n", "not run backwards"},
		{"zero line", section + "::anchor{path=\"a.go\" lines=\"0-3\"}\n", "must start at 1"},
		{"bad side", section + "::anchor{path=\"a.go\" lines=\"1\" side=\"left\"}\n", `side "left" must be old or new`},
		{"absolute path", section + "::anchor{path=\"/etc/passwd\"}\n", "must be a clean repo-relative path"},
		{"parent path", section + "::anchor{path=\"../a.go\"}\n", "must be a clean repo-relative path"},
		{"backslash path", section + "::anchor{path=\"a\\\\b.go\"}\n", "must be a clean repo-relative path"},
		{"unclean path", section + "::anchor{path=\"a//b.go\"}\n", "must be a clean repo-relative path"},
		{"malformed glob", section + "::anchor{glob=\"src/[a-\"}\n", "is not a valid pattern"},
		{"parent glob", section + "::anchor{glob=\"../**\"}\n", "must be a repo-relative pattern"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, issues := ParseStory([]byte(tt.src))
			if !strings.Contains(issues.Error(), tt.want) {
				t.Errorf("issues:\n%v\nwant one containing %q", issues, tt.want)
			}
		})
	}
}

func TestParseStoryCodeFences(t *testing.T) {
	src := frontmatter + "## One {#one author=human}\n" +
		"~~~~\n## Fake {#fake author=human}\n::bogus{}\n~~~\nstill code\n~~~~\n" +
		"   ```go\n::anchor{path=\"x\"}\n   ```\n" +
		"::anchor{path=\"real.go\"}\n"
	s, issues := ParseStory([]byte(src))
	if len(issues) != 0 {
		t.Fatalf("issues: %v", issues)
	}
	if len(s.Sections) != 1 || len(s.Sections[0].Anchors) != 1 || s.Sections[0].Anchors[0].Path != "real.go" {
		t.Errorf("sections = %+v, want one section with only the real.go anchor", s.Sections)
	}
	if !strings.Contains(s.Sections[0].Body, "## Fake") {
		t.Errorf("Body lost the fenced code: %q", s.Sections[0].Body)
	}
}

func TestParseAttrs(t *testing.T) {
	tests := []struct {
		in   string
		want []attr
	}{
		{"", nil},
		{`#my-id author=agent`, []attr{{"#", "my-id", true}, {"author", "agent", true}}},
		{`path="a b/c.go"  skim`, []attr{{"path", "a b/c.go", true}, {"skim", "", false}}},
		{`symbol="say \"hi\" \\ bye"`, []attr{{"symbol", `say "hi" \ bye`, true}}},
		{`glob="src/{a,b}/**"`, []attr{{"glob", "src/{a,b}/**", true}}},
		{`path=`, []attr{{"path", "", true}}},
	}
	for _, tt := range tests {
		got, err := parseAttrs(tt.in)
		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parseAttrs(%q) = %+v, %v; want %+v", tt.in, got, err, tt.want)
		}
	}
	for _, in := range []string{`path="open`, `path="a"b`, `Path="a"`, `="a"`, `9x=1`} {
		if got, err := parseAttrs(in); err == nil {
			t.Errorf("parseAttrs(%q) = %+v, want an error", in, got)
		}
	}
}
