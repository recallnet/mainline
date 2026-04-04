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
	source_ref TEXT NOT NULL DEFAULT '',
	ref_kind TEXT NOT NULL DEFAULT 'branch',
	source_worktree_path TEXT NOT NULL,
	source_sha TEXT NOT NULL,
	allow_newer_head INTEGER NOT NULL DEFAULT 0,
	requested_by TEXT NOT NULL,
	priority TEXT NOT NULL DEFAULT 'normal',
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
	attempt_count INTEGER NOT NULL DEFAULT 0,
	next_attempt_at DATETIME,
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
