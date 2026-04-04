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
	"github.com/recallnet/mainline/internal/state/sqlcgen"
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

	repo, err := sqlcgen.New(db).UpsertRepository(ctx, sqlcgen.UpsertRepositoryParams{
		CanonicalPath:    record.CanonicalPath,
		ProtectedBranch:  record.ProtectedBranch,
		RemoteName:       record.RemoteName,
		MainWorktreePath: record.MainWorktree,
		PolicyVersion:    record.PolicyVersion,
	})
	if err != nil {
		return RepositoryRecord{}, err
	}
	return fromSQLCRepository(repo), nil
}

// GetRepositoryByPath returns a repository record by canonical path.
func (s Store) GetRepositoryByPath(ctx context.Context, canonicalPath string) (RepositoryRecord, error) {
	db, err := s.open()
	if err != nil {
		return RepositoryRecord{}, err
	}
	defer db.Close()

	record, err := sqlcgen.New(db).GetRepositoryByPath(ctx, canonicalPath)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RepositoryRecord{}, ErrNotFound
		}
		return RepositoryRecord{}, err
	}

	return fromSQLCRepository(record), nil
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

	created, err := sqlcgen.New(db).CreateIntegrationSubmission(ctx, sqlcgen.CreateIntegrationSubmissionParams{
		RepoID:             submission.RepoID,
		BranchName:         submission.BranchName,
		SourceRef:          submission.SourceRef,
		RefKind:            string(submission.RefKind),
		SourceWorktreePath: submission.SourceWorktree,
		SourceSha:          submission.SourceSHA,
		AllowNewerHead:     boolToInt64(submission.AllowNewerHead),
		RequestedBy:        submission.RequestedBy,
		Priority:           submission.Priority,
		Status:             string(submission.Status),
		LastError:          submission.LastError,
	})
	if err != nil {
		return IntegrationSubmission{}, err
	}
	return fromSQLCIntegrationSubmission(created), nil
}

// GetIntegrationSubmission returns a submission by id.
func (s Store) GetIntegrationSubmission(ctx context.Context, submissionID int64) (IntegrationSubmission, error) {
	db, err := s.open()
	if err != nil {
		return IntegrationSubmission{}, err
	}
	defer db.Close()

	submission, err := sqlcgen.New(db).GetIntegrationSubmission(ctx, submissionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return IntegrationSubmission{}, ErrNotFound
		}
		return IntegrationSubmission{}, err
	}

	return fromSQLCIntegrationSubmission(submission), nil
}

// NextQueuedIntegrationSubmission returns the oldest queued submission for a repo.
func (s Store) NextQueuedIntegrationSubmission(ctx context.Context, repoID int64) (IntegrationSubmission, error) {
	db, err := s.open()
	if err != nil {
		return IntegrationSubmission{}, err
	}
	defer db.Close()

	submission, err := sqlcgen.New(db).NextQueuedIntegrationSubmission(ctx, repoID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return IntegrationSubmission{}, ErrNotFound
		}
		return IntegrationSubmission{}, err
	}

	return fromSQLCIntegrationSubmission(submission), nil
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

	submission, err := sqlcgen.New(db).UpdateIntegrationSubmissionStatus(ctx, sqlcgen.UpdateIntegrationSubmissionStatusParams{
		Status:    string(status),
		LastError: lastError,
		ID:        submissionID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return IntegrationSubmission{}, ErrNotFound
		}
		return IntegrationSubmission{}, err
	}

	return fromSQLCIntegrationSubmission(submission), nil
}

// UpdateIntegrationSubmissionPriority updates queued submission priority.
func (s Store) UpdateIntegrationSubmissionPriority(ctx context.Context, submissionID int64, priority string) (IntegrationSubmission, error) {
	db, err := s.open()
	if err != nil {
		return IntegrationSubmission{}, err
	}
	defer db.Close()

	submission, err := sqlcgen.New(db).UpdateIntegrationSubmissionPriority(ctx, sqlcgen.UpdateIntegrationSubmissionPriorityParams{
		Priority: priority,
		ID:       submissionID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return IntegrationSubmission{}, ErrNotFound
		}
		return IntegrationSubmission{}, err
	}

	return fromSQLCIntegrationSubmission(submission), nil
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

	created, err := sqlcgen.New(db).CreatePublishRequest(ctx, sqlcgen.CreatePublishRequestParams{
		RepoID:        request.RepoID,
		TargetSha:     request.TargetSHA,
		Status:        string(request.Status),
		AttemptCount:  int64(request.AttemptCount),
		NextAttemptAt: request.NextAttemptAt,
		SupersededBy:  request.SupersededBy,
	})
	if err != nil {
		return PublishRequest{}, err
	}
	return fromSQLCPublishRequest(created), nil
}

