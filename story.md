---
title: Rename the module to github.com/nickelsey/storied
---

## Why {#why author=agent}

The module was named `storied` until the repository had a home. Now that it
lives on GitHub, the module path is the real import path, so `go install`
works and other modules can depend on it.

::anchor{hunk="5b06c1d"}

## The version flag {#ldflags author=agent}

The Makefile stamps the version into the binary by naming the variable's full
package path, so that path changes with the module. The comment in `main.go`
that documents the flag changes with it.

::anchor{hunk="061bee3"}
::anchor{hunk="a1ff544"}

## Base branch by name {#base-ref author=agent}

`storied init` used to record `base_ref: origin/main` when it found the
remote's default branch, which ties the bundle to one clone's remote name.
It now records `main`. Since `main` and `origin/main` can disagree, the
merge base comes from whichever contains more of the head's history:
origin when the local branch was never pulled, local when it has commits
not pushed yet, and origin when the two have diverged. Draft resolves the
name the same way and rewrites an old `origin/` prefix.

::anchor{glob="internal/draft/init.go"}
::anchor{glob="internal/draft/draft.go"}
::anchor{glob="internal/draft/draft_test.go"}

## Imports {#imports author=agent}

Every other change is an import line rewritten from `storied/internal/...` to
`github.com/nickelsey/storied/internal/...`. The bundle schema name
`storied/v1` is a format identifier, not an import path, and is unchanged.

::anchor{glob="internal/**/*.go"}
