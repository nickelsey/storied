// Package pack publishes a bundle as a commit on a side ref of the PR's
// repository, and fetches one back for review.
//
// The bundle travels as a git tree rather than an archive: git already
// compresses and checksums it, a reviewer needs nothing but git to fetch
// it, and it never appears on the PR branch.
package pack

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"storied/internal/bundle"
	"storied/internal/draft"
	"storied/internal/gitx"
	"storied/internal/lint"
)

// MarkerFile is written into a fetched bundle and names the commit it came
// from. Its presence says the directory is a copy that fetch may replace.
const MarkerFile = ".fetched"

// Ref returns the ref a PR's bundle is published under.
func Ref(pr int) string { return fmt.Sprintf("refs/storied/pr-%d", pr) }

// Options selects what to pack and where to publish it.
type Options struct {
	RepoDir   string // any directory inside the worktree; "" means the current directory
	BundleDir string // "" means .storied at the worktree root
	PR        int    // 0 means the manifest's pr
	Remote    string // "" means origin
	NoPush    bool   // only write the local ref
}

// Result says what Pack published.
type Result struct {
	Ref    string
	Commit string
	Files  []string // bundle-relative paths in the published tree
	Remote string   // empty when nothing was pushed
}

// Pack checks the bundle, records a checksum for every artifact file in the
// published copy of the manifest, commits the bundle's files to the PR's
// side ref and pushes it. The author's own files are not changed.
func Pack(ctx context.Context, opts Options) (*Result, error) {
	res, err := lint.Run(ctx, lint.Options{RepoDir: opts.RepoDir, BundleDir: opts.BundleDir})
	if err != nil {
		return nil, err
	}
	rep := res.Report
	if len(rep.Errors) > 0 {
		issues := make(bundle.Issues, len(rep.Errors))
		for i, e := range rep.Errors {
			issues[i] = bundle.Issue{File: e.File, Line: e.Line, Msg: e.Msg}
		}
		return nil, issues
	}
	if rep.BundleHead != "" {
		return nil, fmt.Errorf("the bundle was written for %s but the branch is at %s; run storied draft, check the story, then pack again",
			gitx.Short(rep.BundleHead), gitx.Short(rep.Head))
	}
	b := res.Bundle
	pr := opts.PR
	if pr == 0 {
		pr = b.Manifest.PR
	}
	if pr <= 0 {
		return nil, errors.New("the manifest has no pr number; pass --pr <number>")
	}

	// The files to publish: the three bundle files and every file an
	// artifact names. Anything else in the directory, such as a rendered
	// page, stays behind.
	manifestSrc, err := os.ReadFile(filepath.Join(b.Dir, bundle.ManifestFile))
	if err != nil {
		return nil, err
	}
	sums := map[string]string{}
	contents := map[string][]byte{}
	for _, name := range []string{bundle.StoryFile, bundle.AgentFile} {
		if contents[name], err = os.ReadFile(filepath.Join(b.Dir, name)); err != nil {
			return nil, err
		}
	}
	for _, a := range b.Manifest.Artifacts {
		for _, f := range a.Files() {
			data, err := os.ReadFile(filepath.Join(b.Dir, filepath.FromSlash(f)))
			if err != nil {
				return nil, err
			}
			contents[f] = data
			sum := sha256.Sum256(data)
			sums[f] = hex.EncodeToString(sum[:])
		}
	}
	if contents[bundle.ManifestFile], err = withChecksums(manifestSrc, pr, sums); err != nil {
		return nil, err
	}

	repo, _, err := lint.Locate(ctx, opts.RepoDir, opts.BundleDir)
	if err != nil {
		return nil, err
	}
	out := &Result{Ref: Ref(pr)}
	for f := range contents {
		out.Files = append(out.Files, f)
	}
	sort.Strings(out.Files)
	if out.Commit, err = commitTree(ctx, repo, contents, out.Files,
		fmt.Sprintf("storied bundle for %s", b.Manifest.HeadSHA)); err != nil {
		return nil, err
	}
	if _, err := repo.Run(ctx, "update-ref", out.Ref, out.Commit); err != nil {
		return nil, err
	}
	if opts.NoPush {
		return out, nil
	}
	out.Remote = opts.Remote
	if out.Remote == "" {
		out.Remote = "origin"
	}
	// Forced: the ref holds only the latest bundle, and a new head replaces it.
	if _, err := repo.Run(ctx, "push", "--quiet", out.Remote, "+"+out.Ref+":"+out.Ref); err != nil {
		return out, fmt.Errorf("the bundle is committed locally as %s, but pushing it failed: %w", out.Ref, err)
	}
	return out, nil
}

