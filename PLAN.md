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
- explicitly support bare-clone-plus-worktree layouts, where the canonical repository storage lives separately from the checked-out worktree
- keep durable queue state with the shared repository storage or a global state root, never in per-worktree mutable directories
- use native `git` only for workflow-critical operations that `go-git` does not expose or support correctly enough
- `go-git` may read and write rebase-related branch config, but the actual integration rebase step should still be expected to run through native `git rebase`

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
- detect and model bare repo storage path separately from worktree path
- config file support
- `mainline repo init`
- `mainline repo show`
- `mainline doctor`

Checks in `doctor`:

- is this a Git repo
- which branch is protected
- where is the canonical worktree
- where the shared repository storage lives
- is the protected branch clean
- does upstream exist
- does protected branch diverge from upstream
- are worktrees in an expected location for this machine policy
- are there stale locks or unfinished queue items

Acceptance criteria:

- can point `mainline` at a repo and get an accurate health report
- can detect dirty `main`
- can detect missing canonical worktree
- can operate correctly when invoked from a linked worktree of a bare-clone layout

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
- state root resolution that prefers shared repo storage or configurable global state over per-worktree paths

Acceptance criteria:

- queue items persist across process restarts
- only one integration worker can run per repo
- only one publish worker can run per repo
- all worktrees for the same underlying repo observe the same queue state

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
- repo state is shared correctly when submission is invoked from a linked worktree or bare-clone worktree layout

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
8. if source worktree is dirty, apply configured dirty-worktree policy before integration
9. attempt rebase of source branch onto protected branch in source worktree
10. if successful, fast-forward protected branch to source branch tip
11. mark submission succeeded
12. emit publish request if auto-publish is enabled

Blocked behavior:

- if rebase conflicts, mark submission blocked
- do not modify protected branch
- preserve source worktree as resolution site

Dirty worktree behavior:

- default MVP policy should reject dirty source worktrees with precise recovery instructions
- stash/rebase/unstash may be added later as an explicit opt-in policy, not an implicit default

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

Important machine-level constraint:

- `mainline` may not be the only writer to `origin/<protected>` during early adoption
- publish logic must coexist with direct `git push` from agents or factory daemons by re-checking protected-branch tip before and after publish attempts

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
- hook coordination policy so `mainline` checks do not naively stack on heavyweight repo hooks
- worktree location policy, including machine-specific path expectations and doctor warnings

Suggested initial policy matrix:

- `sync_policy`: `manual`, `sync-before-integrate`
- `publish_mode`: `manual`, `auto`
- `integration_strategy`: `rebase-then-ff`, `ff-only`
- `dirty_worktree_policy`: `reject`, `stash-and-restore`
- `worktree_layout_policy`: `any`, `enforce-prefix`
- `hook_policy`: `inherit`, `replace-with-mainline-checks`, `bypass-with-explicit-command`

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

## Milestone 10: Self-Hosting and `mq` Dogfooding

Goal:

- use `mainline` on `mainline` through an explicit worktree-first workflow

Deliverables:

- committed repo-local worktree skill
- documented self-hosting flow for feature worktrees, submission, integration, and publish
- README and flow docs that point agents and humans at the same `mq` path
- cleanup of any stale "submission only" caveats now that end-to-end landing exists

Acceptance criteria:

- this repo contains the worktree skill it expects agents to follow
- the documented `mq` workflow is end-to-end and internally consistent
- humans and agents have one canonical dogfooding path for landing work

## Milestone 11: Operator Controls

Goal:

- give operators explicit recovery tools for queue items

Deliverables:

- `mainline retry`
- `mainline cancel`
- durable event logging for operator-triggered state changes
- status/doctor output updated for canceled and retried items

Acceptance criteria:

- blocked or failed work can be retried without editing SQLite directly
- queued work can be canceled safely
- operator actions are visible in durable history

## Milestone 12: Real Distribution Packaging

Goal:

- move from source-first install instructions to real package outputs

Deliverables:

- Homebrew formula or tap output
- Nix package or flake output
- release build metadata for published binaries
- install docs that match published artifacts

Acceptance criteria:

- a new user can install `mainline`, `mq`, and `mainlined` through Homebrew or Nix without cloning the repo
- release docs point at real published artifacts

## Milestone 13: Live Operator UX

Goal:

- make queue supervision easier during active multi-agent use

Deliverables:

- event stream output for live queue observation
- improved status views for active integrations and publishes
- optional TUI status screen if the event stream proves insufficient

