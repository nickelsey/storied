package draft

import (
	"fmt"
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	for p, want := range map[string]string{
		"go.sum":                         "go.sum",
		"web/package-lock.json":          "web/package-lock.json",
		"deps.lock":                      "deps.lock",
		"api/v1/service.pb.go":           "api/v1/service.pb.go",
		"api/service_pb2.py":             "api/service_pb2.py",
		"web/dist.min.js":                "web/dist.min.js",
		"ui/__snapshots__/a.snap":        "ui/__snapshots__/**",
		"vendor/github.com/x/y/z.go":     "vendor/**",
		"web/node_modules/left-pad/i.js": "web/node_modules/**",
		"schema.generated.ts":            "schema.generated.ts",
		"locksmith.py":                   "",
		"core/generated_report.py":       "",
		"vendor[1]/x.go":                 "",
		"odd{name}.lock":                 "",
	} {
		if got := skimGlob(p); got != want {
			t.Errorf("skimGlob(%q) = %q, want %q", p, got, want)
		}
	}
	for p, want := range map[string]bool{
		"tests/export/test_repair.py": true,
		"core/mesh/repair_test.go":    true,
		"web/src/button.test.tsx":     true,
		"web/src/button.spec.ts":      true,
		"tests/fixtures/meshes/a.obj": true,
		"core/testdata/golden.json":   true,
		"conftest.py":                 true,
		"src/FooTest.java":            true,
		"core/mesh/repair.py":         false,
		"core/latest.py":              false,
		"contest/entry.py":            false,
		"testing.md":                  false,
	} {
		if got := isTest(p); got != want {
			t.Errorf("isTest(%q) = %v, want %v", p, got, want)
		}
	}
	for p, want := range map[string]string{
		"tests/export/test_repair.py": "repair",
		"core/mesh/repair_test.go":    "repair",
		"web/button.spec.ts":          "button",
		"src/FooTest.java":            "Foo",
		"core/mesh/repair.py":         "repair",
	} {
		if got := stem(p); got != want {
			t.Errorf("stem(%q) = %q, want %q", p, got, want)
		}
	}
}

// outline renders proposals compactly: one line per section.
func outline(ps []proposal) string {
	var b strings.Builder
	for _, p := range ps {
		fmt.Fprintf(&b, "%s | %s | %s\n", p.id, p.title, strings.Join(p.anchors, " "))
	}
	return b.String()
}

func TestPropose(t *testing.T) {
	tests := []struct {
		name  string
		files []changed
		taken []string
		want  string
		skip  string
	}{
		{
			name: "small files of a directory merge, tests follow their code, skim goes last",
			files: []changed{
				{"README.md", 2}, {"core/mesh/io.py", 10}, {"core/mesh/repair.py", 20}, {"go.sum", 4},
				{"plugins/export.py", 5}, {"tests/test_export.py", 9}, {"tests/test_other.py", 3}, {"tests/test_repair.py", 9},
			},
			want: `readme-md | README.md | path="README.md"
core-mesh | Changes in core/mesh | path="core/mesh/io.py" path="core/mesh/repair.py"
tests-core-mesh-repair-py | Tests for core/mesh/repair.py | path="tests/test_repair.py"
plugins-export-py | plugins/export.py | path="plugins/export.py"
tests-plugins-export-py | Tests for plugins/export.py | path="tests/test_export.py"
tests | Tests in tests | path="tests/test_other.py"
generated | Lockfiles, generated and vendored files | glob="go.sum" skim
`,
		},
		{
			name:  "a large file gets its own section",
			files: []changed{{"a/big.go", ownSection}, {"a/small.go", 3}, {"a/tiny.go", 1}},
			want: `a-big-go | a/big.go | path="a/big.go"
a | Changes in a | path="a/small.go" path="a/tiny.go"
`,
		},
		{
			name:  "a directory with too much for one section is split",
			files: []changed{{"a/one.go", 70}, {"a/two.go", 70}, {"a/three.go", 70}, {"a/four.go", 70}, {"a/five.go", 70}, {"a/six.go", 70}},
			want: `a | Changes in a (part 1) | path="a/one.go" path="a/two.go" path="a/three.go" path="a/four.go"
a-2 | Changes in a (part 2) | path="a/five.go" path="a/six.go"
`,
		},
		{
			name: "many files become one glob",
			files: []changed{{"a/1.go", 1}, {"a/2.go", 1}, {"a/3.go", 1}, {"a/4.go", 1}, {"a/5.go", 1},
				{"a/6.go", 1}, {"a/7.go", 1}, {"a/8.go", 1}, {"a/9.go", 1}},
			want: "a | Changes in a | glob=\"a/*\"\n",
		},
		{
			name:  "ids avoid the ones in use",
			files: []changed{{"why.md", 1}, {"x/why.md", 1}},
			taken: []string{"why-md", "x-why-md", "x-why-md-2"},
			want: `why-md-2 | why.md | path="why.md"
x-why-md-3 | x/why.md | path="x/why.md"
`,
		},
		{
			name:  "vendored trees collapse to one glob each",
			files: []changed{{"vendor/a/x.go", 5}, {"vendor/b/y.go", 5}, {"web/node_modules/p/i.js", 5}, {"yarn.lock", 9}},
			want:  "generated | Lockfiles, generated and vendored files | glob=\"vendor/**\" skim glob=\"web/node_modules/**\" skim glob=\"yarn.lock\" skim\n",
		},
		{
			name:  "awkward paths",
			files: []changed{{`say "hi".txt`, 1}, {`back\slash.txt`, 1}, {"dir {x}/a.txt", 1}, {"ünï/c d.txt", 1}},
			want: `say-hi-txt | say "hi".txt | path="say \"hi\".txt"
dir-x-a-txt | dir {x}/a.txt | path="dir {x}/a.txt"
n-c-d-txt | ünï/c d.txt | path="ünï/c d.txt"
`,
			skip: `back\slash.txt`,
		},
		{name: "nothing to do", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			taken := map[string]bool{}
			for _, id := range tt.taken {
				taken[id] = true
			}
			got, skipped := propose(tt.files, taken)
			if outline(got) != tt.want {
				t.Errorf("got:\n%swant:\n%s", outline(got), tt.want)
			}
			if strings.Join(skipped, ",") != tt.skip {
				t.Errorf("skipped %q, want %q", skipped, tt.skip)
			}
		})
	}
}
