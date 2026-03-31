# Contributing

## Development Baseline

- Go 1.25 or newer
- run `make fmt`, `make lint`, `make test`, and `make build` before sending changes
- run `make install-hooks` once per clone so local commit and push gates mirror CI
- keep edits ASCII unless the file already requires Unicode

## Repo Workflow

- treat `main` as protected
- do feature work in a dedicated topic worktree
- land through `mq submit` and `mq run-once` instead of manual merge-on-main when possible
- do not hand-edit SQLite state

## Docs Expectations

When the command surface changes, update:

- `README.md`
- `PLAN.md` or `SPEC.md` if the operating model changed
- flow or install docs under `docs/` if operator behavior changed

## Testing

Prefer end-to-end command tests for user-facing workflow changes. This repo’s core promise is operational correctness across Git worktrees, state, and worker coordination, so command-level coverage matters more than isolated helper tests alone.
