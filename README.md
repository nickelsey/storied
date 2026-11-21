# storied

Storied (working name) turns a PR's story into an ordered, anchored path
through its diff. Each section of the story claims the changed lines it
explains, so a reviewer can read a large PR in story order and see which
changes nobody explained. GitHub stays the record for comments and approvals.

Status: phase 0. Every command works. `symbol` and `commit` anchors are
parsed but not resolved, and publishing has only been tested against a local
remote, not GitHub.

## How a story points at code

The diff is cut into change blocks: each unbroken run of added and deleted
lines is one block, with an id that is a hash of its file path and its
changed lines. A chapter of the story lists the blocks it explains:

```markdown
## Rewiring the exporter {#rewiring author=agent}
::anchor{hunk="108c261"}
::anchor{hunk="b2af964"}

The exporter now calls the new pass before writing.
```

An id survives a rebase and edits elsewhere in the file. It changes only
when the block's own lines change, and then the block shows up as unreviewed
until it is attached again. `storied draft` lists every block with its id
and the chapter that claims it. A chapter can also claim whole files
(`path`, `glob`) or a line range (`lines`). The page shows a chapter's code
in the order its anchors are written, so a chapter can move between files
and come back.

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
| `storied draft` | list the change blocks of the diff and which section explains each |
| `storied lint` | validate the bundle, resolve anchors, report coverage and stale anchors |
| `storied render` | write one static HTML page of the diff in story order |
| `storied pack` | checksum the bundle and publish it on the PR's side ref |
| `storied fetch` | fetch the bundle an author published for a PR |

Every working command takes `--repo`, `--bundle`, `--base` and `--head`. The
bundle lives in `.storied/` at the repository root unless `--bundle` says
otherwise.

```sh
storied init                 # scaffold .storied/ for the current branch
storied draft                # list every change block and the chapter that explains it
storied draft --json         # the same with the changed lines, for an agent to write from
storied draft --by-file      # fallback: write a section per group of unexplained files
storied lint                 # report; exit 1 on errors
storied lint --strict        # exit 1 on warnings too
storied lint --json          # the same report for CI and agents
storied render               # writes .storied/story.html
storied render -o page.html  # or - for standard output
storied pack                 # publish the bundle on refs/storied/pr-<n> of origin
storied fetch 482            # reviewer: fetch and verify the bundle for PR 482
```

`draft` also brings the manifest's base and head up to date, so run it again
after new commits or a rebase. It never rewrites a section a person wrote or
edited.

`pack` leaves the author's files alone and never touches the PR branch: the
bundle is a commit of its own on a ref outside `refs/heads`. `fetch` refuses
a bundle whose artifacts do not match their checksums.

To have a coding agent write the story, give it `skills/storied/SKILL.md`.

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
internal/pack/     publish a bundle on a side ref, fetch and verify one
internal/fixture/  a repository with a bundle, shared by tests
skills/storied/    instructions that teach a coding agent to write a story
```
