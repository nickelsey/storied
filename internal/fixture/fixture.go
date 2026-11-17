// Package fixture builds a small repository with a review bundle, shared by
// the tests of the packages that work on a whole bundle.
package fixture

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nickelsey/storied/internal/gittest"
)

// Fixture is a repository with a base commit, two commits on top of it, and
// a bundle in .storied describing them.
type Fixture struct {
	Repo       *gittest.Repo
	Base, Head string // full SHAs
	Bundle     string // the bundle directory
	t          testing.TB
}

const repairBase = `"""Mesh repair helpers."""

import numpy as np


def weld(vertices, tol=1e-6):
    """Merge vertices closer than tol."""
    keys = np.round(vertices / tol)
    return np.unique(keys, axis=0) * tol


def fill_holes(mesh):
    for loop in mesh.boundary_loops():
        mesh.add_face(loop)
    return mesh


def is_watertight(mesh):
    return not mesh.boundary_loops()
`

const repairHead = `"""Mesh repair helpers."""

import numpy as np


def weld(vertices, tol=1e-6):
    """Merge vertices closer than tol."""
    keys = np.round(vertices / tol).astype(np.int64)
    _, index = np.unique(keys, axis=0, return_index=True)
    return vertices[np.sort(index)]


def fill_holes(mesh):
    for loop in mesh.boundary_loops():
        mesh.add_face(loop)
    return mesh


def is_watertight(mesh):
    return not mesh.boundary_loops()


def repair_watertight(mesh, tol=1e-6):
    """Weld, then fill holes, until the mesh is closed."""
    mesh.vertices = weld(mesh.vertices, tol)
    if not is_watertight(mesh):
        fill_holes(mesh)
    return mesh
`

const exportBase = `from core.mesh import io


def export_mesh(obj, path):
    mesh = io.from_blender(obj)
    io.write(mesh, path)
`

const exportHead = `from core.mesh import io
from core.mesh.repair import repair_watertight


def export_mesh(obj, path, repair=True):
    mesh = io.from_blender(obj)
    if repair:
        mesh = repair_watertight(mesh)  # <closes> holes & welds
    io.write(mesh, path)
`

const legacy = `def close_mesh(mesh):
    # Old single-pass repair.
    mesh.fill()
    return mesh

LEGACY = True
`

const testRepair = `from core.mesh.repair import repair_watertight


def test_repair_closes_cube(open_cube):
    assert repair_watertight(open_cube).boundary_loops() == []
`

// Story is the fixture's story.md. It leaves the README, the logo, the
// renamed doc, the mode change and part of legacy.py unexplained, and has
// one stale and one unsupported anchor.
const Story = `---
title: Switch mesh export to watertight repair pass
risk: medium
---

Start with the repair pass; the rest follows from it.

## Why {#why author=human}

Exports from the Blender plugin sometimes produce **non-manifold** meshes.
See [the issue](https://example.com/issues/12) and <b>this raw HTML</b>.

## Repair pass in the exporter {#repair-pass author=agent risk=high}
::anchor{path="core/mesh/repair.py" lines="21-28"}
::anchor{path="plugins/blender/export.py"}
::anchor{hunk="b2af964"}
::anchor{path="plugins/blender/export.py" symbol="export_mesh"}
::artifact{id="mesh-compare"}
::artifact{id="repair-notes"}
::artifact{id="drag-chart"}

The exporter now calls ` + "`repair_watertight`" + ` before writing:

- weld
- fill holes

## Welding keeps vertex order {#weld author=human}
::anchor{path="core/mesh/repair.py"}
::anchor{path="core/mesh/legacy.py" lines="1-4" side="old"}

` + "`weld`" + ` used to return sorted, rounded copies.

## Tests and fixtures {#tests author=agent risk=low}
::anchor{glob="tests/**/*.py"}
::anchor{glob="tests/fixtures/**" skim}
::anchor{glob="*.lock" skim}
::anchor{path="core/mesh/gone.py"}
::artifact{id="export-tests"}
`

