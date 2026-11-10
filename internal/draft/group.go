package draft

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Size limits for proposed sections, in changed lines.
const (
	ownSection = 80  // a file at least this large gets a section to itself
	maxMerged  = 300 // small files of one directory are merged up to this size
	manyFiles  = 8   // above this many files, a merged section uses one glob
)

// changed is a file that needs a section, with its number of changed lines.
type changed struct {
	path  string
	units int
}

// proposal is a section draft wants to write.
type proposal struct {
	id      string
	title   string
	anchors []string // attribute text of each ::anchor directive
	files   []string
}

var lockfiles = map[string]bool{
	"package-lock.json": true, "npm-shrinkwrap.json": true, "yarn.lock": true, "pnpm-lock.yaml": true,
	"bun.lockb": true, "Cargo.lock": true, "go.sum": true, "poetry.lock": true, "uv.lock": true,
	"Pipfile.lock": true, "Gemfile.lock": true, "composer.lock": true, "flake.lock": true,
	"packages.lock.json": true, "gradle.lockfile": true, "Podfile.lock": true, "mix.lock": true,
}

var (
	generatedRE = regexp.MustCompile(`(\.min\.(js|css)|\.pb\.(go|cc|h)|_pb2(_grpc)?\.pyi?|\.snap|\.g\.dart|[._]generated\.[A-Za-z0-9]+|\.lock)$`)
	testNameRE  = regexp.MustCompile(`^(test_.*|conftest\.py|.*_test\.[A-Za-z0-9]+|.*_spec\.[A-Za-z0-9]+|.*\.(test|spec)\.[A-Za-z0-9]+|.*Tests?\.(java|kt|cs|swift|scala))$`)
	testStemRE  = regexp.MustCompile(`^test_|(_test|_spec|\.test|\.spec|Tests?)$`)
	slugRE      = regexp.MustCompile(`[^a-z0-9]+`)
)

var (
	vendoredDirs = map[string]bool{"vendor": true, "node_modules": true, "__snapshots__": true}
	testDirs     = map[string]bool{"test": true, "tests": true, "__tests__": true, "testdata": true, "fixtures": true, "spec": true, "specs": true, "e2e": true}
)

// skimGlob returns the glob to skim a lockfile, a generated file or a
// vendored tree with, or "" when the file deserves a reader.
func skimGlob(p string) string {
	parts := strings.Split(p, "/")
	for i, part := range parts[:len(parts)-1] {
		if vendoredDirs[part] {
			if prefix := strings.Join(parts[:i+1], "/"); !hasGlobMeta(prefix) {
				return prefix + "/**"
			}
		}
	}
	base := parts[len(parts)-1]
	if (lockfiles[base] || generatedRE.MatchString(base)) && !hasGlobMeta(p) {
		return p
	}
	return ""
}

