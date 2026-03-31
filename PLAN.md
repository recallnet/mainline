# mainline Plan

## Objective

Build `mainline` as an open-source Go project that protects a local protected branch, coordinates integrations from many worktrees, and coalesces publishes so only the latest protected-branch tip matters.

This plan assumes:

- Go implementation
- SQLite-backed state
- CLI-first architecture
- optional daemon
- macOS-first development with cross-platform intent
- prefer mature off-the-shelf Go libraries over bespoke infrastructure where they fit the product model

Implementation guidance:

- use `go-git` as the default Git engine instead of shelling out to raw `git` for ordinary repository inspection and mutation paths
- only build thin `mainline`-specific adapters around library behavior and product policy

## Product Milestones

### Milestone 0: Project Skeleton

Goal:

- establish a clean open-source repository shape and build pipeline

Deliverables:

- Go module initialized
- `cmd/mainline` CLI entrypoint
- `cmd/mainlined` daemon entrypoint
- package layout for git, queue, state, policy, and worker logic
- Makefile or `justfile`
- CI for format, lint, test, release build
- README with positioning and install notes

Acceptance criteria:

- `go test ./...` passes
- `mainline --help` works
- `mainlined --help` works

## Milestone 1: Repository Discovery and Health

Goal:

- understand and validate a repository before any queue logic exists

Deliverables:

- detect repo root from current directory
- inspect worktrees and refs using `go-git`-backed repository access
- identify canonical protected branch worktree
- config file support
- `mainline repo init`
- `mainline repo show`
- `mainline doctor`

Checks in `doctor`:

- is this a Git repo
- which branch is protected
- where is the canonical worktree
- is the protected branch clean
- does upstream exist
- does protected branch diverge from upstream
- are there stale locks or unfinished queue items

Acceptance criteria:

- can point `mainline` at a repo and get an accurate health report
- can detect dirty `main`
- can detect missing canonical worktree

## Milestone 2: Durable State and Locking

Goal:

- establish queue state that survives crashes

Deliverables:

- SQLite schema
- repo records
- integration submission records
- publish request records
- event log table
- lock abstraction
- stale lock detection and recovery rules

Acceptance criteria:

- queue items persist across process restarts
- only one integration worker can run per repo
- only one publish worker can run per repo

## Milestone 3: Branch Submission

Goal:

- allow humans and agents to submit topic branches safely

Deliverables:

- `mainline submit`
- current-branch submission
- explicit branch/worktree submission
- submitter metadata capture
- source worktree cleanliness checks
- rejection when branch is protected branch
- rejection when branch HEAD is missing or ambiguous

Acceptance criteria:

- a submitted branch appears in queue state
- invalid branches are rejected with actionable errors

## Milestone 4: Integration Queue MVP

Goal:

- land topic branches onto protected branch safely and serially

Strategy:

- `rebase-then-ff`

Worker flow:

1. acquire integration lock
2. validate repo health
3. load oldest queued submission
4. fetch remote if configured
5. fast-forward protected branch from upstream if configured and possible
6. validate source branch still exists
7. ensure source worktree is clean
8. attempt rebase of source branch onto protected branch in source worktree
9. if successful, fast-forward protected branch to source branch tip
10. mark submission succeeded
11. emit publish request if auto-publish is enabled

Blocked behavior:

- if rebase conflicts, mark submission blocked
- do not modify protected branch
- preserve source worktree as resolution site

Deliverables:

- worker implementation
- `mainline run-once`
- status transitions
- event logging

Acceptance criteria:

- two queued branches land in deterministic order
- protected branch stays clean after success
- protected branch stays unchanged after conflict

## Milestone 5: Publish Queue MVP

Goal:

- coalesce publishes and publish only the latest protected-branch tip

Publish semantics:

- publish requests are keyed by repo + protected branch
- if a newer protected-branch SHA exists, older queued publish requests are superseded
- only latest protected-branch tip is pushed

Worker flow:

1. acquire publish lock
2. compute latest protected-branch tip
3. mark older queued publish requests superseded
4. push latest tip to remote
5. if protected branch changed during push, schedule another publish

Optional behavior for MVP:

- do not interrupt in-flight pushes yet
- simply re-run publish if the branch advanced

Reason:

- simpler and safer first release
- still achieves “latest wins” semantically

Deliverables:

- publish queue schema and worker
- `mainline publish`
- auto-publish after successful integration

Acceptance criteria:

- multiple rapid publish requests result in at most one meaningful final publish of latest tip

## Milestone 6: Daemon Mode

Goal:

- support background operation for agent-heavy workflows

Deliverables:

- `mainlined` worker loop
- poll or notify-on-state-change behavior
- structured logs
- graceful shutdown
- lock recovery on restart

Acceptance criteria:

- daemon can process integrations and publishes without manual `run-once`
- crash/restart does not lose queue truth

## Milestone 7: Policies and Hooks

Goal:

- make the tool useful across more repositories without hardcoding workflows

Deliverables:

- repo config file
- sync policy options
- publish mode options
- pre-integrate checks
- pre-publish checks
- command timeout controls

Suggested initial policy matrix:

- `sync_policy`: `manual`, `sync-before-integrate`
- `publish_mode`: `manual`, `auto`
- `integration_strategy`: `rebase-then-ff`, `ff-only`

Acceptance criteria:

- different repos can use different policies
- failing checks block landing before protected branch mutation

## Milestone 8: In-Flight Publish Preemption

Goal:

