package render

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html/template"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"

	"github.com/nickelsey/storied/internal/anchor"
	"github.com/nickelsey/storied/internal/bundle"
	"github.com/nickelsey/storied/internal/diff"
	"github.com/nickelsey/storied/internal/gitx"
	"github.com/nickelsey/storied/internal/lint"
)

//go:embed page.html.tmpl
var pageTmpl string

//go:embed page.css
var pageCSS string

//go:embed page.js
var pageJS string

var tmpl = template.Must(template.New("page").Parse(pageTmpl))

// maxText is the most text shown inline for a log-like artifact.
const maxText = 64 << 10

// Raw HTML in Markdown is dropped and unsafe link schemes are removed,
// which is goldmark's default. A story is written by the PR author and may
// be written by an agent, so it is not trusted to carry markup.
var markdown = goldmark.New(goldmark.WithExtensions(extension.GFM))

type page struct {
	Title      string
	Repo       string
	PR         int
	PRURL      string
	Base, Head string
	BundleHead string
	Coverage   coverageView
	Files      int
	Warnings   []string
	Preamble   template.HTML
	Sections   []sectionView
	Uncovered  []fileView
	Agent      template.HTML
	CSS        template.CSS
	JS         template.JS
}

type coverageView struct {
	anchor.Coverage
	Parts []coveragePart
}

type coveragePart struct {
	Class   string
	Count   int
	Percent float64
}

type sectionView struct {
	ID, Title    string
	Author, Risk string
	Body         template.HTML
	Shown        int
	Elsewhere    int // lines it claims that are shown under another section
	Coverage     coverageView
	Notes        []noteView
	Artifacts    []artifactView
	Files        []fileView
}

type noteView struct {
	State, Spec, Reason string
}

type fileView struct {
	Path, OldPath  string
	Status         string
	Added, Deleted int
	URL            string
	Skim           bool
	Note           string // set for a file that changed without line changes
	Hunks          []hunkView
}

type hunkView struct {
	Header string
	Lines  []lineView
}

type lineView struct {
	Kind           string // "ctx", "add" or "del"
	Old, New       string
	OldURL, NewURL string
	HTML           template.HTML
	NoEOL          bool
}

type artifactView struct {
	ID, Kind    string
	Class       string // provenance class
	Source      string // where the artifact is said to come from
	SourceURL   string
	Outdated    string // set when the artifact was produced at another commit
	Images      []imageView
	HTML        template.HTML
	Text        string
	Truncated   bool
	Placeholder string
}

type imageView struct {
	Label string
	Src   template.URL
}

// Page writes the review page for a lint result without errors.
func Page(w io.Writer, res *lint.Result) error {
	if res.Resolution == nil {
		return fmt.Errorf("the bundle has errors; run storied lint")
	}
	b, r, rep := res.Bundle, res.Resolution, res.Report
	rd := &renderer{b: b, r: r, hl: newHighlighter(), hunks: map[[2]int][]template.HTML{}}
	if b.Manifest.Repo != "" && b.Manifest.PR > 0 {
		rd.prURL = fmt.Sprintf("https://github.com/%s/pull/%d", b.Manifest.Repo, b.Manifest.PR)
	}

	p := page{
		Title:      b.Story.Title,
		Repo:       b.Manifest.Repo,
		PR:         b.Manifest.PR,
		PRURL:      rd.prURL,
		Base:       gitx.Short(rep.Base),
		Head:       gitx.Short(rep.Head),
		BundleHead: gitx.Short(rep.BundleHead),
		Coverage:   newCoverageView(rep.Coverage),
		Files:      len(r.Diff.Files),
		CSS:        template.CSS(pageCSS),
		JS:         template.JS(pageJS),
	}
	for _, f := range rep.Warnings {
		p.Warnings = append(p.Warnings, f.String())
	}
	var err error
	if p.Preamble, err = renderMarkdown(b.Story.Preamble); err != nil {
		return err
	}
	if p.Agent, err = renderMarkdown(b.Agent); err != nil {
		return err
	}

	artifacts := map[string]bundle.Artifact{}
	for _, a := range b.Manifest.Artifacts {
		artifacts[a.ID] = a
	}
	for i, sec := range b.Story.Sections {
		res := r.Sections[i]
		sv := sectionView{
			ID: sec.ID, Title: sec.Title, Author: sec.Author, Risk: sec.Risk,
			Shown:     len(res.Primary),
			Elsewhere: len(res.Units) - len(res.Primary),
			Files:     rd.files(res.Shown),
		}
		if sv.Body, err = renderMarkdown(sec.Body); err != nil {
			return err
		}
		var cov anchor.Coverage
		for _, u := range res.Primary {
			cov.Add(r.Class[u])
		}
		sv.Coverage = newCoverageView(cov)
		for _, c := range res.Claims {
			if c.State == anchor.Stale || c.State == anchor.Unsupported {
				sv.Notes = append(sv.Notes, noteView{State: string(c.State), Spec: lint.Spec(c.Anchor), Reason: c.Reason})
			}
		}
		for _, ref := range sec.Artifacts {
			av, err := rd.artifact(artifacts[ref.ID])
			if err != nil {
				return err
			}
			sv.Artifacts = append(sv.Artifacts, av)
		}
		p.Sections = append(p.Sections, sv)
	}

	var uncovered []int
	for u, owner := range r.Owner {
		if owner < 0 {
			uncovered = append(uncovered, u)
		}
	}
	p.Uncovered = rd.files(uncovered)

	// Render to memory first so a template error leaves no partial page.
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, p); err != nil {
		return err
	}
	_, err = w.Write(buf.Bytes())
	return err
}

