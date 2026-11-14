package gitx_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"storied/internal/gittest"
	"storied/internal/gitx"
)

func TestRepo(t *testing.T) {
	r := gittest.New(t)
	r.Write("a.txt", "a\n")
	first := r.Commit("first")
	r.Git("checkout", "-q", "-b", "feature")
	r.Write("b.txt", "b\n")
	feature := r.Commit("feature work")
	r.Git("checkout", "-q", "main")
	r.Write("c.txt", "c\n")
	r.Commit("main moves on")

	ctx := context.Background()
	repo := gitx.Repo{Dir: r.Dir}

	if got, err := repo.RevParse(ctx, "feature"); err != nil || got != feature {
		t.Errorf("RevParse(feature) = %q, %v; want %q", got, err, feature)
	}
	if got, err := repo.MergeBase(ctx, "main", "feature"); err != nil || got != first {
		t.Errorf("MergeBase(main, feature) = %q, %v; want %q", got, err, first)
	}
	if root, err := repo.Root(ctx); err != nil || root == "" {
		t.Errorf("Root() = %q, %v", root, err)
	}

	_, err := repo.RevParse(ctx, "no-such-ref")
	var gitErr *gitx.Error
	if !errors.As(err, &gitErr) {
		t.Fatalf("RevParse(no-such-ref) error = %v, want *gitx.Error", err)
	}
	if gitErr.ExitCode == 0 || gitErr.Stderr == "" || !strings.Contains(err.Error(), "rev-parse") {
		t.Errorf("error = %+v, want a non-zero exit code, stderr, and the command in the message", gitErr)
	}

	for _, rev := range []string{"", "-x", "--upload-pack=evil"} {
		if _, err := repo.RevParse(ctx, rev); err == nil {
			t.Errorf("RevParse(%q): got nil error", rev)
		}
		if _, err := repo.MergeBase(ctx, "main", rev); err == nil {
			t.Errorf("MergeBase(main, %q): got nil error", rev)
		}
	}
}

func TestRunOutsideRepo(t *testing.T) {
	_, err := gitx.Repo{Dir: t.TempDir()}.Root(context.Background())
	var gitErr *gitx.Error
	if !errors.As(err, &gitErr) {
		t.Fatalf("Root() outside a repository: error = %v, want *gitx.Error", err)
	}
}

func TestShort(t *testing.T) {
	for in, want := range map[string]string{
		"94545c99faf4f4b813ed21e15776123418fc971e":                         "94545c99f",
		"94545c99faf4f4b813ed21e15776123418fc971e94545c99faf4f4b813ed21e1": "94545c99f",
		"94545c9": "94545c9",
		"main":    "main",
		"HEAD~1":  "HEAD~1",
		"":        "",
		"a-branch-name-that-is-exactly-forty-char": "a-branch-name-that-is-exactly-forty-char",
	} {
		if got := gitx.Short(in); got != want {
			t.Errorf("Short(%q) = %q, want %q", in, got, want)
		}
	}
}
