package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const (
	defaultDirName = "mainline"
	defaultDBName  = "state.db"
)

// Store describes the durable state boundary.
type Store struct {
	Path string
}

// RepositoryRecord is the durable repository row.
type RepositoryRecord struct {
	ID              int64
	CanonicalPath   string
	ProtectedBranch string
	RemoteName      string
	MainWorktree    string
	PolicyVersion   string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// IntegrationSubmission is the durable submission row.
type IntegrationSubmission struct {
	ID             int64
	RepoID         int64
	BranchName     string
	SourceWorktree string
	SourceSHA      string
	RequestedBy    string
	Status         string
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// PublishRequest is the durable publish row.
type PublishRequest struct {
	ID           int64
	RepoID       int64
	TargetSHA    string
	Status       string
	SupersededBy sql.NullInt64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// EventRecord is the durable event row.
type EventRecord struct {
	ID        int64
	RepoID    int64
	ItemType  string
	ItemID    sql.NullInt64
	EventType string
	Payload   json.RawMessage
	CreatedAt time.Time
}

// NewStore returns a repo-local durable store.
func NewStore(path string) Store {
	return Store{Path: path}
}

// DefaultPath returns the default SQLite path under shared git storage.
func DefaultPath(gitDir string) string {
	return filepath.Join(gitDir, defaultDirName, defaultDBName)
}

// Exists reports whether the state database file already exists.
func (s Store) Exists() bool {
	if s.Path == "" {
		return false
	}

	info, err := os.Stat(s.Path)
	if err != nil {
		return false
	}

	return !info.IsDir()
}

// EnsureSchema creates the durable store schema if needed.
func (s Store) EnsureSchema(ctx context.Context) error {
	db, err := s.open()
	if err != nil {
		return err
	}
	defer db.Close()

	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("ensure state schema: %w", err)
	}

	return nil
}

// UpsertRepository inserts or updates the repository record.
func (s Store) UpsertRepository(ctx context.Context, record RepositoryRecord) (RepositoryRecord, error) {
	db, err := s.open()
	if err != nil {
		return RepositoryRecord{}, err
	}
	defer db.Close()

	if err := s.EnsureSchema(ctx); err != nil {
		return RepositoryRecord{}, err
	}

	row := db.QueryRowContext(ctx, `
		INSERT INTO repositories (
			canonical_path,
			protected_branch,
			remote_name,
			main_worktree_path,
			policy_version
		) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(canonical_path) DO UPDATE SET
			protected_branch = excluded.protected_branch,
			remote_name = excluded.remote_name,
			main_worktree_path = excluded.main_worktree_path,
			policy_version = excluded.policy_version,
			updated_at = CURRENT_TIMESTAMP
		RETURNING id, canonical_path, protected_branch, remote_name, main_worktree_path, policy_version, created_at, updated_at
	`,
		record.CanonicalPath,
		record.ProtectedBranch,
		record.RemoteName,
		record.MainWorktree,
		record.PolicyVersion,
	)

	return scanRepositoryRecord(row)
}

// GetRepositoryByPath returns a repository record by canonical path.
func (s Store) GetRepositoryByPath(ctx context.Context, canonicalPath string) (RepositoryRecord, error) {
	db, err := s.open()
	if err != nil {
		return RepositoryRecord{}, err
	}
	defer db.Close()

	row := db.QueryRowContext(ctx, `
		SELECT id, canonical_path, protected_branch, remote_name, main_worktree_path, policy_version, created_at, updated_at
		FROM repositories
		WHERE canonical_path = ?
	`, canonicalPath)

	record, err := scanRepositoryRecord(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RepositoryRecord{}, ErrNotFound
		}
		return RepositoryRecord{}, err
	}

	return record, nil
}

// CreateIntegrationSubmission inserts a submission row.
func (s Store) CreateIntegrationSubmission(ctx context.Context, submission IntegrationSubmission) (IntegrationSubmission, error) {
	db, err := s.open()
	if err != nil {
		return IntegrationSubmission{}, err
	}
	defer db.Close()

	row := db.QueryRowContext(ctx, `
		INSERT INTO integration_submissions (
			repo_id,
			branch_name,
			source_worktree_path,
			source_sha,
			requested_by,
			status,
			last_error
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		RETURNING id, repo_id, branch_name, source_worktree_path, source_sha, requested_by, status, last_error, created_at, updated_at
	`,
		submission.RepoID,
		submission.BranchName,
		submission.SourceWorktree,
		submission.SourceSHA,
		submission.RequestedBy,
		submission.Status,
		submission.LastError,
	)

	return scanIntegrationSubmission(row)
}

// GetIntegrationSubmission returns a submission by id.
func (s Store) GetIntegrationSubmission(ctx context.Context, submissionID int64) (IntegrationSubmission, error) {
	db, err := s.open()
	if err != nil {
		return IntegrationSubmission{}, err
	}
	defer db.Close()

	row := db.QueryRowContext(ctx, `
		SELECT id, repo_id, branch_name, source_worktree_path, source_sha, requested_by, status, last_error, created_at, updated_at
		FROM integration_submissions
		WHERE id = ?
	`, submissionID)

	submission, err := scanIntegrationSubmission(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return IntegrationSubmission{}, ErrNotFound
		}
		return IntegrationSubmission{}, err
	}

	return submission, nil
}

