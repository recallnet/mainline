-- +goose Up
CREATE TABLE IF NOT EXISTS repositories (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	canonical_path TEXT NOT NULL UNIQUE,
	protected_branch TEXT NOT NULL,
	remote_name TEXT NOT NULL,
	main_worktree_path TEXT NOT NULL,
	policy_version TEXT NOT NULL,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS integration_submissions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	repo_id INTEGER NOT NULL,
	branch_name TEXT NOT NULL,
	source_worktree_path TEXT NOT NULL,
	source_sha TEXT NOT NULL,
	requested_by TEXT NOT NULL,
	status TEXT NOT NULL,
	last_error TEXT NOT NULL DEFAULT '',
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	FOREIGN KEY (repo_id) REFERENCES repositories(id)
);

CREATE INDEX IF NOT EXISTS idx_integration_submissions_repo_status_created
ON integration_submissions(repo_id, status, created_at);

CREATE TABLE IF NOT EXISTS publish_requests (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	repo_id INTEGER NOT NULL,
	target_sha TEXT NOT NULL,
	status TEXT NOT NULL,
	superseded_by INTEGER,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	FOREIGN KEY (repo_id) REFERENCES repositories(id),
	FOREIGN KEY (superseded_by) REFERENCES publish_requests(id)
);

CREATE INDEX IF NOT EXISTS idx_publish_requests_repo_status_created
ON publish_requests(repo_id, status, created_at);

CREATE TABLE IF NOT EXISTS events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	repo_id INTEGER NOT NULL,
	item_type TEXT NOT NULL,
	item_id INTEGER,
	event_type TEXT NOT NULL,
	payload BLOB NOT NULL,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	FOREIGN KEY (repo_id) REFERENCES repositories(id)
);

CREATE INDEX IF NOT EXISTS idx_events_repo_created
ON events(repo_id, created_at);

-- +goose Down
DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS publish_requests;
DROP TABLE IF EXISTS integration_submissions;
DROP TABLE IF EXISTS repositories;