// GetPublishRequest returns a publish request by id.
func (s Store) GetPublishRequest(ctx context.Context, requestID int64) (PublishRequest, error) {
	db, err := s.open()
	if err != nil {
		return PublishRequest{}, err
	}
	defer db.Close()

	request, err := sqlcgen.New(db).GetPublishRequest(ctx, requestID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PublishRequest{}, ErrNotFound
		}
		return PublishRequest{}, err
	}

	return fromSQLCPublishRequest(request), nil
}

// LatestQueuedPublishRequest returns the newest queued publish request for a repo.
func (s Store) LatestQueuedPublishRequest(ctx context.Context, repoID int64) (PublishRequest, error) {
	db, err := s.open()
	if err != nil {
		return PublishRequest{}, err
	}
	defer db.Close()

	request, err := sqlcgen.New(db).LatestQueuedPublishRequest(ctx, repoID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PublishRequest{}, ErrNotFound
		}
		return PublishRequest{}, err
	}

	return fromSQLCPublishRequest(request), nil
}

// LatestReadyQueuedPublishRequest returns the newest queued publish request ready to run now.
func (s Store) LatestReadyQueuedPublishRequest(ctx context.Context, repoID int64, now time.Time) (PublishRequest, error) {
	db, err := s.open()
	if err != nil {
		return PublishRequest{}, err
	}
	defer db.Close()

	request, err := sqlcgen.New(db).LatestReadyQueuedPublishRequest(ctx, sqlcgen.LatestReadyQueuedPublishRequestParams{
		RepoID: repoID,
		NowUtc: sql.NullTime{Time: now.UTC(), Valid: true},
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PublishRequest{}, ErrNotFound
		}
		return PublishRequest{}, err
	}

	return fromSQLCPublishRequest(request), nil
}

// NextDelayedQueuedPublishRequest returns the earliest delayed queued publish request for a repo.
func (s Store) NextDelayedQueuedPublishRequest(ctx context.Context, repoID int64, now time.Time) (PublishRequest, error) {
	db, err := s.open()
	if err != nil {
		return PublishRequest{}, err
	}
	defer db.Close()

	request, err := sqlcgen.New(db).NextDelayedQueuedPublishRequest(ctx, sqlcgen.NextDelayedQueuedPublishRequestParams{
		RepoID: repoID,
		NowUtc: sql.NullTime{Time: now.UTC(), Valid: true},
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PublishRequest{}, ErrNotFound
		}
		return PublishRequest{}, err
	}

	return fromSQLCPublishRequest(request), nil
}

// SupersedeOlderQueuedPublishRequests marks older queued requests as superseded.
func (s Store) SupersedeOlderQueuedPublishRequests(ctx context.Context, repoID int64, keepID int64) error {
	db, err := s.open()
	if err != nil {
		return err
	}
	defer db.Close()

	return sqlcgen.New(db).SupersedeOlderQueuedPublishRequests(ctx, sqlcgen.SupersedeOlderQueuedPublishRequestsParams{
		KeepID: sql.NullInt64{Int64: keepID, Valid: true},
		RepoID: repoID,
	})
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

	request, err := sqlcgen.New(db).UpdatePublishRequestStatus(ctx, sqlcgen.UpdatePublishRequestStatusParams{
		Status:       string(status),
		SupersededBy: supersededBy,
		ID:           requestID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PublishRequest{}, ErrNotFound
		}
		return PublishRequest{}, err
	}

	return fromSQLCPublishRequest(request), nil
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

	request, err := sqlcgen.New(db).SchedulePublishRetry(ctx, sqlcgen.SchedulePublishRetryParams{
		AttemptCount:  int64(attemptCount),
		NextAttemptAt: sql.NullTime{Time: nextAttemptAt.UTC(), Valid: true},
		ID:            requestID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PublishRequest{}, ErrNotFound
		}
		return PublishRequest{}, err
	}

	return fromSQLCPublishRequest(request), nil
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

	request, err := sqlcgen.New(db).ResetPublishRequestForRetry(ctx, requestID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PublishRequest{}, ErrNotFound
		}
		return PublishRequest{}, err
	}

	return fromSQLCPublishRequest(request), nil
}

