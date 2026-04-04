# Go Refactor Tasks

This is the concrete follow-up task list from the idiomatic-Go review.

## Completed In This Slice

- Introduced a typed domain layer for:
  - ref kinds
  - submission status
  - publish status
  - submission outcome
  - blocked reason
  - event type and item type
- Added typed event payload structs for the core workflow.
- Moved the integration workflow out of `run_once.go` into
  `integration_workflow.go`.
- Decoupled `status --json` from the raw persistence structs by projecting
  dedicated read models.

## Remaining Required Tasks

- Move the publish workflow and recovery helpers out of `run_once.go` into a
  dedicated file, matching the integration side.
- Replace the remaining ad hoc event payload maps outside the core workflow
  with typed payload structs.
- Add transaction-shaped store helpers for core transitions so status updates
  and event appends stop being manually sequenced across multiple calls.
- Split `repo.go` into command-focused files:
  - `repo_init.go`
  - `repo_show.go`
  - `repo_root.go`
  - `repo_doctor.go`
  - `repo_audit.go`
- Remove the placeholder-command path from production CLI behavior.
- Push Git failure classification toward typed semantic errors at the
  `internal/git` boundary, keeping substring checks only as a last resort.
- Decide whether `mainline` will expose a supported `pkg/` surface for Go
  callers or remain a CLI/daemon product only.

## Recommended Order

1. Extract publish workflow from `run_once.go`.
2. Add transactional store helpers for state transitions and event emission.
3. Split `repo.go` by command.
4. Type the remaining event payloads and Git failure causes.
5. Decide and document the public Go API boundary.
