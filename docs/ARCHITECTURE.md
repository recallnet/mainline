# Architecture

`mainline` is a local coordinator, not a hosted service. The design goal is to keep Git as the source of truth for repository state while using SQLite to make queue and event state durable across crashes and restarts.

## Core Pieces

- `cmd/mainline`: full CLI entrypoint
- `cmd/mq`: short alias intended for daily use
- `cmd/mainlined`: polling worker loop for unattended operation
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

`run-once` and `mainlined` share the same worker path.

1. Acquire the integration lock.
2. Process the oldest queued submission if one exists.
3. Otherwise acquire the publish lock.
4. Publish the newest meaningful protected-branch tip.

Publish work is coalesced. When `interrupt_inflight` is enabled, a newer queued publish can preempt an older local in-flight push so the newest protected tip becomes the next publish target.

## Repository Layout Support

The discovery path supports both:

- standard repos with a worktree-local `.git`
- bare-clone storage with linked worktrees

For bare-clone layouts, the shared Git directory is treated as the durable anchor for the state DB and lease files.
