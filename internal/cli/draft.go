package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"storied/internal/bundle"
	"storied/internal/draft"
	"storied/internal/lint"
)

func runInit(args []string, stdout, stderr io.Writer) error {
	fs, bf := newFlags("init", "[flags]", stderr)
	fs.Lookup("base").Usage = "`branch` the PR targets (default: the remote's default branch, else main or master)"
	github := fs.String("github", "", "GitHub repository as `owner/name` (default: taken from the origin remote)")
	pr := fs.Int("pr", 0, "pull request `number`, if there is one yet")
	title := fs.String("title", "", "story `title` (default: the commit subject or the branch name)")
	if err := parse(fs, args); err != nil {
		return err
	}
	res, err := draft.Init(context.Background(), draft.InitOptions{
		RepoDir: bf.repo, BundleDir: bf.bundle, Base: bf.base, Head: bf.head,
		GitHub: *github, PR: *pr, Title: *title, Tool: "storied/" + Version,
	})
	if err != nil {
		return reportIssues(err, stderr)
	}
	fmt.Fprintf(stdout, "wrote %s\n", res.Bundle)
	fmt.Fprintf(stdout, "  title  %s\n", res.Title)
	fmt.Fprintf(stdout, "  base   %s (%s)\n", res.BaseRef, res.BaseSHA[:9])
	fmt.Fprintf(stdout, "  head   %s\n", res.HeadSHA[:9])
	if res.GitHub != "" {
		fmt.Fprintf(stdout, "  repo   %s\n", res.GitHub)
	}
	if res.Excluded {
		fmt.Fprintln(stdout, "added the bundle directory to .git/info/exclude")
	}
	fmt.Fprintln(stdout, "next: storied draft")
	return nil
}

func runDraft(args []string, stdout, stderr io.Writer) error {
	fs, bf := newFlags("draft", "[flags]", stderr)
	fs.Lookup("base").Usage = "`branch` the PR targets (default: the manifest's base_ref)"
	asJSON := fs.Bool("json", false, "write the sections and the changes under each as JSON")
	dryRun := fs.Bool("dry-run", false, "show what would be drafted without writing")
	byFile := fs.Bool("by-file", false, "also write a section with file anchors for each group of unexplained files")
	if err := parse(fs, args); err != nil {
		return err
	}
	res, err := draft.Run(context.Background(), draft.Options{
		RepoDir: bf.repo, BundleDir: bf.bundle, Base: bf.base, Head: bf.head, DryRun: *dryRun, ByFile: *byFile,
	})
	if err != nil {
		return reportIssues(err, stderr)
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}

	added, needProse := 0, 0
	for _, s := range res.Sections {
		lines := 0
		for _, f := range s.Files {
			lines += f.Added + f.Deleted
		}
		note := ""
		if s.NeedsProse {
			needProse++
			note = "  needs prose"
		}
		if s.Status == "new" {
			added++
		}
		fmt.Fprintf(stdout, "  %-4s  %s  (%s, %s)%s\n", s.Status, s.ID, plural(len(s.Files), "file"), plural(lines, "line"), note)
	}
	for _, a := range res.Attention {
		fmt.Fprintf(stdout, "check: %s\n", a)
	}
	var loose []lint.BlockReport
	for _, b := range res.Blocks {
		if b.Unclaimed > 0 {
			loose = append(loose, b.BlockReport)
		}
	}
	if len(loose) > 0 {
		fmt.Fprintf(stdout, "%d of %s not in the story yet:\n", len(loose), plural(len(res.Blocks), "change block"))
		lint.WriteBlocks(stdout, loose)
		fmt.Fprintln(stdout, `attach each to the section that explains it with ::anchor{hunk="<id>"}`)
	} else {
		fmt.Fprintf(stdout, "every one of the %s is in the story\n", plural(len(res.Blocks), "change block"))
	}
	switch {
	case *dryRun && *byFile:
		fmt.Fprintf(stdout, "dry run: would write %s; nothing written\n", plural(added, "new section"))
	case *dryRun:
		fmt.Fprintln(stdout, "dry run: nothing written")
	case res.Written && added > 0:
		fmt.Fprintf(stdout, "wrote %s to %s\n", plural(added, "new section"), res.Bundle)
	case res.Written:
		fmt.Fprintf(stdout, "updated %s\n", res.Bundle)
	}
	if needProse > 0 {
		fmt.Fprintf(stdout, "next: write the prose for %s, then run storied lint\n", plural(needProse, "section"))
	}
	return nil
}

// reportIssues prints bundle issues one per line and marks the command as
// failed; any other error is returned for the caller to print.
func reportIssues(err error, stderr io.Writer) error {
	var issues bundle.Issues
	if !errors.As(err, &issues) {
		return err
	}
	for _, i := range issues {
		fmt.Fprintf(stderr, "error: %s\n", i)
	}
	return errFailed
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
