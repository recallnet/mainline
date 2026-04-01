# Architecture

`mainline` is a local coordinator, not a hosted service. The design goal is to keep Git as the source of truth for repository state while using SQLite to make queue and event state durable across crashes and restarts.

## Core Pieces

- `cmd/mainline`: full CLI entrypoint
- `cmd/mq`: short alias intended for daily use
- `cmd/mainlined`: optional long-lived host for the same drain loop used by `mq`
- `internal/app`: command handlers and operator-facing output
- `internal/git`: repository discovery, `go-git` inspection, and thin native-`git` execution where library support is missing
- `internal/policy`: persisted `mainline.toml` config
- `internal/state`: SQLite store plus file-based lease metadata

## Data Model

Durable state lives under shared Git storage so all worktrees in one repository see the same queue:

- `repositories`: canonical repo identity and protected-branch config
- `integration_submissions`: queued topic branches
- `publish_requests`: queued or completed publish work
- `events`: append-only operator-visible history

This split is deliberate:

- Git answers branch topology, branch heads, worktree membership, rebase, merge, and push semantics.
- SQLite answers queue ordering, worker coordination, audit trail, and restart recovery.

## Worker Model

The important design point is that queue progress does not depend on a standing
daemon.

The drain loop is shared across:

- `mq submit`
- `mq land`
- `mq publish`
- `mq retry`
- `mq cancel`
- `mq run-once`
- `mainlined`

That means a normal repo can run daemonless and still make forward progress.
Mutating commands queue work first, then opportunistically try to become the
repo worker themselves. If another process already holds the repo lock, they
exit cleanly and let that worker continue draining.

The per-repo drain loop is:

1. Acquire the integration lock.
2. Process the next runnable integration submission if one exists.
3. Otherwise acquire the publish lock.
4. Publish the newest meaningful protected-branch tip.
5. If delayed work is scheduled, sleep until it becomes runnable and continue.
6. Exit only when the repo is truly quiescent.

Publish work is coalesced. When `interrupt_inflight` is enabled, a newer queued
publish can preempt an older local in-flight push so the newest protected tip
becomes the next publish target.

`mainlined` is therefore an optional deployment mode, not a correctness
dependency. Its job is to host the same drain loop as a long-lived helper for
many repos, not to own special queue semantics that `mq` lacks.

## Repository Layout Support

The discovery path supports both:

- standard repos with a worktree-local `.git`
- bare-clone storage with linked worktrees

For bare-clone layouts, the shared Git directory is treated as the durable anchor for the state DB and lease files.
