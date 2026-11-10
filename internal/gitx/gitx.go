// Package gitx runs git as a subprocess and reports failures as typed errors.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Repo is a git repository, addressed by any directory inside its worktree.
type Repo struct {
	Dir string
}

// Error describes a git invocation that failed.
type Error struct {
	Args     []string
	Stderr   string
	ExitCode int // -1 when git did not start or was killed
	Err      error
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), msg)
}

func (e *Error) Unwrap() error { return e.Err }

// Run executes git with args in the repository and returns its stdout.
func (r Repo) Run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.Dir
	// LC_ALL=C keeps messages parseable; no prompt because nobody is there to answer.
	cmd.Env = append(os.Environ(), "LC_ALL=C", "GIT_TERMINAL_PROMPT=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		code := -1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		}
		return nil, &Error{Args: args, Stderr: stderr.String(), ExitCode: code, Err: err}
	}
	return stdout.Bytes(), nil
}

func (r Repo) runLine(ctx context.Context, args ...string) (string, error) {
	out, err := r.Run(ctx, args...)
	return strings.TrimSpace(string(out)), err
}

// RevParse resolves rev to the full SHA of the commit it names.
func (r Repo) RevParse(ctx context.Context, rev string) (string, error) {
	if err := checkRev(rev); err != nil {
		return "", err
	}
	return r.runLine(ctx, "rev-parse", "--verify", rev+"^{commit}")
}

// MergeBase returns the best common ancestor of two commits.
func (r Repo) MergeBase(ctx context.Context, a, b string) (string, error) {
	for _, rev := range []string{a, b} {
		if err := checkRev(rev); err != nil {
			return "", err
		}
	}
	return r.runLine(ctx, "merge-base", a, b)
}

// Root returns the top-level directory of the worktree.
func (r Repo) Root(ctx context.Context) (string, error) {
	return r.runLine(ctx, "rev-parse", "--show-toplevel")
}

// checkRev rejects revisions git would read as command-line options.
func checkRev(rev string) error {
	if rev == "" || strings.HasPrefix(rev, "-") {
		return fmt.Errorf("invalid revision %q", rev)
	}
	return nil
}