// NextQueuedIntegrationSubmission returns the oldest queued submission for a repo.
func (s Store) NextQueuedIntegrationSubmission(ctx context.Context, repoID int64) (IntegrationSubmission, error) {
	db, err := s.open()
	if err != nil {
		return IntegrationSubmission{}, err
	}
	defer db.Close()

	row := db.QueryRowContext(ctx, `
		SELECT id, repo_id, branch_name, source_worktree_path, source_sha, requested_by, status, last_error, created_at, updated_at
		FROM integration_submissions
		WHERE repo_id = ? AND status = 'queued'
		ORDER BY created_at ASC, id ASC
		LIMIT 1
	`, repoID)

	submission, err := scanIntegrationSubmission(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return IntegrationSubmission{}, ErrNotFound
		}
		return IntegrationSubmission{}, err
	}

	return submission, nil
}

// UpdateIntegrationSubmissionStatus updates submission state and error text.
func (s Store) UpdateIntegrationSubmissionStatus(ctx context.Context, submissionID int64, status string, lastError string) (IntegrationSubmission, error) {
	db, err := s.open()
	if err != nil {
		return IntegrationSubmission{}, err
	}
	defer db.Close()

	row := db.QueryRowContext(ctx, `
		UPDATE integration_submissions
		SET status = ?, last_error = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
		RETURNING id, repo_id, branch_name, source_worktree_path, source_sha, requested_by, status, last_error, created_at, updated_at
	`, status, lastError, submissionID)

	submission, err := scanIntegrationSubmission(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return IntegrationSubmission{}, ErrNotFound
		}
		return IntegrationSubmission{}, err
	}

	return submission, nil
}

// CreatePublishRequest inserts a publish request row.
func (s Store) CreatePublishRequest(ctx context.Context, request PublishRequest) (PublishRequest, error) {
	db, err := s.open()
	if err != nil {
		return PublishRequest{}, err
	}
	defer db.Close()

	row := db.QueryRowContext(ctx, `
		INSERT INTO publish_requests (
			repo_id,
			target_sha,
			status,
			superseded_by
		) VALUES (?, ?, ?, ?)
		RETURNING id, repo_id, target_sha, status, superseded_by, created_at, updated_at
	`,
		request.RepoID,
		request.TargetSHA,
		request.Status,
		request.SupersededBy,
	)

	return scanPublishRequest(row)
}

// GetPublishRequest returns a publish request by id.
func (s Store) GetPublishRequest(ctx context.Context, requestID int64) (PublishRequest, error) {
	db, err := s.open()
	if err != nil {
		return PublishRequest{}, err
	}
	defer db.Close()

	row := db.QueryRowContext(ctx, `
		SELECT id, repo_id, target_sha, status, superseded_by, created_at, updated_at
		FROM publish_requests
		WHERE id = ?
	`, requestID)

	request, err := scanPublishRequest(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PublishRequest{}, ErrNotFound
		}
		return PublishRequest{}, err
	}

	return request, nil
}

