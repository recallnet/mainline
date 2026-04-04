package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/recallnet/mainline/internal/domain"
	_ "modernc.org/sqlite"
)

const (
	defaultDirName       = "mainline"
	defaultDBName        = "state.db"
	currentSchemaVersion = 5
)

var ErrUnsupportedSchemaVersion = errors.New("unsupported state schema version")

// Store describes the durable state boundary.
type Store struct {
	Path string
}

// RepositoryRecord is the durable repository row.
type RepositoryRecord struct {
	ID              int64     `json:"id"`
	CanonicalPath   string    `json:"canonical_path"`
	ProtectedBranch string    `json:"protected_branch"`
	RemoteName      string    `json:"remote_name"`
	MainWorktree    string    `json:"main_worktree_path"`
	PolicyVersion   string    `json:"policy_version"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// IntegrationSubmission is the durable submission row.
type IntegrationSubmission struct {
	ID             int64                   `json:"id"`
	RepoID         int64                   `json:"repo_id"`
	BranchName     string                  `json:"branch_name"`
	SourceRef      string                  `json:"source_ref"`
	RefKind        domain.RefKind          `json:"ref_kind"`
	SourceWorktree string                  `json:"source_worktree_path"`
	SourceSHA      string                  `json:"source_sha"`
	AllowNewerHead bool                    `json:"allow_newer_head"`
	RequestedBy    string                  `json:"requested_by"`
	Priority       string                  `json:"priority"`
	Status         domain.SubmissionStatus `json:"status"`
	LastError      string                  `json:"last_error"`
	CreatedAt      time.Time               `json:"created_at"`
	UpdatedAt      time.Time               `json:"updated_at"`
}

// PublishRequest is the durable publish row.
type PublishRequest struct {
	ID            int64                `json:"id"`
	RepoID        int64                `json:"repo_id"`
	TargetSHA     string               `json:"target_sha"`
	Status        domain.PublishStatus `json:"status"`
	AttemptCount  int                  `json:"attempt_count"`
	NextAttemptAt sql.NullTime         `json:"next_attempt_at"`
	SupersededBy  sql.NullInt64        `json:"superseded_by"`
	CreatedAt     time.Time            `json:"created_at"`
	UpdatedAt     time.Time            `json:"updated_at"`
}

// EventRecord is the durable event row.
type EventRecord struct {
	ID        int64            `json:"id"`
	RepoID    int64            `json:"repo_id"`
	ItemType  domain.ItemType  `json:"item_type"`
	ItemID    sql.NullInt64    `json:"item_id"`
	EventType domain.EventType `json:"event_type"`
	Payload   json.RawMessage  `json:"payload"`
	CreatedAt time.Time        `json:"created_at"`
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

	if _, err := db.Exec(`PRAGMA journal_mode = WAL;`); err != nil {
		return fmt.Errorf("configure sqlite journal mode: %w", err)
	}
	return ensureSchemaVersion(ctx, db)
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
	if err := applyTestFault("CreateIntegrationSubmission"); err != nil {
		return IntegrationSubmission{}, err
	}
	if submission.Priority == "" {
		submission.Priority = "normal"
	}
	if submission.SourceRef == "" {
		if submission.BranchName != "" {
			submission.SourceRef = submission.BranchName
			submission.RefKind = domain.RefKindBranch
		} else {
			submission.SourceRef = submission.SourceSHA
			submission.RefKind = domain.RefKindSHA
		}
	}
	db, err := s.open()
	if err != nil {
		return IntegrationSubmission{}, err
	}
	defer db.Close()

	row := db.QueryRowContext(ctx, `
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
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING id, repo_id, branch_name, source_ref, ref_kind, source_worktree_path, source_sha, allow_newer_head, requested_by, priority, status, last_error, created_at, updated_at
	`,
		submission.RepoID,
		submission.BranchName,
		submission.SourceRef,
		submission.RefKind,
		submission.SourceWorktree,
		submission.SourceSHA,
		submission.AllowNewerHead,
		submission.RequestedBy,
		submission.Priority,
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
		SELECT id, repo_id, branch_name, source_ref, ref_kind, source_worktree_path, source_sha, allow_newer_head, requested_by, priority, status, last_error, created_at, updated_at
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
		SELECT id, repo_id, branch_name, source_ref, ref_kind, source_worktree_path, source_sha, allow_newer_head, requested_by, priority, status, last_error, created_at, updated_at
		FROM integration_submissions
		WHERE repo_id = ? AND status = 'queued'
		ORDER BY
			CASE priority
				WHEN 'high' THEN 0
				WHEN 'normal' THEN 1
				WHEN 'low' THEN 2
				ELSE 1
			END ASC,
			created_at ASC,
			id ASC
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
func (s Store) UpdateIntegrationSubmissionStatus(ctx context.Context, submissionID int64, status domain.SubmissionStatus, lastError string) (IntegrationSubmission, error) {
	if err := applyTestFault("UpdateIntegrationSubmissionStatus"); err != nil {
		return IntegrationSubmission{}, err
	}
	db, err := s.open()
	if err != nil {
		return IntegrationSubmission{}, err
	}
	defer db.Close()

	row := db.QueryRowContext(ctx, `
		UPDATE integration_submissions
		SET status = ?, last_error = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
		RETURNING id, repo_id, branch_name, source_ref, ref_kind, source_worktree_path, source_sha, allow_newer_head, requested_by, priority, status, last_error, created_at, updated_at
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

// UpdateIntegrationSubmissionPriority updates queued submission priority.
func (s Store) UpdateIntegrationSubmissionPriority(ctx context.Context, submissionID int64, priority string) (IntegrationSubmission, error) {
	db, err := s.open()
	if err != nil {
		return IntegrationSubmission{}, err
	}
	defer db.Close()

	row := db.QueryRowContext(ctx, `
		UPDATE integration_submissions
		SET priority = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
		RETURNING id, repo_id, branch_name, source_ref, ref_kind, source_worktree_path, source_sha, allow_newer_head, requested_by, priority, status, last_error, created_at, updated_at
	`, priority, submissionID)

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
	if err := applyTestFault("CreatePublishRequest"); err != nil {
		return PublishRequest{}, err
	}
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
			attempt_count,
			next_attempt_at,
			superseded_by
		) VALUES (?, ?, ?, ?, ?, ?)
		RETURNING id, repo_id, target_sha, status, attempt_count, next_attempt_at, superseded_by, created_at, updated_at
	`,
		request.RepoID,
		request.TargetSHA,
		request.Status,
		request.AttemptCount,
		request.NextAttemptAt,
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
		SELECT id, repo_id, target_sha, status, attempt_count, next_attempt_at, superseded_by, created_at, updated_at
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
		SELECT id, repo_id, target_sha, status, attempt_count, next_attempt_at, superseded_by, created_at, updated_at
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

// LatestReadyQueuedPublishRequest returns the newest queued publish request ready to run now.
func (s Store) LatestReadyQueuedPublishRequest(ctx context.Context, repoID int64, now time.Time) (PublishRequest, error) {
	db, err := s.open()
	if err != nil {
		return PublishRequest{}, err
	}
	defer db.Close()

	row := db.QueryRowContext(ctx, `
		SELECT id, repo_id, target_sha, status, attempt_count, next_attempt_at, superseded_by, created_at, updated_at
		FROM publish_requests
		WHERE repo_id = ? AND status = 'queued' AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, repoID, now.UTC())

	request, err := scanPublishRequest(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PublishRequest{}, ErrNotFound
		}
		return PublishRequest{}, err
	}

	return request, nil
}

