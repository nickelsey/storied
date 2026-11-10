package render

import (
	"html"
	"html/template"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"

	"storied/internal/diff"
)

// maxHighlight is the size of one side of a hunk above which it is shown
// without syntax highlighting.
const maxHighlight = 256 << 10

// highlighter turns hunk lines into HTML, with syntax highlighting where a
// lexer knows the file type.
type highlighter struct {
	lexers map[string]chroma.Lexer // by path; nil when no lexer matches
}

func newHighlighter() *highlighter {
	return &highlighter{lexers: map[string]chroma.Lexer{}}
}

func (hl *highlighter) lexer(path string) chroma.Lexer {
	l, ok := hl.lexers[path]
	if !ok {
		if l = lexers.Match(path); l != nil {
			l = chroma.Coalesce(l)
		}
		hl.lexers[path] = l
	}
	return l
}

// hunk returns the HTML of each line of h. The old and the new side are
// tokenised separately, each as one block, so that constructs spanning
// several lines (strings, comments) are coloured correctly within the hunk.
func (hl *highlighter) hunk(path string, h diff.Hunk) []template.HTML {
	out := make([]template.HTML, len(h.Lines))
	var oldIdx, newIdx []int
	for i, l := range h.Lines {
		if l.Kind != diff.Add {
			oldIdx = append(oldIdx, i)
		}
		if l.Kind != diff.Delete {
			newIdx = append(newIdx, i)
		}
	}
	lexer := hl.lexer(path)
	// The new side goes last so that context lines take its colouring.
	for _, idx := range [][]int{oldIdx, newIdx} {
		texts := make([]string, len(idx))
		for j, i := range idx {
			texts[j] = strings.TrimSuffix(h.Lines[i].Text, "\r")
		}
		for j, line := range highlightLines(lexer, texts) {
			out[idx[j]] = template.HTML(line)
		}
	}
	return out
}

// highlightLines returns one HTML string per input line.
func highlightLines(lexer chroma.Lexer, lines []string) []string {
	if out, ok := tokenise(lexer, lines); ok {
		return out
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = html.EscapeString(l)
	}
	return out
}

func tokenise(lexer chroma.Lexer, lines []string) (out []string, ok bool) {
	if lexer == nil || len(lines) == 0 {
		return nil, false
	}
	text := strings.Join(lines, "\n")
	if len(text) > maxHighlight {
		return nil, false
	}
	// A lexer reports some errors by panicking inside the iterator.
	defer func() {
		if recover() != nil {
			out, ok = nil, false
		}
	}()
	it, err := lexer.Tokenise(nil, text)
	if err != nil {
		return nil, false
	}

	var cur strings.Builder
	for _, tok := range it.Tokens() {
		class := tokenClass(tok.Type)
		for i, part := range strings.Split(tok.Value, "\n") {
			if i > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
			if part == "" {
				continue
			}
			if class == "" {
				cur.WriteString(html.EscapeString(part))
			} else {
				cur.WriteString(`<span class="` + class + `">` + html.EscapeString(part) + `</span>`)
			}
		}
	}
	out = append(out, cur.String())
	// Lexers may add a final newline; anything else means the lines no
	// longer correspond and the result cannot be used.
	if len(out) == len(lines)+1 && out[len(out)-1] == "" {
		out = out[:len(lines)]
	}
	return out, len(out) == len(lines)
}

// tokenClass maps a token to one of a handful of CSS classes. The page
// carries its own small colour scheme rather than a chroma style, so the
// output does not change when chroma's styles do.
func tokenClass(t chroma.TokenType) string {
	switch {
	case t.InCategory(chroma.Comment):
		return "hc"
	case t.InCategory(chroma.Keyword), t == chroma.OperatorWord:
		return "hk"
	case t.InSubCategory(chroma.LiteralString):
		return "hs"
	case t.InSubCategory(chroma.LiteralNumber):
		return "hn"
	}
	switch t {
	case chroma.NameFunction, chroma.NameFunctionMagic, chroma.NameClass, chroma.NameDecorator, chroma.NameTag:
		return "hf"
	case chroma.NameBuiltin, chroma.NameBuiltinPseudo, chroma.NameConstant:
		return "hb"
	}
	return ""
}