Acceptance criteria:

- operators can watch queue activity without polling raw SQLite-backed commands manually
- active integrations, publishes, retries, and cancels are visible in a live operator-facing surface

## Milestone 14: Named Watch And Logs Surface

Goal:

- turn the deferred operator commands into first-class interfaces instead of leaving them as placeholders

Deliverables:

- `logs` command as the named durable queue history surface
- `watch` command as a live refreshing status view
- shell completion and docs that advertise the new operator commands

Acceptance criteria:

- operators can use `mq logs` without needing to know the lower-level `events` command name
- operators can use `mq watch` to keep a live repo status screen open during active work
- the named operator commands use the same queue/state codepaths as `status` and `events`

## Milestone 15: Repo Config Editing

Goal:

- make repo policy editing a supported operator path instead of requiring manual file hunting

Deliverables:

- `config edit` command that opens the repo config in the operator's editor
- config scaffolding when the repo has no config yet
- shell completion and docs for the new command surface

Acceptance criteria:

- operators can run `mq config edit --repo /path/to/main` and land in the correct config file
- the command works from linked worktrees and edits the shared repo config path
- a missing config is scaffolded with repo-aware defaults before the editor opens

## Milestone 16: Release Automation

Goal:

- turn packaging into repeatable release artifacts instead of source-only packaging definitions

Deliverables:

- local release build script for multi-platform archives
- checksum generation for release assets
- tag-driven GitHub workflow that uploads archives and checksums to a GitHub release
- install docs that point at both package managers and direct release downloads

Acceptance criteria:

- maintainers can build a full release artifact set locally with one command
- pushing a version tag produces downloadable archives for `mainline`, `mq`, and `mainlined`
- release docs match the actual GitHub release artifact layout

## Milestone 17: Binary Identity And Versioning

Goal:

- make shipped binaries identify themselves correctly and report build metadata

Deliverables:

- program-aware help text so `mq` and `mainlined` do not identify as `mainline`
- `version` command and `--version` flag support
- ldflags-based build metadata wired through normal builds and release builds
- docs that point operators at the version surface for support and bug reports

Acceptance criteria:

- `mq --help` identifies itself as `mq`
- `mainline version`, `mq version`, and `mainlined --version` report build metadata
- release artifacts embed the tagged version instead of always reporting `dev`

## Milestone 18: Versioned Homebrew Release Metadata

Goal:

- make GitHub releases publish package metadata for stable Homebrew installs instead of only `--HEAD` source builds

Deliverables:

- formula generation script that targets a tagged GitHub release
- generated formula asset attached by the release workflow
- release verification for generated formula syntax and URLs
- install docs that describe stable Homebrew release usage

Acceptance criteria:

- a tagged release publishes a `mainline.rb` formula asset tied to that version
- the formula installs the correct archive for Intel and Apple Silicon macOS
- install docs no longer imply that Homebrew support is `--HEAD`-only

## Milestone 19: Machine-Readable Release Manifest

Goal:

- publish a stable manifest that automation can consume without scraping GitHub release pages

Deliverables:

- manifest generation script that emits version, commit, archive URLs, and checksums as JSON
- CI verification for manifest generation
- release workflow upload of the manifest asset
- install docs that point operators and automation at the manifest

Acceptance criteria:

- a tagged release publishes a `release-manifest.json` asset
- the manifest includes every shipped archive plus checksum and URL metadata
- automation can discover the current release payload shape from a single JSON file

## Milestone 20: Invariant Coverage

Goal:

- make core queue and publish invariants explicit in end-to-end tests instead of relying on scattered feature tests

Deliverables:

- invariant-focused test matrix for integration, conflict, cancel, retry, and publish behavior
- reusable test helpers for asserting protected-branch cleanliness and queue/status consistency
- CI entrypoint for the invariant suite

Acceptance criteria:

- protected-branch safety is asserted after success, conflict, cancel, and retry flows
- status output is checked against durable state in representative end-to-end scenarios
- the invariant suite runs as a named CI step instead of being an implicit side effect of the full test run

## Milestone 21: Repo Hook Rigor

Goal:

- mirror CI-grade safety locally for a repository that pushes straight to `main`

Deliverables:

- repo-managed `pre-commit` and `pre-push` hooks under a tracked hooks path
- hook installer script and documented setup flow for contributors
- shell regression tests that exercise hook pass and fail paths against realistic Git state

Acceptance criteria:

- `pre-commit` blocks unformatted staged Go files, workflow mistakes, staged secrets, and release drift on relevant changes
- `pre-push` blocks dirty pushes, stale `origin/main`, and pushes to `origin/main` from a non-`main` local branch
- contributors can enable the tracked hooks with one documented command

## Milestone 22: Multi-Agent Stress And Soak

Goal:

- prove the queue holds up under real local parallelism instead of only small deterministic flows

Deliverables:

- a named stress test target that creates many feature worktrees, submits in parallel, and drains through the daemon
- machine-readable stress metrics for submission outcomes, publish coalescing, queue depth, and drain duration
- conflict traffic in the stress run so blocked-item handling is exercised under load

Acceptance criteria:

- parallel submissions from many worktrees land deterministically without dirtying the protected worktree
- exactly one publish completes for the final protected tip while older publish requests are superseded
- the stress run emits metrics that operators can use to track queue behavior over time

## Milestone 23: Repeated Soak And Flake Tracking

Goal:

- move from one-off green stress runs to evidence about stability over many runs

Deliverables:

- soak runner that executes the stress workload repeatedly with configurable iteration count
- machine-readable soak summaries that record pass/fail, duration, queue depth, and publish coalescing metrics per run
- flake-rate reporting in CI or local maintainer tooling

Acceptance criteria:

- maintainers can run a long soak with one command
- every soak run produces a persisted summary that can be compared across revisions
- any intermittent queue, publish, lock, or hook failure is visible as a measured flake instead of anecdotal suspicion

## Milestone 24: Randomized Timing And Failure Injection

Goal:

- make race conditions and brittle ordering assumptions easier to reproduce before production

Deliverables:

- randomized delays around submit, integration, publish, and daemon polling paths in test-only harnesses
- fault injection for fetch, rebase, push, hook execution, and state-store write paths
- seeded replay support so a failing randomized run can be reproduced exactly
- a randomized soak command that persists per-run seeds and replay metadata

Acceptance criteria:

- the stress harness can run with randomized timing and seeded replay
- failure injection exercises blocked, failed, retried, and superseded paths without manual test surgery
- a failing randomized run can be rerun from the same seed and reproduce the same failure class
- maintainers can run repeated randomized soaks without inventing local wrapper scripts

## Milestone 25: Crash And Restart Correctness

Goal:

- prove restart-safety instead of assuming it from normal happy-path tests

Deliverables:

- tests that terminate the worker during integration, during publish, and while locks are held
- restart scenarios that verify queue state, events, and lock recovery after abrupt process exit
- explicit assertions for partial-progress recovery and stale-lock cleanup

Acceptance criteria:

- crashing during integration never leaves protected `main` dirty
- crashing during publish converges back to the correct latest publish target after restart
- stale locks are either reclaimed safely or surfaced clearly through `doctor` and worker output

## Milestone 26: Real-Repo Certification Matrix

Goal:

- validate behavior against the kinds of repos that will actually use `mainline`

Deliverables:

- certification checklist for at least a small matrix of real repos
- exercised repo classes including bare-clone-plus-worktree layouts, heavy hook repos, and agent-heavy repos
- captured findings and any repo-specific policy defaults required for safe adoption
- a repeatable certification runner that executes the committed matrix on disposable mirrors

Acceptance criteria:

- `mainline` passes end-to-end dogfood runs on multiple real repositories, not just temporary test repos
- at least one hook-heavy repo and one bare-clone layout repo complete repeated landing/publish cycles cleanly
- any repo-specific incompatibilities are documented as either fixed, policy-gated, or explicit non-goals
- maintainers can rerun the same matrix without hand-building disposable clone setups

## Milestone 27: Observability And Confidence Reporting

Goal:

- make confidence measurable during real use instead of inferred from occasional manual checks

Deliverables:

- exported metrics or structured reports for queue depth, drain latency, blocked rate, retry rate, supersede rate, and publish latency
- operator-visible confidence report summarizing recent soak results and live health signals
- command output that surfaces whether the current build has passed the required confidence gates

Acceptance criteria:

- operators can inspect recent queue behavior and confidence signals without reading raw SQLite tables
- regressions in latency, blocked rate, or flaky publish behavior are visible as metrics deltas
- maintainers can tell whether a build meets the current promotion bar from one report

Implementation note:

- soak and certification evidence should be stamped with the `mainline` commit they were produced from so the confidence report can reject stale evidence from a different build

## Milestone 28: Upgrade And Migration Safety

Goal:

- remove storage and release-upgrade uncertainty as a source of production risk

Deliverables:

