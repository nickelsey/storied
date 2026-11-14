---
title: Switch mesh export to watertight repair pass
risk: medium
---

Reviewers: start with the repair pass, the rest follows from it.

## Why {#why author=human}

Exports from the Blender plugin occasionally produce non-manifold meshes.

### Background

A level-3 heading is prose, not a new section.

## Repair pass in the exporter {#repair-pass author=agent risk=high}
::anchor{path="plugins/blender/export.py" symbol="export_mesh"}
::anchor{path="core/mesh/repair.py"}
::anchor{path="core/mesh/repair.py" lines="40-72"}
::anchor{path="core/mesh/legacy.py" lines="10" side="old"}
::artifact{id="mesh-compare"}
::artifact{id="repair-notes"}

The exporter now calls `repair_watertight` before writing.

```markdown
## Not a section {#nope author=human}
::anchor{path="inside/a/code/fence.py"}
```

## Test fixtures {#fixtures author=agent risk=low}
::anchor{glob="tests/fixtures/meshes/**" skim}
::anchor{glob="tests/export/{test_repair,test_export}.py"}
::anchor{hunk="3fa9c21"}
::anchor{commit="2222222222222222222222222222222222222222"}
::artifact{id="export-tests"}