// NextDelayedQueuedPublishRequest returns the earliest delayed queued publish request for a repo.
func (s Store) NextDelayedQueuedPublishRequest(ctx context.Context, repoID int64, now time.Time) (PublishRequest, error) {
	db, err := s.open()
	if err != nil {
		return PublishRequest{}, err
	}
	defer db.Close()

	row := db.QueryRowContext(ctx, `
		SELECT id, repo_id, target_sha, status, attempt_count, next_attempt_at, superseded_by, created_at, updated_at
		FROM publish_requests
		WHERE repo_id = ? AND status = 'queued' AND next_attempt_at IS NOT NULL AND next_attempt_at > ?
		ORDER BY next_attempt_at ASC, id ASC
		LIMIT 1
	`, repoID, now.UTC())

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
func (s Store) UpdatePublishRequestStatus(ctx context.Context, requestID int64, status domain.PublishStatus, supersededBy sql.NullInt64) (PublishRequest, error) {
	if err := applyTestFault("UpdatePublishRequestStatus"); err != nil {
		return PublishRequest{}, err
	}
	db, err := s.open()
	if err != nil {
		return PublishRequest{}, err
	}
	defer db.Close()

	row := db.QueryRowContext(ctx, `
		UPDATE publish_requests
		SET status = ?, superseded_by = ?, next_attempt_at = NULL, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
		RETURNING id, repo_id, target_sha, status, attempt_count, next_attempt_at, superseded_by, created_at, updated_at
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

// SchedulePublishRetry requeues a publish request for a later retry attempt.
func (s Store) SchedulePublishRetry(ctx context.Context, requestID int64, attemptCount int, nextAttemptAt time.Time) (PublishRequest, error) {
	if err := applyTestFault("UpdatePublishRequestStatus"); err != nil {
		return PublishRequest{}, err
	}
	db, err := s.open()
	if err != nil {
		return PublishRequest{}, err
	}
	defer db.Close()

	row := db.QueryRowContext(ctx, `
		UPDATE publish_requests
		SET status = 'queued', attempt_count = ?, next_attempt_at = ?, superseded_by = NULL, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
		RETURNING id, repo_id, target_sha, status, attempt_count, next_attempt_at, superseded_by, created_at, updated_at
	`, attemptCount, nextAttemptAt.UTC(), requestID)

	request, err := scanPublishRequest(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PublishRequest{}, ErrNotFound
		}
		return PublishRequest{}, err
	}

	return request, nil
}

// ResetPublishRequestForRetry clears delayed retry state and manual retry budget exhaustion.
func (s Store) ResetPublishRequestForRetry(ctx context.Context, requestID int64) (PublishRequest, error) {
	if err := applyTestFault("UpdatePublishRequestStatus"); err != nil {
		return PublishRequest{}, err
	}
	db, err := s.open()
	if err != nil {
		return PublishRequest{}, err
	}
	defer db.Close()

	row := db.QueryRowContext(ctx, `
		UPDATE publish_requests
		SET status = 'queued', attempt_count = 0, next_attempt_at = NULL, superseded_by = NULL, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
		RETURNING id, repo_id, target_sha, status, attempt_count, next_attempt_at, superseded_by, created_at, updated_at
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

// ListPublishRequests returns publish requests for a repo ordered by creation time.
func (s Store) ListPublishRequests(ctx context.Context, repoID int64) ([]PublishRequest, error) {
	db, err := s.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `
		SELECT id, repo_id, target_sha, status, attempt_count, next_attempt_at, superseded_by, created_at, updated_at
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
	if err := applyTestFault("AppendEvent"); err != nil {
		return EventRecord{}, err
	}
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

// CountQueuedIntegrationSubmissions returns the number of queued submissions for a repo.
func (s Store) CountQueuedIntegrationSubmissions(ctx context.Context, repoID int64) (int, error) {
	db, err := s.open()
	if err != nil {
		return 0, err
	}
	defer db.Close()

	var count int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM integration_submissions
		WHERE repo_id = ? AND status = 'queued'
	`, repoID).Scan(&count); err != nil {
		return 0, err
	}

	return count, nil
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
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure sqlite busy timeout: %w", err)
	}
	version, err := schemaVersion(db)
	if err != nil {
		db.Close()
		return nil, err
	}
	if version > currentSchemaVersion {
		db.Close()
		return nil, fmt.Errorf("%w at %s: found version %d, binary supports up to %d", ErrUnsupportedSchemaVersion, s.Path, version, currentSchemaVersion)
	}

	return db, nil
}

func schemaVersion(db *sql.DB) (int, error) {
	var version int
	if err := db.QueryRow(`PRAGMA user_version;`).Scan(&version); err != nil {
		return 0, fmt.Errorf("read sqlite schema version: %w", err)
	}
	return version, nil
}

func setSchemaVersion(ctx context.Context, db *sql.DB, version int) error {
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d;`, version)); err != nil {
		return fmt.Errorf("set sqlite schema version: %w", err)
	}
	return nil
}

func ensureSchemaVersion(ctx context.Context, db *sql.DB) error {
	version, err := schemaVersion(db)
	if err != nil {
		return err
	}
	if version > currentSchemaVersion {
		return fmt.Errorf("%w: found version %d, binary supports up to %d", ErrUnsupportedSchemaVersion, version, currentSchemaVersion)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin schema transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("ensure state schema: %w", err)
	}
	migrated := false
	if version < 2 {
		if _, err := tx.ExecContext(ctx, `
			ALTER TABLE integration_submissions
			ADD COLUMN priority TEXT NOT NULL DEFAULT 'normal'
		`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return fmt.Errorf("add integration submission priority column: %w", err)
		}
		version = 2
		migrated = true
	}
	if version < 3 {
		if _, err := tx.ExecContext(ctx, `
			ALTER TABLE integration_submissions
			ADD COLUMN source_ref TEXT NOT NULL DEFAULT ''
		`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return fmt.Errorf("add integration submission source_ref column: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			ALTER TABLE integration_submissions
			ADD COLUMN ref_kind TEXT NOT NULL DEFAULT 'branch'
		`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return fmt.Errorf("add integration submission ref_kind column: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE integration_submissions
			SET source_ref = CASE
				WHEN source_ref <> '' THEN source_ref
				WHEN branch_name <> '' THEN branch_name
				ELSE source_sha
			END,
			    ref_kind = CASE
				WHEN ref_kind <> '' THEN ref_kind
				WHEN branch_name <> '' THEN 'branch'
				ELSE 'sha'
			END
		`); err != nil {
			return fmt.Errorf("backfill integration submission source refs: %w", err)
		}
		version = 3
		migrated = true
	}
	if version < 4 {
		if _, err := tx.ExecContext(ctx, `
			ALTER TABLE publish_requests
			ADD COLUMN attempt_count INTEGER NOT NULL DEFAULT 0
		`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return fmt.Errorf("add publish request attempt_count column: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			ALTER TABLE publish_requests
			ADD COLUMN next_attempt_at DATETIME
		`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return fmt.Errorf("add publish request next_attempt_at column: %w", err)
		}
		version = 4
		migrated = true
	}
	if version < 5 {
		if _, err := tx.ExecContext(ctx, `
			ALTER TABLE integration_submissions
			ADD COLUMN allow_newer_head INTEGER NOT NULL DEFAULT 0
		`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return fmt.Errorf("add integration submission allow_newer_head column: %w", err)
		}
		version = 5
		migrated = true
	}
	if migrated && version < currentSchemaVersion {
		version = currentSchemaVersion
	}
	if migrated {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d;`, version)); err != nil {
			return fmt.Errorf("set sqlite schema version: %w", err)
		}
	}
	if !migrated && version == 0 {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d;`, currentSchemaVersion)); err != nil {
			return fmt.Errorf("set sqlite schema version: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema transaction: %w", err)
	}

	return nil
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
		&submission.SourceRef,
		&submission.RefKind,
		&submission.SourceWorktree,
		&submission.SourceSHA,
		&submission.AllowNewerHead,
		&submission.RequestedBy,
		&submission.Priority,
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
		&request.AttemptCount,
		&request.NextAttemptAt,
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

