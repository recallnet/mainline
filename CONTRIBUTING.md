# Contributing

## Development Baseline

- Go 1.25 or newer
- run `make fmt`, `make lint`, `make test`, and `make build` before sending changes
- run `make install-hooks` once per clone so local commit and push gates mirror CI
- keep edits ASCII unless the file already requires Unicode

## Repo Workflow

- treat `main` as protected
- keep the repo root checkout clean on `main`; that is the canonical protected checkout humans inspect and wrappers build from
- do feature work in a dedicated topic worktree
- initialize new clones with `mq repo init`, commit `mainline.toml`, and run `./scripts/install-hooks.sh`
- most agents should finish from a topic worktree with `mq submit --check-only --json` and `mq submit --wait --timeout 15m --json`
- controller agents and daemons should prefer `mq land --json --timeout 30m` or one machine-global `mainlined --all --json`
- land through `mq` instead of manual merge-on-main when possible
- do not hand-edit SQLite state

## Docs Expectations

When the command surface changes, update:

- `README.md`
- `PLAN.md` or `SPEC.md` if the operating model changed
- flow or install docs under `docs/` if operator behavior changed
- `AGENTS.md` and repo-local skills if agent workflow changed
- `docs/JSON_CONTRACTS.md` plus contract tests if machine-readable output changed

## Testing

Prefer end-to-end command tests for user-facing workflow changes. This repo’s core promise is operational correctness across Git worktrees, state, and worker coordination, so command-level coverage matters more than isolated helper tests alone.