func newCoverageView(c anchor.Coverage) coverageView {
	v := coverageView{Coverage: c}
	for _, part := range []struct {
		class string
		n     int
	}{{"specific", c.Specific}, {"broad", c.Broad}, {"skim", c.Skim}, {"uncovered", c.Uncovered}} {
		if part.n > 0 {
			v.Parts = append(v.Parts, coveragePart{part.class, part.n, float64(part.n*1000/c.Total) / 10})
		}
	}
	return v
}

func renderMarkdown(src string) (template.HTML, error) {
	if strings.TrimSpace(src) == "" {
		return "", nil
	}
	var buf bytes.Buffer
	if err := markdown.Convert([]byte(src), &buf); err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil
}

type renderer struct {
	b     *bundle.Bundle
	r     *anchor.Resolution
	hl    *highlighter
	hunks map[[2]int][]template.HTML // highlighted lines by file and hunk index
	prURL string
}

// files builds the diff view of a list of units, keeping their order. Each
// run of units that goes forward through one file becomes one file view, so
// a chapter that names a block of a.go, then b.go, then a.go again shows
// a.go twice. A hunk is cut down to the changes in the set plus their
// context, so two sections that explain different parts of one hunk each
// show their part.
func (rd *renderer) files(units []int) []fileView {
	var out []fileView
	for start := 0; start < len(units); {
		fi := rd.r.Units[units[start]].File
		end := start + 1
		for end < len(units) && rd.r.Units[units[end]].File == fi && units[end] > units[end-1] {
			end++
		}
		out = append(out, rd.file(fi, units[start:end]))
		start = end
	}
	return out
}

func (rd *renderer) file(fi int, units []int) fileView {
	f := rd.r.Diff.Files[fi]
	fv := fileView{Path: f.Path(), Status: f.Status.String(), Skim: true}
	if f.OldPath != "" && f.NewPath != "" && f.OldPath != f.NewPath {
		fv.OldPath = f.OldPath
	}
	if rd.prURL != "" {
		fv.URL = rd.prURL + "/files#diff-" + pathHash(f.Path())
	}

	owned := map[int]map[int]bool{} // hunk index to owned line indexes
	for _, u := range units {
		unit := rd.r.Units[u]
		if rd.r.Class[u] != anchor.Skim {
			fv.Skim = false
		}
		if unit.WholeFile() {
			fv.Note = wholeFileNote(f)
			continue
		}
		if owned[unit.Hunk] == nil {
			owned[unit.Hunk] = map[int]bool{}
		}
		owned[unit.Hunk][unit.Line] = true
	}

	for hi, h := range f.Hunks {
		if owned[hi] == nil {
			continue
		}
		key := [2]int{fi, hi}
		htmlLines, ok := rd.hunks[key]
		if !ok {
			htmlLines = rd.hl.hunk(f.Path(), h)
			rd.hunks[key] = htmlLines
		}
		for _, seg := range segments(h, owned[hi]) {
			hv := hunkView{Header: h.Header}
			for i := seg[0]; i < seg[1]; i++ {
				l := h.Lines[i]
				lv := lineView{Kind: "ctx", HTML: htmlLines[i], NoEOL: l.NoEOL}
				switch l.Kind {
				case diff.Add:
					lv.Kind = "add"
					fv.Added++
				case diff.Delete:
					lv.Kind = "del"
					fv.Deleted++
				}
				if l.Old > 0 {
					lv.Old = strconv.Itoa(l.Old)
				}
				if l.New > 0 {
					lv.New = strconv.Itoa(l.New)
				}
				if fv.URL != "" {
					// GitHub addresses a deleted line on the left side
					// and every other line on the right.
					if l.Kind == diff.Delete {
						lv.OldURL = fv.URL + "L" + lv.Old
					} else {
						lv.NewURL = fv.URL + "R" + lv.New
					}
				}
				hv.Lines = append(hv.Lines, lv)
			}
			fv.Hunks = append(fv.Hunks, hv)
		}
	}
	return fv
}

