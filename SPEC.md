# mainline Spec

## Summary

`mainline` is a local branch coordinator for Git repositories with a protected local branch, usually `main`.

It exists to make a previously informal workflow safe and repeatable:

- all commits happen on topic branches or worktrees
- the protected branch stays clean
- integrations into the protected branch are serialized
- rebases from upstream happen in one controlled place
- pushes are coalesced so only the latest publish matters

`mainline` is designed for:

- solo developers who want a safer `main`
- power users working with many local worktrees
- agent-heavy environments with 5-20 parallel coding sessions
- small teams who want local-first branch protection before CI or merge queues

`mainline` is not a hosted merge queue. It is a local coordinator that sits on top of normal Git repositories and remote providers.

## Problem

Modern local Git workflows increasingly look like this:

- a protected local `main` worktree exists
- many feature branches are created as worktrees
- each worktree rebases from local `main`
- completed work is fast-forward merged into local `main`
- local `main` is then pushed to `origin/main`

This breaks down under parallelism.

Typical failure modes:

- `main` becomes a scratchpad for manual merges or rebases
- multiple agents or humans push concurrently
- stale pushes waste time and network
- upstream drift gets handled ad hoc in random shells
- conflicts end up being resolved in the wrong place
- failed integrations leave the protected branch dirty
- there is no audit trail for why a branch was integrated or skipped

The result is a protected branch that is not actually protected.

## Goals

- Keep the protected branch clean at all times.
- Ensure all commits are created on worktree branches, not on the protected branch.
- Serialize branch integration into the protected branch.
- Resolve branch-specific merge and rebase failures in the originating worktree by default.
- Coalesce publish requests so only the latest protected-branch tip is pushed.
- Support automatic upstream synchronization inside the queue.
- Provide a durable local queue with inspectable state.
- Preserve real Git semantics while leaning on mature Go libraries instead of reimplementing repository plumbing.
- Feel trustworthy enough for open-source adoption.

## Non-Goals

- Replace Git hosting providers.
- Replace CI systems or hosted merge queues.
- Invent a new merge strategy.
- Automatically resolve semantic conflicts.
- Manage repository-specific build systems beyond executing configured checks.
- Require daemon-only operation; daemon mode is supported, not mandatory.

## Product Positioning

`mainline` is a local-first branch protection and integration coordinator.

It should be understandable in one sentence:

> `mainline` keeps your local `main` boring while parallel branches land, sync, and publish through a safe queue.

The default mental model is:

- Git remains the source of truth.
- `mainline` owns policy and ordering.
- users and agents own topic branches.

## Terminology

- Protected branch: the branch `mainline` manages, usually `main`.
- Integration branch: a topic branch submitted for landing.
- Main worktree: the checkout whose current branch is the protected branch.
- Worktree branch: a branch checked out in a separate worktree.
- Integrate queue: ordered queue of branch landing requests.
- Publish queue: coalesced queue of remote publish requests.
- Superseded publish: a publish request made obsolete by a newer protected-branch tip.
- Policy: configured rules for sync, checks, branch cleanliness, and publish behavior.

## Design Principles

### 1. Protected Branch Must Stay Boring

The protected branch should be:

- checked out in a canonical worktree
- clean before and after every `mainline` operation
- free of ad hoc commits
- free of unresolved merge state
- the only branch `mainline` publishes

### 2. Conflicts Belong to Topic Branches

When a submitted branch cannot land cleanly:

- do not resolve on the protected branch
- do not leave merge state in the main worktree
- report the branch as blocked
- require resolution in the originating worktree by default

### 3. Newest Publish Wins

Publishing is freshness-sensitive, not fairness-sensitive.

If three publishes are requested for protected-branch tips `A`, `B`, and `C`, only `C` matters if it is the current tip by the time publish occurs.

### 4. Prefer Proven Libraries

`mainline` should preserve normal Git refs, worktrees, and branch semantics without inventing a custom repository model.

Implementation preference:

- prefer mature off-the-shelf Go libraries over custom plumbing
- use `go-git` as the default repository engine unless a specific Git behavior is unsupported or incorrect for the required workflow
- keep any fallback glue narrow and clearly justified

