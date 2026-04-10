# mainline Product Spec

## One Sentence

`mainline` stops parallel coding agents and worktrees from clobbering protected
`main` by turning local branch landing into a serialized, durable,
machine-readable queue.

## Product Boundary

`mainline` is a local coordinator for a protected Git branch.

It owns:

- submission of work to a durable queue
- serialized integration onto the protected branch
- publish coordination to the remote
- queue state, events, retries, operator controls, and health reporting

It does not own:

- arbitrary Git authoring workflows
- CI systems
- hosted merge queues
- semantic conflict resolution
- every Git operation a factory might perform outside protected-branch landing

## Primary Use Cases

### Humans With Many Worktrees

- keep the root checkout of `main` clean
- do feature work in topic worktrees
- land through `mq`, not through manual merge/push on `main`

### Many Local Coding Agents

- give each agent a worktree
- let agents submit completed work without touching protected `main`
- keep queue state durable and inspectable

### Factory / Supervisor Systems

- submit work from worktrees or durable refs
- wait on structured lifecycle outcomes
- inspect machine-readable status, events, health, and logs
- let one daemon own integration and publish for many repos

### Potential Embedded Go Consumers

If `mainline` becomes a library, the supported use case is:

- embedding coordinator semantics inside a larger Go system
- using typed `Submit`, `Wait`, `Status`, `Events`, `Retry`, `Cancel`, and
  `Doctor` flows
- avoiding subprocess boundaries for Go-based factory supervisors

That library use case is about orchestration. It is not about replacing Git
itself.

## Core Model

The durable model is:

- one protected branch per repo
- one integration queue per repo
- one publish queue per repo
- one repo-local SQLite state store
- per-repo advisory locking
- one canonical root checkout that humans inspect
- many disposable feature worktrees where changes are authored

Submission writes queue state first. Draining is opportunistic:

1. `mq submit` records the submission durably
2. `mq submit` may try to drain if no worker currently holds the lock
3. otherwise an existing worker or `mainlined` drains it later

This keeps submit cheap and reliable while still giving low latency when the
queue is idle.

## Non-Negotiable Invariants

- protected `main` must stay clean before and after any `mainline` action
- `mainline` must not create commits on protected `main`
- conflicts belong to the submitted worktree or submitted ref, not protected
  `main`
- only one integration runs per repo at a time
- only one publish runs per repo at a time
- newer publish requests supersede older stale ones
- machine-readable output must remain versioned and documented
- the root checkout must be trustworthy enough for humans, wrappers, and
  supervisors to inspect

## Supported Operator Model

### Default Human / Agent Flow

From a feature worktree:

```bash
mq submit --check-only --json
mq submit --wait --timeout 15m --json
```

Or:

```bash
mq submit --json
mq wait --submission <id> --for landed --json --timeout 30m
```

### Controller Flow

```bash
mq land --json --timeout 30m
```

If a publish request fails because remote `main` advanced first, the supported
operator recovery path is:

```bash
mq retry --repo /path/to/protected-worktree --publish <id>
```

Before retrying the push, `mq` may fetch upstream and rebase the protected
branch onto the updated upstream tip when unpublished local commits replay
cleanly.

### Machine-Wide Daemon Flow

```bash
mainlined --all --json
```

With repo-local observation:

```bash
mq events --repo /path/to/root --follow --json --lifecycle
```

## Public Automation Surface

The public automation contract is:

- `mq` and `mainlined`
- the JSON shapes documented in `docs/JSON_CONTRACTS.md`

The public automation contract is not:

- internal Go structs
- `internal/` packages
- undocumented CLI fields

Compatibility expectations:

- additive JSON fields are allowed within a schema version
- removals, renames, or semantic breaks require a schema-version change
- tests must enforce the documented contract

## Current Product Constraints

These are current design constraints, not hidden bugs:

- one protected branch per repo
- CLI/daemon first; stable Go library surface is not yet committed
- native `git` remains the write path for workflow-critical operations such as
  rebase and push
- factories should currently integrate through the CLI/daemon contract, not by
  importing `internal/`

## Forward Product Direction

The next productization work is:

- stabilize the factory-facing JSON API further
- reduce dependence on live source worktrees for unattended flows
- add publish circuit breaking and better recovery tooling
- improve supervisor metrics and readiness
- harden the machine-global daemon model
- decide whether to expose a supported `pkg/` Go library surface

The forward execution plan for that work lives in `PLAN.md`.
