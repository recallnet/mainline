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

`make certify-matrix` builds and uses the local `./bin/mq` so certification
matches the code in this checkout, not whatever global `mq` happens to be on
the machine.

If you need to keep the disposable clones for debugging, pass
`--keep-workdirs` to the runner directly. Those preserved workdirs stay outside
the repo tree by default so they do not pollute `go test ./...`.