// ListPublishRequests returns publish requests for a repo ordered by creation time.
func (s Store) ListPublishRequests(ctx context.Context, repoID int64) ([]PublishRequest, error) {
	db, err := s.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	requests, err := sqlcgen.New(db).ListPublishRequests(ctx, repoID)
	if err != nil {
		return nil, err
	}
	return mapPublishRequests(requests), nil
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

	created, err := sqlcgen.New(db).AppendEvent(ctx, sqlcgen.AppendEventParams{
		RepoID:    event.RepoID,
		ItemType:  string(event.ItemType),
		ItemID:    event.ItemID,
		EventType: string(event.EventType),
		Payload:   []byte(event.Payload),
	})
	if err != nil {
		return EventRecord{}, err
	}
	return fromSQLCEvent(created), nil
}

// CountUnfinishedItems returns unfinished submission and publish counts.
func (s Store) CountUnfinishedItems(ctx context.Context, repoID int64) (int, error) {
	db, err := s.open()
	if err != nil {
		return 0, err
	}
	defer db.Close()

	queries := sqlcgen.New(db)
	submissionCount, err := queries.CountUnfinishedIntegrationSubmissions(ctx, repoID)
	if err != nil {
		return 0, err
	}
	publishCount, err := queries.CountUnfinishedPublishRequests(ctx, repoID)
	if err != nil {
		return 0, err
	}

	return int(submissionCount + publishCount), nil
}

