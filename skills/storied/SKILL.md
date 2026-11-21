---
name: storied
description: Write or update the review story for a branch with the storied CLI. Use when asked to prepare a PR for review, write its story or review bundle, or bring an existing story up to date after new commits.
---

# Writing a review story with storied

A story explains a change in the order a reviewer should read it. Each
section says what one logical step does and points at the exact diff blocks
that carry it out. Reviewers read the story beside those diffs; anything no
section points at is shown to them as unreviewed.

The bundle lives in `.storied/` at the repository root and is not committed
to the branch:

| File | What it holds |
| --- | --- |
| `story.md` | the sections, in reading order |
| `manifest.yaml` | base and head commits, artifacts and where they came from |
| `agent.md` | notes for a reviewer's agent |
| `artifacts/` | evidence the story embeds: logs, images, reports |

## The loop

1. `storied init` if `.storied/` does not exist. Pass `--base <branch>` when
   the branch does not target the default branch.
2. `storied draft --json`. Read `blocks`: every change block of the diff with
   its `id`, `path`, `line`, nearest function (`header`), changed `lines`,
   and the `section` that already explains it, if any.
3. Decide the sections (see below) and write them in `story.md`, attaching
   each block with `::anchor{hunk="<id>"}`.
4. `storied lint`. Fix every error. Read every warning; "Not in the story"
   lists the blocks still unattached, with their ids.
5. Repeat 3 and 4 until lint reports no errors and nothing is left under
   "Not in the story" that a reviewer needs explained.
6. `storied render` and read `.storied/story.html` once, top to bottom, as
   the reviewer will.

After new commits or a rebase, run `storied draft` again. It updates the
base and head, and prints a `check:` line for each section that lost a
block. Re-attach those blocks or rewrite the section; do not start over.

## Deciding the sections

Group by what the change does, not by file or directory. One section is one
step a reviewer can hold in their head: "add the helper", "switch the callers
over", "delete the old path", "tests". A step that touches eight files is
still one section; a file that takes part in two steps appears in both.

Order the sections so each one only relies on those before it: groundwork,
then the main change, then the call sites that use it, then tests, then
mechanical fallout.

Within a section, list the anchors in the order the prose walks through
them. The page shows the code in that order, so a section can go from the
new function in one file to its caller in another and back to the first
file for the cleanup.

Keep a section under about 400 changed lines. If it is larger, it is
probably two steps.

## story.md

```markdown
---
title: Share one helper for abbreviating commit SHAs
risk: low
---

## Why {#why author=human}

Two packages each had their own copy of the same function.

## The helper {#helper author=agent}
::anchor{hunk="3fa9c21"}
::anchor{hunk="a07be44"}

`gitx.Short` returns the first nine characters of a full commit id and
leaves anything else, such as a branch name, unchanged.

## Callers switched over {#callers author=agent risk=medium}
::anchor{hunk="108c261"}
::anchor{hunk="b2af964"}
::artifact{id="unit-tests"}

Both copies are deleted and their callers use the shared one.
```

Rules the parser enforces:

- The file starts with frontmatter holding `title`, and optionally `risk`
  (`low`, `medium` or `high`), which sections inherit.
- A section starts at a `## Title {#id author=agent}` heading. The id is
  lower-case letters, digits and hyphens, and unique. Use `author=agent` for
  what you write. Never change a section marked `author=human` unless asked.
- Directives are whole lines directly under the heading:
  `::anchor{...}` and `::artifact{id="..."}`.
- A section without anchors is fine for context such as "Why".

Anchors, from most to least specific:

| Anchor | Claims |
| --- | --- |
| `::anchor{hunk="<id>"}` | one change block. Use this by default. |
| `::anchor{path="p" lines="a-b"}` | changed lines in a range of the new file. Goes stale on the next push; avoid. |
| `::anchor{path="p"}` | every change in a file. Use for a new file that is one step. |
| `::anchor{glob="dir/**"}` | every change in matching files |
| `::anchor{glob="*.lock" skim}` | the same, shown collapsed. Use for lockfiles, generated and vendored files. |

When two sections claim the same lines, the more specific anchor decides
where they are shown.

## Writing the prose

- Describe what the diff does, not what you intended. The reviewer checks
  your words against the code beside them.
- Say what changed in behaviour and why this way. Do not narrate the diff
  line by line.
- Name anything a reviewer should look at hard: an invariant, an edge case
  you are unsure of, a deliberate omission.
- Two to six sentences per section is usual.
- Do not claim something was tested unless an artifact shows it.

## Artifacts

Put evidence in `.storied/artifacts/`, list it in `manifest.yaml`, and embed
it in the section it supports with `::artifact{id="..."}`.

```yaml
artifacts:
  - id: unit-tests
    kind: log            # markdown, image, image-compare, log, test-report,
    path: artifacts/tests.log   # k8s-diff, vega-lite, gltf-compare
    provenance: { class: author, command: "go test ./..." }
```

`provenance.class` says where the file came from: `ci` (needs `run_url`),
`gpu-job` (needs `job_id`), `author` (your machine), or `reviewer-agent`.
State it truthfully; reviewers see it beside the artifact.
`image-compare` takes `before` and `after` instead of `path`.

## agent.md

Notes for the reviewer's agent, which treats them as claims to check:

- the exact commands that run the relevant tests
- environment needs: headless rendering, Mesa, GPU or cluster access, large
  fixtures
- known gaps: what was not tested, what is stubbed

## Before you finish

- `storied lint` reports 0 errors.
- No block a reviewer needs explained is left under "Not in the story".
- Every section with anchors has prose.
- `storied pack` when the PR exists and the author asks for the bundle to be
  published.