const manifest = `schema: storied/v1
repo: org/cad-integration
pr: 482
base_ref: main
base_sha: BASE
head_sha: HEAD
artifacts:
  - id: mesh-compare
    kind: image-compare
    before: artifacts/mesh-before.png
    after: artifacts/mesh-after.png
    provenance:
      class: ci
      run_url: https://github.com/org/cad-integration/actions/runs/1
      sha: HEAD
  - id: repair-notes
    kind: markdown
    path: artifacts/notes.md
    provenance: { class: author }
  - id: drag-chart
    kind: vega-lite
    path: artifacts/drag.vl.json
    provenance: { class: gpu-job, job_id: slurm-88121 }
  - id: export-tests
    kind: log
    path: artifacts/pytest.log
    provenance: { class: reviewer-agent, command: "pytest tests/export -q" }
`

// A 1x1 PNG.
const pngBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

// New builds the fixture.
func New(t testing.TB) *Fixture {
	t.Helper()
	r := gittest.New(t)
	png, err := base64.StdEncoding.DecodeString(pngBase64)
	if err != nil {
		t.Fatal(err)
	}

	r.Write("README.md", "# cad\n\nMesh tools.\n")
	r.Write("core/mesh/repair.py", repairBase)
	r.Write("core/mesh/legacy.py", legacy)
	r.Write("plugins/blender/export.py", exportBase)
	r.Write("tests/fixtures/meshes/cube.obj", "v 0 0 0\nv 1 0 0\n")
	r.Write("deps.lock", "numpy==2.1.0\n")
	r.Write("docs/old-name.md", "Notes.\n")
	r.Write("tools/run.sh", "#!/bin/sh\nexec pytest\n")
	r.Write("logo.png", "\x89PNG\x00\x01")
	base := r.Commit("base")

	r.Git("checkout", "-q", "-b", "repair")
	r.Write("core/mesh/repair.py", repairHead)
	r.Remove("core/mesh/legacy.py")
	r.Write("plugins/blender/export.py", exportHead)
	r.Commit("add repair pass")

	r.Write("README.md", "# cad\n\nMesh tools, with repair.\n")
	r.Write("tests/export/test_repair.py", testRepair)
	r.Write("tests/fixtures/meshes/cube.obj", "v 0 0 0\nv 1 0 0\nv 1 1 0\n")
	r.Write("deps.lock", "numpy==2.2.0\n")
	r.Git("mv", "docs/old-name.md", "docs/new-name.md")
	r.Write("logo.png", "\x89PNG\x00\x02")
	r.Git("add", "-A")
	r.Git("update-index", "--chmod=+x", "tools/run.sh")
	// Match the worktree to the index so the repository is clean where
	// file modes are tracked. This does nothing on Windows.
	if err := os.Chmod(filepath.Join(r.Dir, "tools", "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	head := r.CommitIndex("tests, docs and housekeeping")

	f := &Fixture{Repo: r, Base: base, Head: head, Bundle: filepath.Join(r.Dir, ".storied"), t: t}
	f.WriteBundle("manifest.yaml", strings.NewReplacer("BASE", base, "HEAD", head).Replace(manifest))
	f.WriteBundle("story.md", Story)
	f.WriteBundle("agent.md", "Run `pytest tests/export -q`. Rendering needs Mesa when headless.\n")
	f.WriteBundle("artifacts/notes.md", "Repair is **idempotent**: a second pass changes nothing.\n")
	f.WriteBundle("artifacts/mesh-before.png", string(png))
	f.WriteBundle("artifacts/mesh-after.png", string(png))
	f.WriteBundle("artifacts/drag.vl.json", "{\"mark\": \"line\"}\n")
	f.WriteBundle("artifacts/pytest.log", "1 passed in 0.12s\n")
	return f
}

// WriteBundle creates or replaces a file in the bundle. The path uses
// forward slashes.
func (f *Fixture) WriteBundle(path, content string) {
	f.t.Helper()
	full := filepath.Join(f.Bundle, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// EditBundle replaces old with new once in a bundle file.
func (f *Fixture) EditBundle(path, old, new string) {
	f.t.Helper()
	full := filepath.Join(f.Bundle, filepath.FromSlash(path))
	data, err := os.ReadFile(full)
	if err != nil {
		f.t.Fatal(err)
	}
	if !strings.Contains(string(data), old) {
		f.t.Fatalf("%s does not contain %q", path, old)
	}
	if err := os.WriteFile(full, []byte(strings.Replace(string(data), old, new, 1)), 0o644); err != nil {
		f.t.Fatal(err)
	}
}
