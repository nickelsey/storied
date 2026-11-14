// Package cli implements the storied command line: argument dispatch, help and exit codes.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"storied/internal/lint"
)

// Version is the build version, set at link time with
// -ldflags "-X storied/internal/cli.Version=...".
var Version = "dev"

// Exit codes returned by Run.
const (
	ExitOK    = 0
	ExitError = 1
	ExitUsage = 2
)

// errFailed is returned by a command that has printed its own findings and
// only needs the exit code set.
var errFailed = errors.New("failed")

// usageError reports a mistake in the command line. An empty msg means the
// flag package has already printed it.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

// newFlags returns a flag set for a command, with the flags every bundle
// command shares.
func newFlags(name, synopsis string, stderr io.Writer) (*flag.FlagSet, *bundleFlags) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: storied %s %s\n\nFlags:\n", name, synopsis)
		fs.PrintDefaults()
	}
	bf := &bundleFlags{}
	fs.StringVar(&bf.repo, "repo", "", "`dir`ectory inside the repository (default: the current directory)")
	fs.StringVar(&bf.bundle, "bundle", "", "bundle `dir`ectory (default: .storied at the repository root)")
	fs.StringVar(&bf.base, "base", "", "base `rev`ision of the diff (default: the manifest's base_sha)")
	fs.StringVar(&bf.head, "head", "", "head `rev`ision of the diff (default: HEAD)")
	return fs, bf
}

type bundleFlags struct{ repo, bundle, base, head string }

func (b *bundleFlags) options() lint.Options {
	return lint.Options{RepoDir: b.repo, BundleDir: b.bundle, Base: b.base, Head: b.head}
}

// parse parses args and rejects positional arguments.
func parse(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return usageError{}
	}
	if fs.NArg() > 0 {
		return usageError{fmt.Sprintf("unexpected argument %q", fs.Arg(0))}
	}
	return nil
}

type command struct {
	name    string
	who     string
	summary string
	run     func(args []string, stdout, stderr io.Writer) error
}

// commands lists the phase 0 commands in the order help prints them.
// Phase 1+ commands (open, serve, tui, mcp) are added when their phase starts.
func commands() []command {
	return []command{
		{"init", "author", "scaffold story.md, manifest.yaml and agent.md for the current branch", runInit},
		{"draft", "author's agent", "list the change blocks of the diff and which section explains each", runDraft},
		{"lint", "author, CI", "validate the bundle, resolve anchors, report coverage and stale anchors", runLint},
		{"render", "author, reviewer", "write one static HTML page of the diff in story order", runRender},
		{"pack", "author", "checksum the bundle and publish it on the PR's side ref", runPack},
		{"fetch", "reviewer", "fetch the bundle an author published for a PR", runFetch},
		{"version", "", "print the version", runVersion},
	}
}

func runVersion(_ []string, stdout, _ io.Writer) error {
	_, err := fmt.Fprintf(stdout, "storied %s\n", Version)
	return err
}

// Run executes the command named by args and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return ExitUsage
	}
	switch args[0] {
	case "help", "-h", "--help":
		usage(stdout)
		return ExitOK
	case "--version":
		args[0] = "version"
	}
	for _, c := range commands() {
		if c.name != args[0] {
			continue
		}
		err := c.run(args[1:], stdout, stderr)
		var usage usageError
		switch {
		case err == nil:
			return ExitOK
		case errors.Is(err, errFailed):
			// The command has already reported what failed.
			return ExitError
		case errors.Is(err, flag.ErrHelp):
			return ExitOK
		case errors.As(err, &usage):
			if usage.msg != "" {
				fmt.Fprintf(stderr, "storied %s: %s\n", c.name, usage.msg)
			}
			return ExitUsage
		}
		fmt.Fprintf(stderr, "storied %s: %v\n", c.name, err)
		return ExitError
	}
	fmt.Fprintf(stderr, "storied: unknown command %q\n\n", args[0])
	usage(stderr)
	return ExitUsage
}

func usage(w io.Writer) {
	fmt.Fprint(w, "Usage: storied <command> [arguments]\n\nCommands:\n")
	for _, c := range commands() {
		fmt.Fprintf(w, "  %-8s %s\n", c.name, c.summary)
	}
}
