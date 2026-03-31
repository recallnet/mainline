# mainline

`mainline` is a local-first branch coordinator for Git repositories with a
protected local branch, usually `main`.

It is built for worktree-heavy development, agent-heavy local workflows, and
machines where keeping `main` boring matters more than cleverness.

## What It Does

`mainline` is aiming at a simple operating model:

- topic branches do work
- a protected branch stays clean
- integrations are serialized
- publishes are coalesced so the newest tip wins
- queue truth survives crashes and restarts

Today the repo implements the foundation for that model:

- project skeleton and release/build scaffolding
- repository discovery and health inspection
- repo config persistence
- durable SQLite state
- per-repo integration and publish locks
- branch submission into durable queue state
- ordered single-repo integration with `run-once`
- coalesced latest-tip publish queue with `publish` and `run-once`
- polling daemon mode with `mainlined`
- support for standard repos and bare-clone-plus-worktree layouts

## Current Status

Implemented milestones:

- Milestone 0: project skeleton
- Milestone 1: repository discovery and health
- Milestone 2: durable state and locking
- Milestone 3: branch submission
- Milestone 4: integration queue MVP
- Milestone 5: publish queue MVP
- Milestone 6: daemon mode

The current CLI can initialize a repo, inspect health, queue clean topic
branches, run one serialized integration cycle locally, queue manual publish
requests, push the latest protected-branch tip through the coalesced publish
queue, or run a polling background loop through `mainlined`.

## Why This Exists

Modern local Git workflows break down under parallelism:

- many worktrees drift against each other
- `main` turns into a conflict scratchpad
- direct pushes race
- stale publishes waste time
- there is no durable local record of what was queued or why something blocked

`mainline` exists to make that workflow explicit, inspectable, and restart-safe
without inventing a new VCS model.

## Supported Repository Layouts

`mainline` is designed to handle both:

- standard repositories with `.git/` in the checked-out worktree
- bare clone storage with linked worktrees, such as:
  - shared repo storage at `~/Projects/.bare/owner/repo.git`
  - checked-out worktree at `~/Projects/owner/repo`

For bare-clone layouts, durable state and locks are stored with the shared Git
storage so every worktree sees the same queue truth.

## CLI

Supported binaries:

- `mainline`: full CLI name
- `mq`: short handle for the main queue CLI
- `mainlined`: daemon entrypoint

Examples:

```bash
mainline --help
mq --help
mainlined --help
```

Example daemon usage:

```bash
mainlined --repo /path/to/repo
mainlined --repo /path/to/repo --interval 2s --json
```

Current repo commands:

```bash
mainline repo init --repo .
mainline repo show --repo .
mainline doctor --repo .
mainline submit --repo /path/to/feature-worktree
mainline submit --repo /path/to/repo --branch fix-login --worktree /path/to/feature-worktree
mainline run-once --repo /path/to/repo
mainline publish --repo /path/to/repo
```

The same commands work through `mq`:

```bash
mq repo init --repo .
mq repo show --repo .
mq doctor --repo .
mq submit --repo /path/to/feature-worktree
mq run-once --repo /path/to/repo
mq publish --repo /path/to/repo
```

## Build

Requires Go 1.25 or newer.

```bash
git clone git@github.com:recallnet/mainline.git
cd mainline
make build
```

Binaries are written to `./bin`:

- `./bin/mainline`
- `./bin/mq`
- `./bin/mainlined`

## Development

Common tasks:

```bash
make fmt
make lint
make test
make build
```

Verification currently used by CI:

- `gofmt -w`
- `go vet ./...`
- `go test ./...`
- build all binaries

## Durable State

Milestone 2 adds a repo-local SQLite state store and file-lock exclusivity.

Current durable entities:

- repositories
- integration submissions
- publish requests
- events

Current submission behavior:

- submits the checked-out branch by default
- allows explicit `--branch` and `--worktree`
- rejects protected-branch submits
- rejects dirty source worktrees
- rejects detached HEAD without an explicit branch worktree
- stores submission metadata and emits a `submission.created` event

Current integration behavior:

- processes the oldest queued submission first
- acquires a per-repo integration lock
- validates a clean protected-branch worktree before integrating
- rebases the submitted branch in its source worktree
- fast-forwards the protected branch on success
- marks rebase conflicts as `blocked` without touching the protected branch
- marks stale or invalid submissions as `failed` with actionable error text
- emits publish requests automatically when `[publish].mode = "auto"`

Current publish behavior:

- queues the current protected-branch tip with `publish`
- processes publish work through the per-repo publish lock
- supersedes older queued publish requests before pushing
- pushes only the latest queued protected-branch tip
- marks publish requests `succeeded`, `failed`, or `superseded`
- lets `run-once` drain publish work when no integration submission is waiting

Current daemon behavior:

- polls the repo on a configurable interval
- reuses the real `run-once` worker path instead of a separate codepath
- can emit structured JSON logs
- exits cleanly on `SIGINT` or `SIGTERM`
- preserves queue truth across restarts because durable state remains in SQLite

Current lock domains:

- integration
- publish

The state path is derived from shared Git storage, not from whichever worktree
happened to invoke the command.

## Architecture

Key packages:

- `cmd/mainline`: main CLI
- `cmd/mq`: short CLI alias
- `cmd/mainlined`: daemon entrypoint
- `internal/app`: command wiring
- `internal/git`: repository discovery and health inspection
- `internal/policy`: repo config types and persistence
- `internal/state`: SQLite state and per-repo locks
- `internal/queue`: queue boundary
- `internal/worker`: worker boundary

## Design Direction

Implementation principles:

- use mature Go libraries where they fit the product model
- use `go-git` as the default repository engine for inspection and config
- preserve real Git semantics instead of inventing custom repo metadata
- use native `git` only where the library surface is not sufficient for the
  required workflow, with the integration rebase path expected to be one of
  those cases

The project plan and spec live in:

- `PLAN.md`
- `SPEC.md`

## License

TBD