// LatestQueuedPublishRequest returns the newest queued publish request for a repo.
func (s Store) LatestQueuedPublishRequest(ctx context.Context, repoID int64) (PublishRequest, error) {
	db, err := s.open()
	if err != nil {
		return PublishRequest{}, err
	}
	defer db.Close()

	row := db.QueryRowContext(ctx, `
		SELECT id, repo_id, target_sha, status, superseded_by, created_at, updated_at
		FROM publish_requests
		WHERE repo_id = ? AND status = 'queued'
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, repoID)

	request, err := scanPublishRequest(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PublishRequest{}, ErrNotFound
		}
		return PublishRequest{}, err
	}

	return request, nil
}

// SupersedeOlderQueuedPublishRequests marks older queued requests as superseded.
func (s Store) SupersedeOlderQueuedPublishRequests(ctx context.Context, repoID int64, keepID int64) error {
	db, err := s.open()
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.ExecContext(ctx, `
		UPDATE publish_requests
		SET status = 'superseded', superseded_by = ?, updated_at = CURRENT_TIMESTAMP
		WHERE repo_id = ? AND status = 'queued' AND id <> ?
	`, keepID, repoID, keepID)
	return err
}

// UpdatePublishRequestStatus updates publish request state and superseded link.
func (s Store) UpdatePublishRequestStatus(ctx context.Context, requestID int64, status string, supersededBy sql.NullInt64) (PublishRequest, error) {
	db, err := s.open()
	if err != nil {
		return PublishRequest{}, err
	}
	defer db.Close()

	row := db.QueryRowContext(ctx, `
		UPDATE publish_requests
		SET status = ?, superseded_by = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
		RETURNING id, repo_id, target_sha, status, superseded_by, created_at, updated_at
	`, status, supersededBy, requestID)

	request, err := scanPublishRequest(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PublishRequest{}, ErrNotFound
		}
		return PublishRequest{}, err
	}

	return request, nil
}

// ListPublishRequests returns publish requests for a repo ordered by creation time.
func (s Store) ListPublishRequests(ctx context.Context, repoID int64) ([]PublishRequest, error) {
	db, err := s.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `
		SELECT id, repo_id, target_sha, status, superseded_by, created_at, updated_at
		FROM publish_requests
		WHERE repo_id = ?
		ORDER BY created_at ASC, id ASC
	`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var requests []PublishRequest
	for rows.Next() {
		request, err := scanPublishRequest(rows)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}

	return requests, rows.Err()
}

// AppendEvent inserts an event record.
func (s Store) AppendEvent(ctx context.Context, event EventRecord) (EventRecord, error) {
	db, err := s.open()
	if err != nil {
		return EventRecord{}, err
	}
	defer db.Close()

	row := db.QueryRowContext(ctx, `
		INSERT INTO events (
			repo_id,
			item_type,
			item_id,
			event_type,
			payload
		) VALUES (?, ?, ?, ?, ?)
		RETURNING id, repo_id, item_type, item_id, event_type, payload, created_at
	`,
		event.RepoID,
		event.ItemType,
		event.ItemID,
		event.EventType,
		[]byte(event.Payload),
	)

	return scanEventRecord(row)
}

// CountUnfinishedItems returns unfinished submission and publish counts.
func (s Store) CountUnfinishedItems(ctx context.Context, repoID int64) (int, error) {
	db, err := s.open()
	if err != nil {
		return 0, err
	}
	defer db.Close()

	var submissionCount int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM integration_submissions
		WHERE repo_id = ? AND status IN ('queued', 'running', 'blocked')
	`, repoID).Scan(&submissionCount); err != nil {
		return 0, err
	}

	var publishCount int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM publish_requests
		WHERE repo_id = ? AND status IN ('queued', 'running')
	`, repoID).Scan(&publishCount); err != nil {
		return 0, err
	}

	return submissionCount + publishCount, nil
}

func (s Store) open() (*sql.DB, error) {
	if s.Path == "" {
		return nil, fmt.Errorf("state path is empty")
	}

	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}

	db, err := sql.Open("sqlite", s.Path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite state: %w", err)
	}

	if _, err := db.Exec(`PRAGMA journal_mode = WAL;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure sqlite journal mode: %w", err)
	}

	return db, nil
}

