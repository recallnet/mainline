# Machine-Readable Contracts

`mq` and `mainlined` are intended to be automatable. This document defines the
public JSON contracts that factory daemons and agent wrappers may depend on.

## Compatibility Policy

Current machine-readable contract family: `v1`

For `v1`:

- documented fields are stable
- field order is not significant
- additive fields are allowed
- optional fields may be omitted when empty
- breaking changes require:
  - updating this document
  - updating the contract tests in `internal/app/app_test.go`
  - bumping the documented contract family to a new version

If you are building automation, bind only to the fields documented here.

## `mq status --json`

Returns one JSON object with these stable top-level keys:

- `repository_root`
- `state_path`
- `current_worktree`
- `current_branch`
- `protected_branch`
- `protected_branch_sha`
- `protected_upstream`
- `counts`
- `recent_events`

Optional top-level keys:

- `latest_submission`
- `latest_publish`
- `active_submissions`
- `active_publishes`
- `integration_worker`
- `publish_worker`

`protected_upstream` is a `git.BranchStatus` object with:

- `name`
- `head_sha`
- `upstream`
- `ahead_count`
- `behind_count`
- `has_upstream`
- `is_protected_branch`

`ahead_count` and `behind_count` are exact commit counts relative to the
upstream ref, not boolean-like drift flags.

`latest_submission` and entries in `active_submissions` extend the durable
submission record with optional blocked-state diagnostics:

- `allow_newer_head`
- `publish_request_id`
- `publish_status`
- `outcome`
- `blocked_reason`
- `conflict_files`
- `protected_tip_sha`
- `retry_hint`

For succeeded submissions:

- `publish_request_id` is the correlated publish request id when `mainline` can
  associate the landed protected SHA with a publish request
- `publish_status` is the correlated publish request status
- `outcome` is:
  - `integrated` when the submission succeeded but publish is absent or not yet succeeded
  - `landed` when the correlated publish request succeeded

`integration_worker` and `publish_worker` mirror the active lock metadata when a
worker is currently holding that lease:

- `domain`
- `repo_root`
- `owner`
- `request_id`
- `pid`
- `created_at`

## `mq events --json`

Returns newline-delimited JSON. Each line is one durable event record with these
stable keys:

- `id`
- `repo_id`
- `item_type`
- `item_id`
- `event_type`
- `payload`
- `created_at`

The exact shape of `payload` depends on `event_type`. Durable events are the raw
audit log.

## `mq events --json --lifecycle`

Returns newline-delimited JSON branch lifecycle records projected from the
durable event log.

Stable common fields:

- `event`
- `status`
- `timestamp`
- `repository_root`

Common optional fields:

- `submission_id`
- `publish_request_id`
- `branch`
- `sha`
- `source_sha`
- `source_worktree`
- `error`
- `blocked_reason`
- `conflict_files`
- `protected_tip_sha`
- `retry_hint`

Lifecycle events currently emitted:

- `submitted`
- `integration_started`
- `blocked`
- `failed`
- `integrated`
- `retried`
- `cancelled`
- `publish_requested`
- `publish_retry_scheduled`
- `published`
- `publish_failed`
- `publish_retried`
- `publish_cancelled`

## `mq watch --json`

Returns newline-delimited JSON snapshots. Each line is one watch frame with:

- `observed_at`
- `status`

`status` is the exact `mq status --json` object shape documented above.

## `mq wait --json`

Returns one JSON object keyed by durable `submission_id`.

Stable fields:

- `submission_id`
- `branch`
- `source_worktree`
- `source_sha`
- `repository_root`
- `protected_branch`
- `submission_status`
- `outcome`
- `duration_ms`

Optional fields:

- `source_ref`
- `ref_kind`
- `protected_sha`
- `publish_request_id`
- `publish_status`
- `last_worker_result`
- `error`

`mq wait --for integrated` uses:

- `outcome = "integrated"` when the submission succeeded and the landed SHA is
  verified reachable from protected `main`

`mq wait --for landed` uses:

- `outcome = "landed"` when the submission succeeded and the correlated publish
  request also succeeded

Failure outcomes are:

- `blocked`
- `failed`
- `cancelled`
- `timed_out`

## `mainlined --json`

Returns newline-delimited JSON log records.

Stable fields:

- `level`
- `event`
- `repo`
- `timestamp`

Optional fields:

- `cycle`
- `message`

Common daemon events:

- `daemon.started`
- `cycle.completed`
- `cycle.failed`
- `daemon.idle_exit`
- `daemon.max_cycles_reached`
- `daemon.stopped`
