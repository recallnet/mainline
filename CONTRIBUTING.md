# Contributing

## First-Time Orientation

There are two different jobs people do in this repo. Keep them separate:

- Developing `mainline` itself
  - you are changing the `mainline` codebase in this checkout
  - the local baseline is `make build`, `go test ./...`, and CLI help / repo-root inspection
  - `mainline.toml` in this repo is committed policy, not proof that a fresh clone is already initialized as a managed queue target
- Using `mainline` on some other repo
  - you are in a target repo that `mq` should manage
  - that repo needs its own `mq repo init`, committed `mainline.toml`, and repo-local state under `.git/mainline/`

For a fresh clone of this source repo, start with:

```bash
make build
go test ./...
./bin/mq --help
./bin/mq repo show --repo . --json
./bin/mq repo root --repo . --json
./bin/mq status --repo . --json
```

Interpretation:

- build should succeed
- tests should succeed even if your normal global Git config enforces commit signing; the test helpers neutralize global signing for temp repos
- `mq repo root --repo . --json` tells you whether the current checkout is the canonical protected root checkout
- `mq status --repo . --json` may fail in a fresh source clone until this repo is explicitly initialized as a managed target repo on that machine

If you want the repo-local orientation flow encoded for an agent, use the
onboarding skill at
[.agents/skills/onboarding/SKILL.md](/Users/devrel/Projects/recallnet/mainline/.agents/skills/onboarding/SKILL.md).
Use that before the worktree skill. The onboarding skill is for first-pass
setup and terminology; the worktree skill is for the contribution flow after
that baseline is already understood.

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
- controller agents should prefer `mq land --json --timeout 30m`
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
