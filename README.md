# storied

Storied (working name) turns a PR's story into an ordered, anchored path
through its diff. Each section of the story claims the changed lines it
explains, so a reviewer can read a large PR in story order and see which
changes nobody explained. GitHub stays the record for comments and approvals.

Status: phase 0 in progress. `init`, `draft`, `lint` and `render` work;
`pack` is not built yet. `symbol`, `content` and `commit` anchors are parsed
but not resolved, so `draft` anchors whole files.

## Build

Requires Go 1.27 or newer and `git` on the PATH.

```sh
make build   # bin/storied
make check   # vet, tests, gofmt
make dist    # static binaries for macOS arm64, Linux amd64, Windows amd64
```

## Commands (phase 0)

| Command | What it does |
| --- | --- |
| `storied init` | scaffold `story.md`, `manifest.yaml` and `agent.md` for the current branch |
| `storied draft` | group hunks into sections and write anchors |
| `storied lint` | validate the bundle, resolve anchors, report coverage and stale anchors |
| `storied render` | write one static HTML page of the diff in story order |
| `storied pack` | bundle, checksum and publish the bundle for the PR |

Every working command takes `--repo`, `--bundle`, `--base` and `--head`. The
bundle lives in `.storied/` at the repository root unless `--bundle` says
otherwise.

```sh
storied init                 # scaffold .storied/ for the current branch
storied draft                # add a section with anchors for every unexplained change
storied draft --json         # the same, plus the changes under each section, for an agent
storied lint                 # report; exit 1 on errors
storied lint --strict        # exit 1 on warnings too
storied lint --json          # the same report for CI and agents
storied render               # writes .storied/story.html
storied render -o page.html  # or - for standard output
```

`draft` can be run again after new commits or a rebase. It keeps every
section a person wrote or edited and only rewrites the skeletons nobody has
touched.

## Layout

```
cmd/storied/       entry point
internal/cli/      command dispatch
internal/gitx/     runs git, typed errors
internal/gittest/  throwaway git repositories for tests
internal/bundle/   manifest, story and agent.md parsing
internal/diff/     git diff and the hunk model
internal/anchor/   anchor resolution, staleness, coverage
internal/lint/     bundle + diff + anchors into a report with warnings
internal/draft/    init scaffold, hunk grouping, story skeleton
internal/render/   static HTML output
internal/fixture/  a repository with a bundle, shared by tests
```
