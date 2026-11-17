package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"

	"github.com/nickelsey/storied/internal/gitx"
	"github.com/nickelsey/storied/internal/pack"
)

// publishFlags returns a flag set with the flags pack and fetch share.
func publishFlags(name, synopsis string, stderr io.Writer) (fs *flag.FlagSet, repo, bundleDir, remote *string) {
	fs = flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: storied %s %s\n\nFlags:\n", name, synopsis)
		fs.PrintDefaults()
	}
	repo = fs.String("repo", "", "`dir`ectory inside the repository (default: the current directory)")
	bundleDir = fs.String("bundle", "", "bundle `dir`ectory (default: .storied at the repository root)")
	remote = fs.String("remote", "", "git `remote` that holds the PR (default: origin)")
	return fs, repo, bundleDir, remote
}

func runPack(args []string, stdout, stderr io.Writer) error {
	fs, repo, bundleDir, remote := publishFlags("pack", "[flags]", stderr)
	pr := fs.Int("pr", 0, "pull request `number` (default: the manifest's pr)")
	noPush := fs.Bool("no-push", false, "commit the bundle to the local ref only")
	if err := parse(fs, args); err != nil {
		return err
	}
	res, err := pack.Pack(context.Background(), pack.Options{
		RepoDir: *repo, BundleDir: *bundleDir, PR: *pr, Remote: *remote, NoPush: *noPush,
	})
	if err != nil {
		return reportIssues(err, stderr)
	}
	fmt.Fprintf(stdout, "packed %s as %s (%s)\n", plural(len(res.Files), "file"), res.Ref, gitx.Short(res.Commit))
	if res.Remote != "" {
		fmt.Fprintf(stdout, "pushed to %s\n", res.Remote)
	} else {
		fmt.Fprintln(stdout, "not pushed")
	}
	return nil
}

func runFetch(args []string, stdout, stderr io.Writer) error {
	fs, repo, bundleDir, remote := publishFlags("fetch", "<pr> [flags]", stderr)
	local := fs.Bool("local", false, "use the bundle ref already in this repository; fetch nothing")
	force := fs.Bool("force", false, "replace a bundle that was not fetched, such as one you are writing")
	// The number may come before the flags, where the flag package would
	// stop reading them.
	var number string
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		number, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return err
		}
		return usageError{}
	}
	switch {
	case number == "" && fs.NArg() == 1:
		number = fs.Arg(0)
	case number == "" || fs.NArg() > 0:
		return usageError{"expected one pull request number"}
	}
	pr, err := strconv.Atoi(number)
	if err != nil || pr <= 0 {
		return usageError{fmt.Sprintf("%q is not a pull request number", number)}
	}

	res, err := pack.Fetch(context.Background(), pack.FetchOptions{
		RepoDir: *repo, BundleDir: *bundleDir, PR: pr, Remote: *remote, Local: *local, Force: *force,
	})
	if err != nil {
		return reportIssues(err, stderr)
	}
	fmt.Fprintf(stdout, "fetched %s into %s\n", plural(len(res.Files), "file"), res.Bundle)
	if !res.Current {
		fmt.Fprintf(stdout, "the story was written for %s, which is not the commit checked out here; check out the PR branch before rendering\n", gitx.Short(res.HeadSHA))
	}
	fmt.Fprintln(stdout, "next: storied render")
	return nil
}