`mainline` should never require a repository to adopt a custom branch model or metadata format to function.

### 5. Recoverability Beats Cleverness

Every queue action should be visible, inspectable, and restartable. If a daemon dies, the state should be recoverable from durable local storage and the Git repository.

## User Stories

### Solo Developer

As a developer with several worktrees, I want to land branches onto local `main` without manually thinking about origin drift, branch cleanliness, or push duplication.

### Agent Supervisor

As an operator supervising many agent worktrees, I want agents to submit completed branches and let a local coordinator serialize integrations and publishing safely.

### Small Team

As a small team sharing conventions, we want a reproducible local branch queue that reduces accidental bad pushes and keeps `main` clean before CI even runs.

### Self-Hosting Maintainer

As a maintainer of `mainline` itself, I want the repo to ship a canonical worktree-first `mq` workflow so agents and humans dogfood the same path the product asks other repos to trust.

## Core Invariants

The following are hard invariants:

- The protected branch worktree must be clean before any queue action begins.
- The protected branch worktree must be clean after any queue action ends.
- `mainline` must not create commits on the protected branch.
- `mainline` must not leave the protected branch in merge, rebase, cherry-pick, or detached HEAD state.
- Only one integration operation may run per repository at a time.
- Only one publish operation may run per repository at a time.
- A publish request for an older protected-branch tip may be discarded if a newer tip exists.
- Any integration failure that requires manual conflict resolution must leave the protected branch untouched and mark the submitted branch blocked.

## Functional Scope

### Self-Hosting Workflow

`mainline` should be able to manage this repository using its own CLI flow.

Expected self-hosting behavior:

- feature work happens in dedicated topic worktrees
- all commits are made from those topic worktrees
- landing happens through `mq submit`, `mq run-once`, and `mq publish` instead of manual merge into `main`
- the repository may ship repo-local agent skills that codify that workflow, but those skills must match the actual command surface and current product behavior

The self-hosting path is not separate from the product. It is a trust test for whether the operator model is clear enough to use on a real worktree-heavy repo.

### Branch Submission

Users or agents can submit a worktree branch for landing.

Submission records:

- repository identity
- protected branch name
- topic branch name
- source worktree path
- source HEAD SHA
- submitter metadata
- optional requested checks or policy overrides
- submission timestamp

Submission preconditions:

- source worktree exists
- source worktree is clean
- source branch is not the protected branch
- source branch has at least one commit
- protected branch worktree is available

### Integration Queue

The integration queue is ordered and fairness-sensitive.

Default behavior:

1. acquire repo integration lock
2. verify protected branch worktree is clean
3. fetch remote state if policy requires
4. sync protected branch with upstream if needed
5. verify submission is still current
6. run configured checks
7. integrate branch onto protected branch
8. fast-forward protected branch ref if landing succeeds
9. enqueue publish if publish policy requires
10. release lock

Supported integration modes:

- fast-forward only
- rebase branch onto protected branch, then fast-forward
- squash-to-branch is explicitly out of scope for MVP

Default MVP mode should be:

- rebase topic branch onto current protected branch in the topic worktree
- fast-forward protected branch to that rebased topic branch

Reason:

- conflict resolution stays in the topic worktree
- protected branch stays clean
- result is linear and predictable

### Upstream Sync

`mainline` should support syncing the protected branch from `origin/<protected>` inside the queue.

Policy options:

- `manual`: never auto-sync; fail if protected branch is behind upstream
- `sync-before-integrate`: fetch and fast-forward protected branch before attempting any integration
- `sync-before-publish`: integrate locally first, then sync before publish if safe

Recommended default:

- `sync-before-integrate`

Rules:

- if protected branch can fast-forward from upstream, do so in the queue
- if protected branch has diverged from upstream, mark the repository blocked for manual intervention
- never perform history-rewriting updates on the protected branch automatically

### Conflict Handling

When a branch cannot rebase or land cleanly:

- mark queue item `blocked`
- record the Git failure and relevant refs
- leave protected branch unchanged
- leave integration branch worktree as the place for resolution

MVP default behavior:

- attempt rebase in the source worktree
- on conflict, stop and report instructions
- do not auto-abort if preserving user context is more useful
- do not continue queue processing for that item until explicit retry

### Publish Queue

The publish queue is coalesced and latest-tip-wins.

Behavior:

- publish requests are keyed by repository and protected branch
- only the latest protected-branch tip is publishable
- if a newer protected-branch tip appears while publish is pending, older publish requests are marked superseded
- if a newer tip appears while a push is in flight, the running push may be interrupted locally if safe, then restarted for the newest tip
- if an old push finishes successfully before interruption, a new publish is still scheduled for the new tip

Important constraint:

`mainline` cannot guarantee remote-side cancellation for a push that has already completed. It guarantees that the newest local protected-branch tip will be the next publish target.

### Policy Checks

`mainline` should support repository-defined checks before integration or publish.

MVP checks:

- worktree clean
- protected branch worktree clean
- branch not already integrated
- branch HEAD still matches submitted SHA unless `--allow-newer-head` is set

Phase 2 checks:

- arbitrary shell command hooks
- branch-local test commands
- upstream freshness policy

### Status and Auditability

Users need inspectable state.

Required status views:

- queue summary
- active integration
- active publish
- blocked items
- recent events
- repository health

Important event types:

- submission created
- submission started
- upstream sync started/completed/failed
- checks started/completed/failed
- integration succeeded/blocked/failed
- publish requested
- publish superseded
- publish started/completed/failed

## Architecture

## Components

### `mainline` CLI

The primary user-facing executable.

Responsibilities:

- accept submissions
- inspect queue state
- run one-shot workers
- configure repositories
- expose debugging tools

### `mainlined` Daemon

Optional background worker.

Responsibilities:

- watch for new queue items
- execute integration and publish workers
- own long-running queue loops
- emit logs and metrics

The daemon should not be required for basic use. Every operation should also have a CLI-triggered path.

### Git Engine

A thin wrapper around `go-git` plus narrowly scoped `mainline` adapters where needed.

Responsibilities:

- repository discovery
- worktree inspection
- branch/ref inspection
- fetch, rebase, fast-forward, push
- translate library behavior into `mainline` health, queue, and policy results

Implementation note:

- `go-git` is the default engine for repository inspection, config handling, and ordinary ref/worktree operations
- `go-git` can represent rebase-related config, but the actual integration rebase step should be expected to use native `git rebase` until a first-class library implementation exists and proves trustworthy

### Queue Engine

Owns:

- item lifecycle
- locking
- ordering
- supersession
- retries

### State Store

Durable local storage for queue state and event history.

Recommended storage:

- SQLite

Reason:

- durable
- inspectable
- transactional
- no server required
- straightforward for per-repo and global views

## Persistence Model

Suggested durable entities:

### Repositories

- id
- canonical repo path
- protected branch
- remote name
- main worktree path
- current policy version

### Integration Submissions

- id
- repo id
- branch name
- source worktree path
- source sha
- requested by
- status
- created at
- updated at
- last error

Statuses:

- `queued`
- `running`
- `blocked`
- `succeeded`
- `failed`
- `superseded`
- `cancelled`

### Publish Requests

- id
- repo id
- target sha
- status
- superseded by
- created at
- updated at

Statuses:

- `queued`
- `running`
- `superseded`
- `succeeded`
- `failed`
- `cancelled`

### Events

- id
- repo id
- item type
- item id
- event type
- payload json
- created at

## Locking Model

There are two lock domains per repository:

- integration lock
- publish lock

Only one integration may run at a time.
Only one publish may run at a time.

Publish may overlap with idle time but should not mutate the protected branch.

Implementation options:

- file locks in repo-local or global state directories
- database-backed lease rows

Recommended approach:

- SQLite state for queue truth
- OS file lock for active worker exclusivity

## Repository Layout Assumptions

`mainline` assumes:

- one canonical main worktree exists
- many topic worktrees may exist
- all worktrees belong to the same underlying repository
- the protected branch is checked out in the canonical worktree

It should not require bare-clone workflows, but it should support them cleanly.

## Commands

Proposed MVP CLI:

- `mainline submit`
- `mainline status`
- `mainline run-once`
- `mainline retry`
- `mainline cancel`
- `mainline publish`
- `mainline doctor`
- `mainline repo init`
- `mainline repo show`

### `mainline submit`

Submit current branch or explicit branch for integration.

Examples:

```bash
mainline submit
mainline submit --branch fix-login
mainline submit --branch fix-login --worktree ~/Projects/_wt/acme/app/fix-login
```

### `mainline status`

Show queue state.

Examples:

```bash
mainline status
mainline status --json
mainline status --repo ~/Projects/acme/app
```

### `mainline run-once`

Run one integration/publish cycle without daemon mode.

Useful for:

- local use
- debugging
- CI-style invocation

### `mainline publish`

Request publish of the current protected-branch tip.

Usually invoked automatically after integration.

### `mainline doctor`

Inspect repository health and detect:

- dirty protected branch
- missing main worktree
- diverged upstream
- broken queue items
- stale locks

## Config

Config should exist at two levels:

- global user config
- per-repository config

### Global Config

Examples:

- state directory
- default remote name
- daemon enablement
- logging format

### Repository Config

Examples:

- protected branch
- canonical main worktree path
- sync policy
- publish policy
- integration strategy
- pre-integration checks
- pre-publish checks

Example repo config:

```toml
[repo]
protected_branch = "main"
remote = "origin"
main_worktree = "/Users/alice/Projects/acme/app"

[integration]
strategy = "rebase-then-ff"
sync_policy = "sync-before-integrate"

[publish]
mode = "auto"
coalesce = true
interrupt_inflight = true

[checks]
pre_integrate = ["just lint", "just test-changed"]
pre_publish = []
```

## Failure Modes and Handling

### Dirty Protected Branch

Behavior:

- refuse integration
- mark repo unhealthy
- require operator action

### Diverged Upstream

Behavior:

- refuse auto-sync
- block integration and publish
- require manual reconciliation

### Rebase Conflict in Topic Branch

Behavior:

- mark submission blocked
- preserve context in topic worktree
- emit actionable instructions

### In-Flight Push Superseded

Behavior:

- mark running publish stale
- interrupt local push if configured and feasible
- enqueue latest protected-branch tip for publish

### Daemon Crash

Behavior:

- state survives in SQLite
- stale locks can be recovered
- `mainline doctor` can identify unfinished work

## Security Model

`mainline` is a local developer tool, not a security boundary.

It provides safety through:

- queue serialization
- branch invariants
- explicit policy
- auditability

It does not prevent a determined user from bypassing it with raw Git commands.

For stronger guarantees, pair it with:

- branch protection on the remote
- CI checks
- repository policy documentation

## Observability

Required:

- human-readable logs
- structured JSON logs
- event history in state store
- queue status output

Nice to have:

- TUI or web UI
- Prometheus metrics
- notifications for blocked or failed items

## Distribution

Primary distribution targets:

- GitHub Releases
- Homebrew tap
- Nix package

The project should build as a single Go binary with no external runtime dependency.

## MVP

The MVP should deliver:

- one protected branch per repo
- canonical main worktree configuration
- branch submission
- ordered integration queue
- rebase-then-fast-forward integration
- upstream fast-forward sync before integration
- blocked-item handling for conflicts
- coalesced publish queue
- latest-tip publish semantics
- durable SQLite state
- status and doctor commands

The MVP does not need:

- web UI
- distributed coordination
- multi-user shared server mode
- complex merge strategies
- policy plugins

## Success Criteria

`mainline` is successful when users can say:

- “my local `main` never gets messy anymore”
- “agents can land branches without stomping each other”
- “I stopped thinking about stale pushes”
- “conflicts happen where they should happen”
- “I can always see why something landed, failed, or got skipped”

## Open Questions

- Should blocked rebases auto-abort, or preserve rebase state in the source worktree by default?
- Should publish interruption be enabled by default, or opt-in until field-tested?
- Should one daemon manage multiple repositories, or should each repository run its own worker loop?
- Should pre-integration checks run in the source worktree, the main worktree, or be policy-selectable?
- Should the canonical main worktree be auto-discovered or explicitly configured only?
- How much Git provider awareness, if any, should exist in the first public release?
