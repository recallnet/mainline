# mainline Forward Plan

`mainline` is already a working product. This document is the forward plan from
the currently shipped `main`, not a milestone ledger.

The unit of progress is a landed commit on `main`, usually followed by a
review-fix commit if the review loop finds a real issue. Each phase below is a
product tranche that should be able to land independently.

## Current Product Shape

Today `mainline` is:

- a CLI and daemon for coordinating one protected Git branch per repo
- a durable local queue backed by SQLite
- a repo-local control plane for many worktrees and many coding agents
- a machine-readable integration surface through `mq`, `mainlined`, and the
  published JSON contracts

Today `mainline` is not yet:

- a stable public Go library
- a general replacement for every Git operation in a factory runtime
- a multi-protected-branch coordinator

That distinction matters. Near-term adoption should target the CLI and daemon
surface first.

## Commit-Based Phases

### Phase A: Factory API Stabilization

Goal:

- make the CLI JSON surfaces durable enough to serve as a real factory API

Needed:

- keep `docs/JSON_CONTRACTS.md` versioned and authoritative
- add stable submission detail lookup by `submission_id`
- keep submission-to-publish correlation explicit in status and lifecycle views
- extend contract tests whenever a new machine-readable field becomes product
  surface

Done when:

- a factory can integrate against `mq` JSON without depending on internal Go
  structs
- contract changes are additive unless the schema version changes

### Phase B: Source-Independent Submission

Goal:

- remove the biggest unattended-factory fragility: dependence on a living source
  worktree

Needed:

- add snapshot-style submission by durable SHA/ref
- define explicit failure and recovery semantics for:
  - source missing
  - source moved
  - source dirtied
  - source branch head drift
- keep worktree-based submission as the best interactive path for humans and
  agents

Done when:

- factories can submit durable work without requiring the original worktree to
  remain intact for the whole queue lifetime

### Phase C: Publish Circuit Breaker and Recovery

Goal:

- prevent repeated publish-hook or publish-check failures from degrading into
  noisy failure spam

Needed:

- repo-level publish block state with durable reason and failure count
- automatic tripping after repeated publish failures in a configured window
- `mq diagnose publish --json`
- `mq unblock publish`
- clear operator and agent instructions for fixing and resuming publish

Done when:

- repeated hook/check failures become one durable blocked condition instead of a
  pile of failed publish requests

### Phase D: Metrics and Supervisor Readiness

Goal:

- make `mainline` easy for supervisors and factory control planes to trust

Needed:

- stable metrics emission for:
  - queue depth
  - integration latency
  - publish latency
  - retry rate
  - conflict rate
  - blocked rate
- stronger health/readiness reporting for `mq doctor --json` or a dedicated
  health command
- daemon/operator docs aimed at supervisors, not just humans

Done when:

- a supervisor can detect `healthy`, `blocked`, `degraded`, and `stuck`
  without parsing human prose

### Phase E: Global Daemon Productization

Goal:

- make one machine-global daemon the standard scaling model

Needed:

- harden `mainlined --all`
- keep repo registration and recovery robust
- document install and launch-agent flows as the default operator path
- optimize wakeup behavior if polling becomes the practical bottleneck

Done when:

- many repos can share one machine daemon without one idle process per repo

### Phase F: Canonical Root Enforcement

Goal:

- make the repo root checkout both Git-truth and filesystem-truth for humans

Needed:

- keep root checkout clean and on protected branch
- refuse wrapper builds from dirty canonical root
- keep `mq repo root` the standard trust check
- keep docs and agent skills aligned on:
  - root checkout is canonical
  - feature work happens in worktrees
  - queue owns landing

Done when:

- humans, wrappers, and supervisors all look at the same trustworthy checkout

### Phase G: Public Go Library Decision

Goal:

- decide whether `mainline` should stay CLI-first or also become a supported Go
  library

Rationale for a Go library:

- factories written in Go may want typed `Submit`, `Wait`, `Status`, `Events`,
  `Retry`, and `Doctor` APIs instead of shelling out to `mq`
- a supervisor service may want in-process control over timeouts, cancellation,
  logging, and result handling
- custom UIs or control planes may want typed queue and lifecycle models

If we do it, the design should be narrow:

- add a stable `pkg/` surface
- expose coordinator semantics, request/response models, and stable errors
- do not expose `internal/` directly
- do not try to replace native Git with a pure library abstraction

Done when either:

- we explicitly state that `mainline` is a CLI/daemon product and not a Go
  library, or
- we ship a small supported `pkg/` surface with typed orchestration APIs

## Working Rules For Future Phases

- land each phase as normal product work on `main`, not as speculative roadmap
  prose
- keep README, install docs, AGENTS, and repo-local skills in sync with the
  shipped command surface
- keep claims tied to tests, contracts, or observed behavior
- prefer the design that scales for many repos, many worktrees, and many agents,
  even if it is less convenient in the short term