// ListEventsForItem returns recent events for a specific durable item.
func (s Store) ListEventsForItem(ctx context.Context, repoID int64, itemType string, itemID int64, limit int) ([]EventRecord, error) {
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
		WHERE repo_id = ? AND item_type = ? AND item_id = ?
		ORDER BY id DESC
		LIMIT ?
	`, repoID, itemType, itemID, limit)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for left, right := 0, len(events)-1; left < right; left, right = left+1, right-1 {
		events[left], events[right] = events[right], events[left]
	}
	return events, nil
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
		SELECT id, repo_id, branch_name, source_ref, ref_kind, source_worktree_path, source_sha, allow_newer_head, requested_by, priority, status, last_error, created_at, updated_at
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

// ListIntegrationSubmissionsByStatus returns submissions for a repo filtered by status.
func (s Store) ListIntegrationSubmissionsByStatus(ctx context.Context, repoID int64, status domain.SubmissionStatus) ([]IntegrationSubmission, error) {
	db, err := s.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `
		SELECT id, repo_id, branch_name, source_ref, ref_kind, source_worktree_path, source_sha, allow_newer_head, requested_by, priority, status, last_error, created_at, updated_at
		FROM integration_submissions
		WHERE repo_id = ? AND status = ?
		ORDER BY created_at ASC, id ASC
	`, repoID, status)
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

// ListPublishRequestsByStatus returns publish requests for a repo filtered by status.
func (s Store) ListPublishRequestsByStatus(ctx context.Context, repoID int64, status domain.PublishStatus) ([]PublishRequest, error) {
	db, err := s.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `
		SELECT id, repo_id, target_sha, status, attempt_count, next_attempt_at, superseded_by, created_at, updated_at
		FROM publish_requests
		WHERE repo_id = ? AND status = ?
		ORDER BY created_at ASC, id ASC
	`, repoID, status)
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
`