// withChecksums returns the manifest with the pr number set and a sha256
// map on every artifact, replacing any the author wrote.
func withChecksums(src []byte, pr int, sums map[string]string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("manifest.yaml is not a mapping")
	}
	root := doc.Content[0]
	// Tagged as strings so a digest that happens to look like a number is
	// quoted on the way out.
	scalar := func(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }
	// set replaces or appends a key of a mapping node.
	set := func(m *yaml.Node, key string, value *yaml.Node) {
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value == key {
				m.Content[i+1] = value
				return
			}
		}
		m.Content = append(m.Content, scalar(key), value)
	}
	get := func(m *yaml.Node, key string) *yaml.Node {
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value == key {
				return m.Content[i+1]
			}
		}
		return nil
	}

	set(root, "pr", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprint(pr)})
	if artifacts := get(root, "artifacts"); artifacts != nil && artifacts.Kind == yaml.SequenceNode {
		for _, a := range artifacts.Content {
			if a.Kind != yaml.MappingNode {
				continue
			}
			sumMap := &yaml.Node{Kind: yaml.MappingNode}
			for _, field := range []string{"path", "data", "before", "after", "fallback"} {
				if v := get(a, field); v != nil && sums[v.Value] != "" && get(sumMap, v.Value) == nil {
					sumMap.Content = append(sumMap.Content, scalar(v.Value), scalar(sums[v.Value]))
				}
			}
			a.Style = 0 // an entry written in flow style would put the map on one long line
			set(a, "sha256", sumMap)
		}
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if _, issues := bundle.ParseManifest(buf.Bytes()); len(issues) > 0 {
		return nil, fmt.Errorf("adding checksums broke the manifest (this is a bug in storied):\n%w", issues)
	}
	return buf.Bytes(), nil
}

