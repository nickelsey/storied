// Package gittest builds throwaway git repositories for tests.
package gittest

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Repo is a git repository in a temporary directory.
type Repo struct {
	Dir     string
	t       testing.TB
	commits int
}

// New creates an empty repository on branch main. It isolates git from the
// user's and the system's configuration for the rest of the test, so tests
// that call New cannot run in parallel.
func New(t testing.TB) *Repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is required to run this test: %v", err)
	}
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(cfg, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"GIT_CONFIG_GLOBAL":   cfg,
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME":     "Test",
		"GIT_AUTHOR_EMAIL":    "test@example.com",
		"GIT_COMMITTER_NAME":  "Test",
		"GIT_COMMITTER_EMAIL": "test@example.com",
	} {
		t.Setenv(k, v)
	}
	r := &Repo{Dir: t.TempDir(), t: t}
	r.Git("init", "-q", "-b", "main")
	r.Git("config", "core.autocrlf", "false")
	return r
}

// Git runs git in the repository and returns its trimmed stdout.
func (r *Repo) Git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Dir
	// Fixed, increasing dates make commit SHAs reproducible.
	date := fmt.Sprintf("%d +0000", 1700000000+r.commits)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}

// Write creates or replaces a file. The path uses forward slashes.
func (r *Repo) Write(path, content string) {
	r.t.Helper()
	full := filepath.Join(r.Dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// Remove deletes a file from the worktree.
func (r *Repo) Remove(path string) {
	r.t.Helper()
	if err := os.Remove(filepath.Join(r.Dir, filepath.FromSlash(path))); err != nil {
		r.t.Fatal(err)
	}
}

// Commit stages every change in the worktree, commits, and returns the SHA.
func (r *Repo) Commit(msg string) string {
	r.t.Helper()
	r.Git("add", "-A")
	return r.CommitIndex(msg)
}

// CommitIndex commits what is already staged and returns the SHA. Use it
// after changing the index directly, for example with git update-index.
func (r *Repo) CommitIndex(msg string) string {
	r.t.Helper()
	r.Git("commit", "-q", "--allow-empty", "-m", msg)
	r.commits++
	return r.Git("rev-parse", "HEAD")
}