type scanner interface {
	Scan(dest ...any) error
}

// NullInt64 returns a valid sql.NullInt64.
func NullInt64(v int64) sql.NullInt64 {
	return sql.NullInt64{Int64: v, Valid: true}
}

func scanRepositoryRecord(row scanner) (RepositoryRecord, error) {
	var record RepositoryRecord
	err := row.Scan(
		&record.ID,
		&record.CanonicalPath,
		&record.ProtectedBranch,
		&record.RemoteName,
		&record.MainWorktree,
		&record.PolicyVersion,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	return record, err
}

func scanIntegrationSubmission(row scanner) (IntegrationSubmission, error) {
	var submission IntegrationSubmission
	err := row.Scan(
		&submission.ID,
		&submission.RepoID,
		&submission.BranchName,
		&submission.SourceWorktree,
		&submission.SourceSHA,
		&submission.RequestedBy,
		&submission.Status,
		&submission.LastError,
		&submission.CreatedAt,
		&submission.UpdatedAt,
	)
	return submission, err
}

func scanPublishRequest(row scanner) (PublishRequest, error) {
	var request PublishRequest
	err := row.Scan(
		&request.ID,
		&request.RepoID,
		&request.TargetSHA,
		&request.Status,
		&request.SupersededBy,
		&request.CreatedAt,
		&request.UpdatedAt,
	)
	return request, err
}

func scanEventRecord(row scanner) (EventRecord, error) {
	var event EventRecord
	var payload []byte
	err := row.Scan(
		&event.ID,
		&event.RepoID,
		&event.ItemType,
		&event.ItemID,
		&event.EventType,
		&payload,
		&event.CreatedAt,
	)
	event.Payload = payload
	return event, err
}

// ListEvents returns recent events for a repo ordered by most recent first.
func (s Store) ListEvents(ctx context.Context, repoID int64, limit int) ([]EventRecord, error) {
	db, err := s.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	if limit <= 0 {
		limit = 20
	}

	rows, err := db.QueryContext(ctx, `
		SELECT id, repo_id, item_type, item_id, event_type, payload, created_at
		FROM events
		WHERE repo_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, repoID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []EventRecord
	for rows.Next() {
		event, err := scanEventRecord(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}

	return events, rows.Err()
}

// ListEventsAfter returns events newer than the provided event id ordered by creation time.
func (s Store) ListEventsAfter(ctx context.Context, repoID int64, afterID int64, limit int) ([]EventRecord, error) {
	db, err := s.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	if limit <= 0 {
		limit = 100
	}

	rows, err := db.QueryContext(ctx, `
		SELECT id, repo_id, item_type, item_id, event_type, payload, created_at
		FROM events
		WHERE repo_id = ? AND id > ?
		ORDER BY created_at ASC, id ASC
		LIMIT ?
	`, repoID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []EventRecord
	for rows.Next() {
		event, err := scanEventRecord(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}

	return events, rows.Err()
}

// ListIntegrationSubmissions returns submissions for a repo ordered by creation time.
func (s Store) ListIntegrationSubmissions(ctx context.Context, repoID int64) ([]IntegrationSubmission, error) {
	db, err := s.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `
		SELECT id, repo_id, branch_name, source_worktree_path, source_sha, requested_by, status, last_error, created_at, updated_at
		FROM integration_submissions
		WHERE repo_id = ?
		ORDER BY created_at ASC, id ASC
	`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var submissions []IntegrationSubmission
	for rows.Next() {
		submission, err := scanIntegrationSubmission(rows)
		if err != nil {
			return nil, err
		}
		submissions = append(submissions, submission)
	}

	return submissions, rows.Err()
}

var ErrNotFound = errors.New("state record not found")

const schemaSQL = `
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
`