func hasGlobMeta(p string) bool { return strings.ContainsAny(p, `*?[]{}\`) }

func isTest(p string) bool {
	parts := strings.Split(p, "/")
	for _, part := range parts[:len(parts)-1] {
		if testDirs[strings.ToLower(part)] {
			return true
		}
	}
	return testNameRE.MatchString(parts[len(parts)-1])
}

// stem reduces a file name to the name of the thing it holds or tests:
// test_repair.py, repair_test.go and repair.spec.ts all give "repair".
func stem(p string) string {
	base := path.Base(p)
	base = strings.TrimSuffix(base, path.Ext(base))
	return testStemRE.ReplaceAllString(base, "")
}

// anchorPath returns the attribute text of a path anchor, or false when the
// story grammar cannot express the path.
func anchorPath(p string) (string, bool) {
	if strings.ContainsAny(p, "\\\n\r") {
		return "", false
	}
	return `path="` + strings.ReplaceAll(p, `"`, `\"`) + `"`, true
}

type group struct {
	first  int // position of its first file in the diff, for ordering
	dir    string
	files  []string
	units  int
	part   int      // 1-based when a directory was split into several groups
	tests  []string // test files that belong after this group
	tested []string // the files of this group those tests are named for
}

// propose groups the files into sections: code by directory, each followed
// by the tests that match it, then the remaining tests, then the files to
// skim. It also returns the files no anchor can name. taken holds section
// ids already in use.
func propose(files []changed, taken map[string]bool) (out []proposal, skipped []string) {
	var code, tests []changed
	var skim []string
	seenSkim := map[string]bool{}
	pos := map[string]int{}
	for i, f := range files {
		pos[f.path] = i
		if _, ok := anchorPath(f.path); !ok {
			skipped = append(skipped, f.path)
			continue
		}
		switch g := skimGlob(f.path); {
		case g != "":
			if !seenSkim[g] {
				seenSkim[g] = true
				skim = append(skim, g)
			}
		case isTest(f.path):
			tests = append(tests, f)
		default:
			code = append(code, f)
		}
	}

	// Code: one group per large file, the rest merged per directory.
	var groups []*group
	open := map[string]*group{} // the merged group still accepting files, by directory
	parts := map[string]int{}
	byStem := map[string]*group{}
	stemFile := map[string]string{}
	for _, f := range code {
		dir := path.Dir(f.path)
		var g *group
		if f.units >= ownSection {
			g = &group{first: pos[f.path], dir: dir}
			groups = append(groups, g)
		} else {
			g = open[dir]
			if g == nil || g.units+f.units > maxMerged {
				parts[dir]++
				g = &group{first: pos[f.path], dir: dir, part: parts[dir]}
				open[dir] = g
				groups = append(groups, g)
			}
		}
		g.files = append(g.files, f.path)
		g.units += f.units
		if s := stem(f.path); byStem[s] == nil {
			byStem[s], stemFile[s] = g, f.path
		}
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].first < groups[j].first })

	// Tests go after the code they are named for; the rest by directory.
	var loose []*group
	looseByDir := map[string]*group{}
	for _, f := range tests {
		if s := stem(f.path); byStem[s] != nil {
			g := byStem[s]
			g.tests = append(g.tests, f.path)
			if len(g.tested) == 0 || g.tested[len(g.tested)-1] != stemFile[s] {
				g.tested = append(g.tested, stemFile[s])
			}
			continue
		}
		dir := path.Dir(f.path)
		g := looseByDir[dir]
		if g == nil {
			g = &group{dir: dir}
			looseByDir[dir] = g
			loose = append(loose, g)
		}
		g.files = append(g.files, f.path)
	}

	ids := func(want string) string {
		id := strings.Trim(slugRE.ReplaceAllString(strings.ToLower(want), "-"), "-")
		if id == "" {
			id = "section"
		}
		unique := id
		for n := 2; taken[unique]; n++ {
			unique = fmt.Sprintf("%s-%d", id, n)
		}
		taken[unique] = true
		return unique
	}
	for _, g := range groups {
		subject, title := g.dir, "Changes in "+g.dir
		switch {
		case len(g.files) == 1:
			subject, title = g.files[0], g.files[0]
		case g.dir == ".":
			subject, title = "top level", "Changes at the top level"
		}
		if len(g.files) > 1 && parts[g.dir] > 1 {
			title += fmt.Sprintf(" (part %d)", g.part)
		}
		out = append(out, proposal{id: ids(subject), title: title, anchors: fileAnchors(g.dir, g.files), files: g.files})
		if len(g.tests) > 0 {
			// Name the one file the tests are for, when there is only one.
			if len(g.tested) == 1 {
				subject = g.tested[0]
			}
			out = append(out, proposal{
				id: ids("tests " + subject), title: "Tests for " + subject,
				anchors: fileAnchors("", g.tests), files: g.tests,
			})
		}
	}
	for _, g := range loose {
		subject := g.dir
		if g.dir == "." {
			subject = "the top level"
		}
		id := "tests " + g.dir
		if isTest(g.dir + "/x") {
			id = g.dir // the directory name already says so
		}
		out = append(out, proposal{
			id: ids(id), title: "Tests in " + subject,
			anchors: fileAnchors(g.dir, g.files), files: g.files,
		})
	}
	if len(skim) > 0 {
		p := proposal{id: ids("generated"), title: "Lockfiles, generated and vendored files"}
		for _, g := range skim {
			p.anchors = append(p.anchors, `glob="`+g+`" skim`)
		}
		p.files = skim
		out = append(out, p)
	}
	return out, skipped
}

// fileAnchors names the files one by one, or with a single glob when there
// are many in one directory. The glob may match other changed files of the
// directory too; their own path anchors are more specific and win.
func fileAnchors(dir string, files []string) []string {
	if len(files) > manyFiles && dir != "" && dir != "." && !hasGlobMeta(dir) {
		return []string{`glob="` + dir + `/*"`}
	}
	out := make([]string, 0, len(files))
	for _, f := range files {
		a, _ := anchorPath(f) // files that cannot be anchored were set aside
		out = append(out, a)
	}
	return out
}

// text renders the proposal as story source, ending in a newline.
func (p proposal) text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s {#%s author=agent}\n", p.title, p.id)
	for _, a := range p.anchors {
		fmt.Fprintf(&b, "::anchor{%s}\n", a)
	}
	return b.String()
}