// CountQueuedIntegrationSubmissions returns the number of queued submissions for a repo.
func (s Store) CountQueuedIntegrationSubmissions(ctx context.Context, repoID int64) (int, error) {
	db, err := s.open()
	if err != nil {
		return 0, err
	}
	defer db.Close()

	count, err := sqlcgen.New(db).CountQueuedIntegrationSubmissions(ctx, repoID)
	if err != nil {
		return 0, err
	}

	return int(count), nil
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

// NullInt64 returns a valid sql.NullInt64.
func NullInt64(v int64) sql.NullInt64 {
	return sql.NullInt64{Int64: v, Valid: true}
}

func boolToInt64(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

func fromSQLCRepository(row sqlcgen.Repository) RepositoryRecord {
	return RepositoryRecord{
		ID:              row.ID,
		CanonicalPath:   row.CanonicalPath,
		ProtectedBranch: row.ProtectedBranch,
		RemoteName:      row.RemoteName,
		MainWorktree:    row.MainWorktreePath,
		PolicyVersion:   row.PolicyVersion,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}
}

func fromSQLCIntegrationSubmission(row sqlcgen.IntegrationSubmission) IntegrationSubmission {
	return IntegrationSubmission{
		ID:             row.ID,
		RepoID:         row.RepoID,
		BranchName:     row.BranchName,
		SourceRef:      row.SourceRef,
		RefKind:        domain.RefKind(row.RefKind),
		SourceWorktree: row.SourceWorktreePath,
		SourceSHA:      row.SourceSha,
		AllowNewerHead: row.AllowNewerHead != 0,
		RequestedBy:    row.RequestedBy,
		Priority:       row.Priority,
		Status:         domain.SubmissionStatus(row.Status),
		LastError:      row.LastError,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	}
}

func mapIntegrationSubmissions(rows []sqlcgen.IntegrationSubmission) []IntegrationSubmission {
	submissions := make([]IntegrationSubmission, 0, len(rows))
	for _, row := range rows {
		submissions = append(submissions, fromSQLCIntegrationSubmission(row))
	}
	return submissions
}

func fromSQLCPublishRequest(row sqlcgen.PublishRequest) PublishRequest {
	return PublishRequest{
		ID:            row.ID,
		RepoID:        row.RepoID,
		TargetSHA:     row.TargetSha,
		Status:        domain.PublishStatus(row.Status),
		AttemptCount:  int(row.AttemptCount),
		NextAttemptAt: row.NextAttemptAt,
		SupersededBy:  row.SupersededBy,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	}
}

func mapPublishRequests(rows []sqlcgen.PublishRequest) []PublishRequest {
	requests := make([]PublishRequest, 0, len(rows))
	for _, row := range rows {
		requests = append(requests, fromSQLCPublishRequest(row))
	}
	return requests
}

func fromSQLCEvent(row sqlcgen.Event) EventRecord {
	return EventRecord{
		ID:        row.ID,
		RepoID:    row.RepoID,
		ItemType:  domain.ItemType(row.ItemType),
		ItemID:    row.ItemID,
		EventType: domain.EventType(row.EventType),
		Payload:   append(json.RawMessage(nil), row.Payload...),
		CreatedAt: row.CreatedAt,
	}
}

func mapEvents(rows []sqlcgen.Event) []EventRecord {
	events := make([]EventRecord, 0, len(rows))
	for _, row := range rows {
		events = append(events, fromSQLCEvent(row))
	}
	return events
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

	events, err := sqlcgen.New(db).ListEvents(ctx, sqlcgen.ListEventsParams{
		RepoID:     repoID,
		LimitCount: int64(limit),
	})
	if err != nil {
		return nil, err
	}
	return mapEvents(events), nil
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

	events, err := sqlcgen.New(db).ListEventsForItemDesc(ctx, sqlcgen.ListEventsForItemDescParams{
		RepoID:     repoID,
		ItemType:   string(itemType),
		ItemID:     sql.NullInt64{Int64: itemID, Valid: true},
		LimitCount: int64(limit),
	})
	if err != nil {
		return nil, err
	}
	mapped := mapEvents(events)
	for left, right := 0, len(mapped)-1; left < right; left, right = left+1, right-1 {
		mapped[left], mapped[right] = mapped[right], mapped[left]
	}
	return mapped, nil
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

	events, err := sqlcgen.New(db).ListEventsAfter(ctx, sqlcgen.ListEventsAfterParams{
		RepoID:     repoID,
		AfterID:    afterID,
		LimitCount: int64(limit),
	})
	if err != nil {
		return nil, err
	}
	return mapEvents(events), nil
}

// ListIntegrationSubmissions returns submissions for a repo ordered by creation time.
func (s Store) ListIntegrationSubmissions(ctx context.Context, repoID int64) ([]IntegrationSubmission, error) {
	db, err := s.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	submissions, err := sqlcgen.New(db).ListIntegrationSubmissions(ctx, repoID)
	if err != nil {
		return nil, err
	}
	return mapIntegrationSubmissions(submissions), nil
}

// ListIntegrationSubmissionsByStatus returns submissions for a repo filtered by status.
func (s Store) ListIntegrationSubmissionsByStatus(ctx context.Context, repoID int64, status domain.SubmissionStatus) ([]IntegrationSubmission, error) {
	db, err := s.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	submissions, err := sqlcgen.New(db).ListIntegrationSubmissionsByStatus(ctx, sqlcgen.ListIntegrationSubmissionsByStatusParams{
		RepoID: repoID,
		Status: string(status),
	})
	if err != nil {
		return nil, err
	}
	return mapIntegrationSubmissions(submissions), nil
}

// ListPublishRequestsByStatus returns publish requests for a repo filtered by status.
func (s Store) ListPublishRequestsByStatus(ctx context.Context, repoID int64, status domain.PublishStatus) ([]PublishRequest, error) {
	db, err := s.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	requests, err := sqlcgen.New(db).ListPublishRequestsByStatus(ctx, sqlcgen.ListPublishRequestsByStatusParams{
		RepoID: repoID,
		Status: string(status),
	})
	if err != nil {
		return nil, err
	}
	return mapPublishRequests(requests), nil
}

var ErrNotFound = errors.New("state record not found")