// commitTree writes the files as blobs, builds a tree from them in a
// scratch index so the user's index and worktree are untouched, and commits
// it without a parent.
func commitTree(ctx context.Context, repo gitx.Repo, contents map[string][]byte, order []string, msg string) (string, error) {
	tmp, err := os.MkdirTemp("", "storied-pack-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	scratch := repo
	scratch.Env = append(append([]string{}, repo.Env...), "GIT_INDEX_FILE="+filepath.Join(tmp, "index"))

	for i, name := range order {
		// hash-object reads the content from a file outside the worktree,
		// so no attribute or line-ending filter rewrites it.
		src := filepath.Join(tmp, fmt.Sprintf("blob-%d", i))
		if err := os.WriteFile(src, contents[name], 0o600); err != nil {
			return "", err
		}
		blob, err := line(ctx, repo, "hash-object", "-w", "--no-filters", "--", src)
		if err != nil {
			return "", err
		}
		if _, err := scratch.Run(ctx, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+name); err != nil {
			return "", err
		}
	}
	tree, err := line(ctx, scratch, "write-tree")
	if err != nil {
		return "", err
	}
	return line(ctx, repo, "commit-tree", tree, "-m", msg)
}

func line(ctx context.Context, repo gitx.Repo, args ...string) (string, error) {
	out, err := repo.Run(ctx, args...)
	return strings.TrimSpace(string(out)), err
}

// FetchOptions selects the bundle to fetch.
type FetchOptions struct {
	RepoDir   string
	BundleDir string // where to put it; "" means .storied at the worktree root
	PR        int
	Remote    string // "" means origin
	Local     bool   // use the ref already in this repository; fetch nothing
	Force     bool   // replace a bundle that was not fetched, such as the author's own
}

// FetchResult says what Fetch wrote.
type FetchResult struct {
	Bundle  string
	Commit  string
	HeadSHA string // the commit the story was written for
	Current bool   // HeadSHA is the commit checked out here
	Files   []string
}

// Fetch brings the PR's bundle into the bundle directory and verifies it.
// A bundle that fails verification is not left behind.
func Fetch(ctx context.Context, opts FetchOptions) (*FetchResult, error) {
	if opts.PR <= 0 {
		return nil, errors.New("a pull request number is required")
	}
	repo, dir, err := lint.Locate(ctx, opts.RepoDir, opts.BundleDir)
	if err != nil {
		return nil, err
	}
	// Refuse before fetching: an existing bundle without the marker is
	// someone's work, not a copy.
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 && !opts.Force {
		if _, err := os.Stat(filepath.Join(dir, MarkerFile)); errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%s holds a bundle that was not fetched; move it away or pass --force to replace it", dir)
		}
	}

	ref := Ref(opts.PR)
	if !opts.Local {
		remote := opts.Remote
		if remote == "" {
			remote = "origin"
		}
		if _, err := repo.Run(ctx, "fetch", "--quiet", remote, "+"+ref+":"+ref); err != nil {
			return nil, fmt.Errorf("no bundle for PR %d on %s: %w", opts.PR, remote, err)
		}
	}
	res := &FetchResult{Bundle: dir}
	if res.Commit, err = repo.RevParse(ctx, ref); err != nil {
		return nil, fmt.Errorf("no bundle for PR %d in this repository: %w", opts.PR, err)
	}

	// Unpack beside the destination, verify, then swap it in.
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), ".storied-fetch-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if res.Files, err = unpack(ctx, repo, res.Commit, tmp); err != nil {
		return nil, err
	}
	b, err := bundle.Load(tmp)
	if err != nil {
		return nil, err
	}
	// A published bundle carries a checksum for every artifact file.
	var missing bundle.Issues
	for _, a := range b.Manifest.Artifacts {
		for _, f := range a.Files() {
			if a.SHA256[f] == "" {
				missing = append(missing, bundle.Issue{File: bundle.ManifestFile, Line: a.Line,
					Msg: fmt.Sprintf("artifact %s: no sha256 for %q; a published bundle must have one", a.ID, f)})
			}
		}
	}
	if len(missing) > 0 {
		return nil, missing
	}
	if err := os.WriteFile(filepath.Join(tmp, MarkerFile), []byte(res.Commit+"\n"), 0o644); err != nil {
		return nil, err
	}
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return nil, err
	}
	if opts.BundleDir == "" {
		if _, err := draft.Exclude(ctx, repo, "/"+lint.DefaultBundleDir+"/"); err != nil {
			return nil, err
		}
	}
	res.HeadSHA = b.Manifest.HeadSHA
	if head, err := repo.RevParse(ctx, "HEAD"); err == nil {
		res.Current = head == res.HeadSHA
	}
	return res, nil
}

// unpack writes the commit's files into dir. The tree comes from another
// person, so only plain files at paths that stay inside dir are accepted.
func unpack(ctx context.Context, repo gitx.Repo, commit, dir string) ([]string, error) {
	out, err := repo.Run(ctx, "ls-tree", "-r", "-z", "--full-tree", commit)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if entry == "" {
			continue
		}
		meta, name, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 {
			return nil, fmt.Errorf("unexpected ls-tree entry %q", entry)
		}
		mode, kind, sha := fields[0], fields[1], fields[2]
		if kind != "blob" || (mode != "100644" && mode != "100755") {
			return nil, fmt.Errorf("the published bundle contains %q, which is not a plain file (mode %s); refusing to unpack it", name, mode)
		}
		if strings.Contains(name, `\`) || !filepath.IsLocal(filepath.FromSlash(name)) {
			return nil, fmt.Errorf("the published bundle contains the path %q, which does not stay inside the bundle; refusing to unpack it", name)
		}
		data, err := repo.Run(ctx, "cat-file", "blob", sha)
		if err != nil {
			return nil, err
		}
		dst := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return nil, err
		}
		files = append(files, name)
	}
	return files, nil
}