- explicit schema migration path with upgrade and downgrade tests where supported
- compatibility checks for upgrading from older binaries to newer binaries on existing state
- release notes and tooling that call out any state or policy changes required during upgrade

Acceptance criteria:

- upgrading across released versions preserves durable queue state correctly
- new binaries can detect unsupported on-disk state and fail clearly instead of corrupting it
- state migrations are tested as first-class release criteria, not manual spot checks

Implementation note:

- the initial migration contract may be narrow: support in-place upgrade from the legacy unversioned schema to version `1`, and reject any future schema version newer than the running binary

## Milestone 29: Production Readiness Gate

Goal:

- define the concrete evidence required before claiming production trust

Deliverables:

- a written readiness rubric that includes soak length, acceptable flake rate, certification repos, migration coverage, and hook/policy coverage
- a release gate command or checklist that aggregates the required evidence
- a documented statement of what "production ready" means for `mainline`

Acceptance criteria:

- the project has an explicit promotion bar instead of relying on intuition
- maintainers can point to measured evidence for every major failure class before release
- no release is called production ready unless it passes the full readiness rubric

## Milestone 30: Factory Landing DX

Goal:

- make the queue-native migration from manual merge-plus-push to `mq` feel like one primitive, not an operator workflow

Deliverables:

- a single-command landing path from a source worktree
- machine-readable success output for automation
- timeout-aware waiting semantics that cover both integration and publish
- command docs aimed at factory and daemon migration

Acceptance criteria:

- a factory daemon can replace merge-plus-push with one `mq` command from the wave worktree
- the command returns nonzero on blocked, failed, cancelled, or timed-out landing
- successful command completion means the branch landed and the protected branch reached a published state that includes that work

## Milestone 31: Submission Preflight And Machine Output

Goal:

- make branch handoff into the queue cheap for automation to validate and parse

Deliverables:

- `submit --check` for deterministic no-mutation preflight
- `submit --json` for machine-readable success and failure output
- stable error codes for deterministic submission failures
- docs and completions that show the factory migration path

Acceptance criteria:

- automation can detect submitter-side failures without parsing human prose
- `submit --check` performs no queue mutation
- `submit --json` returns submission metadata on success and stable error codes on deterministic failure

## Milestone 32: Upstream Drift Operator DX

Goal:

- make upstream advancement feel like ordinary queue progress instead of a manual `main` repair task

Deliverables:

- explicit worker messaging when protected `main` is synced from upstream before integration
- durable `protected.synced_from_upstream` events for operators and daemons
- coverage for the common case where `origin/main` advances before a queued branch lands
- docs that reinforce that `mq` owns protected-branch sync, not the feature worktree

Acceptance criteria:

- `mq run-once` and queue-driven land flows explain upstream syncs in plain operator language
- event streams show when protected `main` was fast-forwarded from upstream
- the queue integrates a behind protected branch without forcing manual merge or push repair

## Milestone 33: Blocking Submit DX

Goal:

- let agents hand a branch to the queue and block on a final integration outcome without writing their own poll loops

Deliverables:

- `submit --wait` for blocking submit-plus-integration
- timeout and poll interval flags for caller control
- stable process exit codes for landed, blocked, and timed-out outcomes
- machine-readable submit wait output for agent wrappers

Acceptance criteria:

- `mq submit --wait` returns zero only when the submitted branch reaches integrated state
- blocked, failed, or cancelled outcomes return a nonzero exit that agents can distinguish from timeout
- agents can consume submit wait results through either human output or JSON without scraping status loops

## Milestone 34: Strict Submit Preflight

Goal:

- give agents a cheap dry-run admission check before they spend time on heavyweight local gates

Deliverables:

- `submit --check-only` as an explicit dry-run alias
- preflight validation that the branch includes the current protected tip
- duplicate active-submission detection for the same branch SHA
- docs and completions that distinguish the strict dry-run path from queueing submit

Acceptance criteria:

- `mq submit --check-only` performs no queue mutation
- dry-run failures are machine-readable and explain whether the branch needs a rebase or is already active in the queue
- exact duplicate active submissions are rejected instead of cluttering the queue

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

## Additional Packaging Follow-Through

- make GitHub CI publish explicit versioned package metadata alongside binary archives
- attach a versioned Homebrew formula asset instead of relying only on generic names
- attach a versioned release manifest asset so automation can pin exact filenames
- bundle package metadata and checksums into a versioned package archive for downstream consumers
- verify package asset generation in CI before tags land
