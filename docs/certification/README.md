# Certification

`mainline` does not get to claim "real repo" confidence from temporary toy repos.

This directory holds the committed certification matrix and the latest local
report generated from disposable mirrors of real sibling repos on this
machine.

Current matrix classes:

- agent-heavy dogfood repo
- hook-heavy regular repo
- hook-heavy bare-clone-plus-worktree repo

Run it with:

```bash
make certify-matrix
```

The runner clones each source repo into disposable local mirrors, applies the
repo-specific `mainline` policy defaults required for safe adoption, performs
two real `mq submit` + `mq run-once` + `mq publish` cycles, and records the
result in `docs/certification/latest-report.json`.