// segments returns the half-open line ranges of h to show for the owned
// changed lines: each run of owned changes with up to three lines of context
// on either side, never reaching across a change that is not owned.
func segments(h diff.Hunk, owned map[int]bool) [][2]int {
	const context = 3
	show := make([]bool, len(h.Lines))
	mark := func(from, to, step int) {
		dist := context + 1 // context lines since the last owned change
		for i := from; i != to; i += step {
			switch {
			case owned[i]:
				show[i], dist = true, 0
			case h.Lines[i].Kind != diff.Context:
				dist = context + 1
			default:
				if dist++; dist <= context {
					show[i] = true
				}
			}
		}
	}
	mark(0, len(h.Lines), 1)
	mark(len(h.Lines)-1, -1, -1)

	var out [][2]int
	for i := 0; i < len(show); {
		if !show[i] {
			i++
			continue
		}
		j := i
		for j < len(show) && show[j] {
			j++
		}
		out = append(out, [2]int{i, j})
		i = j
	}
	return out
}

func wholeFileNote(f diff.File) string {
	switch {
	case f.Binary:
		return "Binary file " + f.Status.String() + "."
	case f.Status == diff.Renamed:
		return "Renamed without changes."
	case f.Status == diff.Copied:
		return "Copied without changes."
	case f.Status == diff.Added:
		return "Empty file added."
	case f.Status == diff.Deleted:
		return "Empty file deleted."
	case f.OldMode != f.NewMode:
		return fmt.Sprintf("Mode changed from %s to %s.", f.OldMode, f.NewMode)
	}
	return "Changed without line changes."
}

// pathHash is the fragment GitHub uses for a file on a PR's "Files changed"
// page: the SHA-256 of its path.
func pathHash(p string) string {
	sum := sha256.Sum256([]byte(p))
	return hex.EncodeToString(sum[:])
}

var imageTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".svg":  "image/svg+xml",
}

func (rd *renderer) artifact(a bundle.Artifact) (artifactView, error) {
	av := artifactView{ID: a.ID, Kind: a.Kind, Class: a.Provenance.Class}
	switch p := a.Provenance; p.Class {
	case "ci":
		av.Source, av.SourceURL = "CI run", p.RunURL
	case "gpu-job":
		av.Source = "GPU job " + p.JobID
	case "author":
		av.Source = "the author's machine"
	case "reviewer-agent":
		av.Source = "a review agent"
	}
	if a.Provenance.Command != "" {
		av.Source += ", " + a.Provenance.Command
	}
	if sha := a.Provenance.SHA; sha != "" && sha != rd.r.Diff.Head {
		av.Outdated = gitx.Short(sha)
	}

	read := func(rel string) ([]byte, error) {
		return os.ReadFile(filepath.Join(rd.b.Dir, filepath.FromSlash(rel)))
	}
	image := func(label, rel string) error {
		mime, ok := imageTypes[strings.ToLower(path.Ext(rel))]
		if !ok {
			av.Placeholder = fmt.Sprintf("%s is not an image type this page can show.", rel)
			return nil
		}
		data, err := read(rel)
		if err != nil {
			return err
		}
		av.Images = append(av.Images, imageView{
			Label: label,
			Src:   template.URL("data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)),
		})
		return nil
	}

	switch a.Kind {
	case "markdown":
		data, err := read(a.Path)
		if err != nil {
			return av, err
		}
		av.HTML, err = renderMarkdown(string(data))
		return av, err
	case "image":
		return av, image("", a.Path)
	case "image-compare":
		if err := image("Before", a.Before); err != nil {
			return av, err
		}
		return av, image("After", a.After)
	case "log", "test-report", "k8s-diff":
		data, err := read(a.Path)
		if err != nil {
			return av, err
		}
		if len(data) > maxText {
			data, av.Truncated = data[:maxText], true
		}
		av.Text = strings.ToValidUTF8(string(data), "�")
		return av, nil
	}
	// Kinds that need a viewer this page does not have.
	if a.Fallback != "" {
		return av, image("", a.Fallback)
	}
	av.Placeholder = fmt.Sprintf("A %s artifact cannot be shown on a static page, and the bundle has no fallback image.", a.Kind)
	return av, nil
}
