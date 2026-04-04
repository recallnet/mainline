-- name: UpsertRepository :one
INSERT INTO repositories (
	canonical_path,
	protected_branch,
	remote_name,
	main_worktree_path,
	policy_version
) VALUES (
	sqlc.arg(canonical_path),
	sqlc.arg(protected_branch),
	sqlc.arg(remote_name),
	sqlc.arg(main_worktree_path),
	sqlc.arg(policy_version)
)
ON CONFLICT(canonical_path) DO UPDATE SET
	protected_branch = excluded.protected_branch,
	remote_name = excluded.remote_name,
	main_worktree_path = excluded.main_worktree_path,
	policy_version = excluded.policy_version,
	updated_at = CURRENT_TIMESTAMP
RETURNING *;

-- name: GetRepositoryByPath :one
SELECT *
FROM repositories
WHERE canonical_path = sqlc.arg(canonical_path);

-- name: CreateIntegrationSubmission :one
INSERT INTO integration_submissions (
	repo_id,
	branch_name,
	source_ref,
	ref_kind,
	source_worktree_path,
	source_sha,
	allow_newer_head,
	requested_by,
	priority,
	status,
	last_error
) VALUES (
	sqlc.arg(repo_id),
	sqlc.arg(branch_name),
	sqlc.arg(source_ref),
	sqlc.arg(ref_kind),
	sqlc.arg(source_worktree_path),
	sqlc.arg(source_sha),
	sqlc.arg(allow_newer_head),
	sqlc.arg(requested_by),
	sqlc.arg(priority),
	sqlc.arg(status),
	sqlc.arg(last_error)
)
RETURNING *;

-- name: GetIntegrationSubmission :one
SELECT *
FROM integration_submissions
WHERE id = sqlc.arg(id);

-- name: NextQueuedIntegrationSubmission :one
SELECT *
FROM integration_submissions
WHERE repo_id = sqlc.arg(repo_id) AND status = 'queued'
ORDER BY
	CASE priority
		WHEN 'high' THEN 0
		WHEN 'normal' THEN 1
		WHEN 'low' THEN 2
		ELSE 1
	END ASC,
	created_at ASC,
	id ASC
LIMIT 1;

-- name: UpdateIntegrationSubmissionStatus :one
UPDATE integration_submissions
SET status = sqlc.arg(status), last_error = sqlc.arg(last_error), updated_at = CURRENT_TIMESTAMP
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: UpdateIntegrationSubmissionPriority :one
UPDATE integration_submissions
SET priority = sqlc.arg(priority), updated_at = CURRENT_TIMESTAMP
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: ListIntegrationSubmissions :many
SELECT *
FROM integration_submissions
WHERE repo_id = sqlc.arg(repo_id)
ORDER BY created_at ASC, id ASC;

-- name: ListIntegrationSubmissionsByStatus :many
SELECT *
FROM integration_submissions
WHERE repo_id = sqlc.arg(repo_id) AND status = sqlc.arg(status)
ORDER BY created_at ASC, id ASC;

-- name: CreatePublishRequest :one
INSERT INTO publish_requests (
	repo_id,
	target_sha,
	priority,
	status,
	attempt_count,
	next_attempt_at,
	superseded_by
) VALUES (
	sqlc.arg(repo_id),
	sqlc.arg(target_sha),
	sqlc.arg(priority),
	sqlc.arg(status),
	sqlc.arg(attempt_count),
	sqlc.narg(next_attempt_at),
	sqlc.narg(superseded_by)
)
RETURNING *;

-- name: GetPublishRequest :one
SELECT *
FROM publish_requests
WHERE id = sqlc.arg(id);

-- name: LatestQueuedPublishRequest :one
SELECT *
FROM publish_requests
WHERE repo_id = sqlc.arg(repo_id) AND status = 'queued'
ORDER BY
	CASE priority
		WHEN 'high' THEN 0
		WHEN 'normal' THEN 1
		WHEN 'low' THEN 2
		ELSE 1
	END ASC,
	created_at DESC,
	id DESC
LIMIT 1;

