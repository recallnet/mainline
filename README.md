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
- support for standard repos and bare-clone-plus-worktree layouts

## Current Status

Implemented milestones:

- Milestone 0: project skeleton
- Milestone 1: repository discovery and health
- Milestone 2: durable state and locking

Not implemented yet:

- branch submission
- integration worker
- publish worker
- daemon loop

The current CLI is useful for repo inspection and initialization, not for full
queue-driven integration yet.

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
- `mainlined`: daemon entrypoint placeholder

Examples:

```bash
mainline --help
mq --help
mainlined --help
```

Current repo commands:

```bash
mainline repo init --repo .
mainline repo show --repo .
mainline doctor --repo .
```

The same commands work through `mq`:

```bash
mq repo init --repo .
mq repo show --repo .
mq doctor --repo .
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

## Near-Term Roadmap

Next milestones:

1. branch submission
2. integration queue MVP
3. publish queue MVP
4. daemon mode

The project plan and spec live in:

- `PLAN.md`
- `SPEC.md`

## License

TBD