- support local interruption of stale publish attempts

Deliverables:

- tracked publish worker process handles
- stale publish detection during in-flight pushes
- configurable local process interruption
- safe restart logic

Important note:

- remote-side cancellation is impossible once a push has completed
- this feature is about local process preemption and publish coalescing

Acceptance criteria:

- when enabled, a newer publish request can preempt an older local in-flight push
- final observable result is that latest protected-branch tip becomes the next published tip

## Milestone 9: UX and OSS Readiness

Goal:

- make the project understandable and adoptable

Deliverables:

- polished README
- architecture docs
- examples for solo, worktree-heavy, and agent-heavy flows
- install docs for Homebrew and Nix
- shell completions
- error messages tuned for recovery

Nice-to-have:

- TUI status screen
- event stream output
- GitHub Action for release automation

## Architecture Plan

## Repository Layout

Proposed layout:

```text
cmd/
  mainline/
  mainlined/
internal/
  cli/
  config/
  doctor/
  events/
  git/
  locks/
  policy/
  publish/
  queue/
  repo/
  state/
  worker/
pkg/
  api/           # optional future stable public package
testdata/
```

Guideline:

- keep reusable internals under `internal/`
- avoid exposing a public library API until it is justified

## State Schema Plan

Initial tables:

- `repositories`
- `submissions`
- `publish_requests`
- `events`
- `locks` if DB-backed leases are used

Indexes:

- queued submissions by repo and creation time
- queued publish requests by repo and protected branch
- events by repo and created time

## Command Surface Plan

First-pass commands:

- `mainline repo init`
- `mainline repo show`
- `mainline doctor`
- `mainline submit`
- `mainline status`
- `mainline run-once`
- `mainline publish`
- `mainline retry <id>`
- `mainline cancel <id>`

Deferred:

- `mainline logs`
- `mainline watch`
- `mainline config edit`

## Git Operations Plan

All Git operations must be:

- non-interactive
- explicit about worktree path
- explicit about target refs
- wrapped with structured stdout/stderr capture

Needed capabilities:

- discover repo root
- list worktrees
- inspect branch and ref SHAs
- detect cleanliness
- fetch remote refs
- fast-forward protected branch
- rebase source branch in source worktree
- abort or preserve rebase state intentionally
- push protected branch

Git wrapper rules:

- no shell interpolation of user-provided ref names without validation
- no destructive commands without explicit state guard
- no interactive merge or rebase flows

## Testing Plan

### Unit Tests

Focus:

- state transitions
- supersession logic
- config parsing
- lock behavior
- policy evaluation

### Integration Tests

Use temporary Git repositories to simulate:

- protected branch setup
- multiple worktrees
- upstream fast-forward
- rebase success
- rebase conflict
- queued submissions
- superseded publish requests

Must-have scenarios:

- branch submits and lands cleanly
- branch blocks on conflict and leaves protected branch unchanged
- upstream advances before integration and queue syncs correctly
- protected branch advances twice before publish and only latest matters

### Soak Tests

Later:

- many queued branches
- many rapid publish requests
- daemon crash and restart
- stale lock recovery

## Release Plan

### Alpha

Ship when:

- integrations and publishes work reliably on a single machine
- queue state is durable
- doctor/status provide actionable output

Audience:

- your own machine
- a few power users

### Beta

Ship when:

- config is stable enough for outside users
- docs are good
- edge-case recovery is tested

Audience:

- open-source early adopters
- worktree-heavy users
- AI-agent operators

### 1.0

Ship when:

- semantics of submit/integrate/publish are stable
- storage format is migration-safe
- core commands are well documented
- failure handling feels trustworthy

## Open Source Plan

### Messaging

Lead with:

- local branch protection
- worktree-friendly Git coordination
- latest-only publish queue

Do not lead with:

- “AI agents”

Reason:

- the product is broader than that
- agent-heavy workflows remain a strong advanced use case

### Initial README Story

1. problem statement
2. quick mental model
3. 5-minute setup
4. solo developer example
5. worktree queue example
6. agent-heavy example

## Engineering Constraints

- protected branch must never become a conflict workspace
- no hidden commits on protected branch
- no daemon-only magic required for correctness
- every queue mutation must be reconstructable from stored events
- operations must be restart-safe

## Suggested Implementation Order

Week 1:

- project skeleton
- repo discovery
- config model
- doctor

Week 2:

- SQLite state
- submission flow
- status command

Week 3:

- integration queue
- rebase-then-ff worker
- integration tests

Week 4:

- publish queue
- coalesced latest-only publish
- daemon MVP

Week 5:

- policy hooks
- better errors
- docs and examples

Week 6:

- preemptive publish interruption
- OSS hardening
- packaging

## Risks

### Risk: Git Edge Cases Become the Product

Mitigation:

- keep MVP narrow
- support one blessed strategy first

### Risk: Daemon Complexity Hides Failure

Mitigation:

- require status and doctor parity
- keep `run-once` fully functional

### Risk: Branch Resolution UX Is Confusing

Mitigation:

- always preserve the rule that branch conflicts are fixed in the branch worktree
- provide exact next steps on block

### Risk: Publish Interruption Is Overcomplicated

Mitigation:

- ship latest-only publish semantics first
- add local preemption later

## Immediate Next Steps

1. initialize Go module and directory layout
2. define config format and repo discovery rules
3. implement `doctor`
4. design SQLite schema and event model
5. implement `submit` and `status`
6. build integration worker before daemon mode