-- name: LatestReadyQueuedPublishRequest :one
SELECT *
FROM publish_requests
WHERE repo_id = sqlc.arg(repo_id)
  AND status = 'queued'
  AND (next_attempt_at IS NULL OR next_attempt_at <= sqlc.arg(now_utc))
ORDER BY
	CASE priority
		WHEN 'high' THEN 0
		WHEN 'normal' THEN 1
		WHEN 'low' THEN 2
		ELSE 1
	END ASC,
	created_at DESC,
	id DESC
LIMIT 1;

-- name: NextDelayedQueuedPublishRequest :one
SELECT *
FROM publish_requests
WHERE repo_id = sqlc.arg(repo_id)
  AND status = 'queued'
  AND next_attempt_at IS NOT NULL
  AND next_attempt_at > sqlc.arg(now_utc)
ORDER BY next_attempt_at ASC, id ASC
LIMIT 1;

-- name: SupersedeOlderQueuedPublishRequests :exec
UPDATE publish_requests
SET status = 'superseded', superseded_by = sqlc.arg(keep_id), updated_at = CURRENT_TIMESTAMP
WHERE repo_id = sqlc.arg(repo_id) AND status = 'queued' AND id <> sqlc.arg(keep_id);

-- name: UpdatePublishRequestStatus :one
UPDATE publish_requests
SET status = sqlc.arg(status), superseded_by = sqlc.narg(superseded_by), next_attempt_at = NULL, updated_at = CURRENT_TIMESTAMP
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: SchedulePublishRetry :one
UPDATE publish_requests
SET status = 'queued',
	attempt_count = sqlc.arg(attempt_count),
	next_attempt_at = sqlc.arg(next_attempt_at),
	superseded_by = NULL,
	updated_at = CURRENT_TIMESTAMP
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: ResetPublishRequestForRetry :one
UPDATE publish_requests
SET status = 'queued',
	attempt_count = 0,
	next_attempt_at = NULL,
	superseded_by = NULL,
	updated_at = CURRENT_TIMESTAMP
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: ListPublishRequests :many
SELECT *
FROM publish_requests
WHERE repo_id = sqlc.arg(repo_id)
ORDER BY created_at ASC, id ASC;

-- name: ListPublishRequestsByStatus :many
SELECT *
FROM publish_requests
WHERE repo_id = sqlc.arg(repo_id) AND status = sqlc.arg(status)
ORDER BY created_at ASC, id ASC;

-- name: AppendEvent :one
INSERT INTO events (
	repo_id,
	item_type,
	item_id,
	event_type,
	payload
) VALUES (
	sqlc.arg(repo_id),
	sqlc.arg(item_type),
	sqlc.narg(item_id),
	sqlc.arg(event_type),
	sqlc.arg(payload)
)
RETURNING *;

-- name: ListEvents :many
SELECT *
FROM events
WHERE repo_id = sqlc.arg(repo_id)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(limit_count);

-- name: ListEventsForItemDesc :many
SELECT *
FROM events
WHERE repo_id = sqlc.arg(repo_id)
  AND item_type = sqlc.arg(item_type)
  AND item_id = sqlc.arg(item_id)
ORDER BY id DESC
LIMIT sqlc.arg(limit_count);

-- name: ListEventsAfter :many
SELECT *
FROM events
WHERE repo_id = sqlc.arg(repo_id) AND id > sqlc.arg(after_id)
ORDER BY created_at ASC, id ASC
LIMIT sqlc.arg(limit_count);

-- name: CountUnfinishedIntegrationSubmissions :one
SELECT COUNT(*)
FROM integration_submissions
WHERE repo_id = sqlc.arg(repo_id) AND status IN ('queued', 'running', 'blocked');

-- name: CountUnfinishedPublishRequests :one
SELECT COUNT(*)
FROM publish_requests
WHERE repo_id = sqlc.arg(repo_id) AND status IN ('queued', 'running');

-- name: CountQueuedIntegrationSubmissions :one
SELECT COUNT(*)
FROM integration_submissions
WHERE repo_id = sqlc.arg(repo_id) AND status = 'queued';
