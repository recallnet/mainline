package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStorePersistsAcrossRestarts(t *testing.T) {
	repoRoot := t.TempDir()
	gitDir := filepath.Join(repoRoot, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	store := NewStore(DefaultPath(gitDir))
	ctx := context.Background()

	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	repo, err := store.UpsertRepository(ctx, RepositoryRecord{
		CanonicalPath:   repoRoot,
		ProtectedBranch: "main",
		RemoteName:      "origin",
		MainWorktree:    repoRoot,
		PolicyVersion:   "v1",
	})
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}

	if _, err := store.CreateIntegrationSubmission(ctx, IntegrationSubmission{
		RepoID:         repo.ID,
		BranchName:     "feature/test",
		SourceRef:      "feature/test",
		RefKind:        "branch",
		SourceWorktree: repoRoot,
		SourceSHA:      "abc123",
		RequestedBy:    "tester",
		Priority:       "normal",
		Status:         "queued",
	}); err != nil {
		t.Fatalf("CreateIntegrationSubmission: %v", err)
	}

	store = NewStore(DefaultPath(gitDir))
	found, err := store.GetRepositoryByPath(ctx, repoRoot)
	if err != nil {
		t.Fatalf("GetRepositoryByPath: %v", err)
	}
	if found.ProtectedBranch != "main" {
		t.Fatalf("expected protected branch main, got %q", found.ProtectedBranch)
	}

	count, err := store.CountUnfinishedItems(ctx, found.ID)
	if err != nil {
		t.Fatalf("CountUnfinishedItems: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 unfinished item, got %d", count)
	}
}

func TestEnsureSchemaMigratesLegacyVersionOnePriorityColumn(t *testing.T) {
	repoRoot := t.TempDir()
	gitDir := filepath.Join(repoRoot, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	store := NewStore(DefaultPath(gitDir))
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Dir(store.Path), 0o755); err != nil {
		t.Fatalf("MkdirAll(state dir): %v", err)
	}

	db, err := sql.Open("sqlite", store.Path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE repositories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			canonical_path TEXT NOT NULL UNIQUE,
			protected_branch TEXT NOT NULL,
			remote_name TEXT NOT NULL,
			main_worktree_path TEXT NOT NULL,
			policy_version TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE integration_submissions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			repo_id INTEGER NOT NULL,
			branch_name TEXT NOT NULL,
			source_worktree_path TEXT NOT NULL,
			source_sha TEXT NOT NULL,
			requested_by TEXT NOT NULL,
			status TEXT NOT NULL,
			last_error TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE publish_requests (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			repo_id INTEGER NOT NULL,
			target_sha TEXT NOT NULL,
			status TEXT NOT NULL,
			superseded_by INTEGER,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			repo_id INTEGER NOT NULL,
			item_type TEXT NOT NULL,
			item_id INTEGER,
			event_type TEXT NOT NULL,
			payload BLOB NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`); err != nil {
		t.Fatalf("create legacy v1 schema: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 1;`); err != nil {
		t.Fatalf("set user_version: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO repositories (canonical_path, protected_branch, remote_name, main_worktree_path, policy_version)
		VALUES (?, 'main', 'origin', ?, 'v1')
	`, repoRoot, repoRoot); err != nil {
		t.Fatalf("insert repository: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO integration_submissions (repo_id, branch_name, source_worktree_path, source_sha, requested_by, status, last_error)
		VALUES (1, 'feature/test', ?, 'abc123', 'tester', 'queued', '')
	`, repoRoot); err != nil {
		t.Fatalf("insert integration submission: %v", err)
	}

	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	submission, err := store.GetIntegrationSubmission(ctx, 1)
	if err != nil {
		t.Fatalf("GetIntegrationSubmission: %v", err)
	}
	if submission.Priority != "normal" {
		t.Fatalf("expected migrated priority normal, got %q", submission.Priority)
	}
	if submission.SourceRef != "feature/test" || submission.RefKind != "branch" {
		t.Fatalf("expected migrated source ref metadata, got %+v", submission)
	}
}

func TestEnsureSchemaMigratesLegacyVersionThreePublishRetryColumns(t *testing.T) {
	repoRoot := t.TempDir()
	gitDir := filepath.Join(repoRoot, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	store := NewStore(DefaultPath(gitDir))
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Dir(store.Path), 0o755); err != nil {
		t.Fatalf("MkdirAll(state dir): %v", err)
	}

	db, err := sql.Open("sqlite", store.Path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE repositories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			canonical_path TEXT NOT NULL UNIQUE,
			protected_branch TEXT NOT NULL,
			remote_name TEXT NOT NULL,
			main_worktree_path TEXT NOT NULL,
			policy_version TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE integration_submissions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			repo_id INTEGER NOT NULL,
			branch_name TEXT NOT NULL,
			source_ref TEXT NOT NULL DEFAULT '',
			ref_kind TEXT NOT NULL DEFAULT 'branch',
			source_worktree_path TEXT NOT NULL,
			source_sha TEXT NOT NULL,
			requested_by TEXT NOT NULL,
			priority TEXT NOT NULL DEFAULT 'normal',
			status TEXT NOT NULL,
			last_error TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE publish_requests (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			repo_id INTEGER NOT NULL,
			target_sha TEXT NOT NULL,
			status TEXT NOT NULL,
			superseded_by INTEGER,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			repo_id INTEGER NOT NULL,
			item_type TEXT NOT NULL,
			item_id INTEGER,
			event_type TEXT NOT NULL,
			payload BLOB NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`); err != nil {
		t.Fatalf("create legacy v3 schema: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 3;`); err != nil {
		t.Fatalf("set user_version: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO repositories (canonical_path, protected_branch, remote_name, main_worktree_path, policy_version)
		VALUES (?, 'main', 'origin', ?, 'v1')
	`, repoRoot, repoRoot); err != nil {
		t.Fatalf("insert repository: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO publish_requests (repo_id, target_sha, status, superseded_by)
		VALUES (1, 'abc123', 'queued', NULL)
	`); err != nil {
		t.Fatalf("insert publish request: %v", err)
	}

	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	request, err := store.GetPublishRequest(ctx, 1)
	if err != nil {
		t.Fatalf("GetPublishRequest: %v", err)
	}
	if request.AttemptCount != 0 {
		t.Fatalf("expected migrated publish attempt_count 0, got %+v", request)
	}
	if request.NextAttemptAt.Valid {
		t.Fatalf("expected migrated publish next_attempt_at to be null, got %+v", request.NextAttemptAt)
	}
}

func TestEnsureSchemaMigratesLegacyVersionFivePublishPriority(t *testing.T) {
	repoRoot := t.TempDir()
	gitDir := filepath.Join(repoRoot, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	store := NewStore(DefaultPath(gitDir))
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Dir(store.Path), 0o755); err != nil {
		t.Fatalf("MkdirAll(state dir): %v", err)
	}

	db, err := sql.Open("sqlite", store.Path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE repositories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			canonical_path TEXT NOT NULL UNIQUE,
			protected_branch TEXT NOT NULL,
			remote_name TEXT NOT NULL,
			main_worktree_path TEXT NOT NULL,
			policy_version TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE integration_submissions (
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
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE publish_requests (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			repo_id INTEGER NOT NULL,
			target_sha TEXT NOT NULL,
			status TEXT NOT NULL,
			attempt_count INTEGER NOT NULL DEFAULT 0,
			next_attempt_at DATETIME,
			superseded_by INTEGER,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			repo_id INTEGER NOT NULL,
			item_type TEXT NOT NULL,
			item_id INTEGER,
			event_type TEXT NOT NULL,
			payload BLOB NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`); err != nil {
		t.Fatalf("create legacy v5 schema: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 5;`); err != nil {
		t.Fatalf("set user_version: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO publish_requests (repo_id, target_sha, status, attempt_count, next_attempt_at, superseded_by)
		VALUES (1, 'abc123', 'queued', 0, NULL, NULL)
	`); err != nil {
		t.Fatalf("insert publish request: %v", err)
	}

	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	request, err := store.GetPublishRequest(ctx, 1)
	if err != nil {
		t.Fatalf("GetPublishRequest: %v", err)
	}
	if request.Priority != "normal" {
		t.Fatalf("expected migrated publish priority normal, got %+v", request)
	}
}

func TestLockManagerEnforcesExclusivityAndReportsStale(t *testing.T) {
	repoRoot := t.TempDir()
	gitDir := filepath.Join(repoRoot, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	manager := NewLockManager(repoRoot, gitDir)

	lease, err := manager.Acquire(IntegrationLock, "test-owner")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lease.Release()

	if _, err := manager.Acquire(IntegrationLock, "other-owner"); err != ErrLockHeld {
		t.Fatalf("expected ErrLockHeld, got %v", err)
	}

	metaPath := filepath.Join(gitDir, defaultDirName, "locks", IntegrationLock+".lock.json")
	payload, err := json.Marshal(LeaseMetadata{
		Domain:    IntegrationLock,
		RepoRoot:  repoRoot,
		Owner:     "test-owner",
		CreatedAt: time.Now().UTC().Add(-2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(metaPath, payload, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	stale, err := manager.InspectStale(time.Hour)
	if err != nil {
		t.Fatalf("InspectStale: %v", err)
	}
	if len(stale) != 1 {
		t.Fatalf("expected 1 stale lease, got %d", len(stale))
	}
}

func TestAppendEventRoundTrips(t *testing.T) {
	repoRoot := t.TempDir()
	gitDir := filepath.Join(repoRoot, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	store := NewStore(DefaultPath(gitDir))
	ctx := context.Background()

	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	repo, err := store.UpsertRepository(ctx, RepositoryRecord{
		CanonicalPath:   repoRoot,
		ProtectedBranch: "main",
		RemoteName:      "origin",
		MainWorktree:    repoRoot,
		PolicyVersion:   "v1",
	})
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}

	event, err := store.AppendEvent(ctx, EventRecord{
		RepoID:    repo.ID,
		ItemType:  "repository",
		ItemID:    sql.NullInt64{},
		EventType: "repository.initialized",
		Payload:   json.RawMessage(`{"protected_branch":"main"}`),
	})
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	if event.EventType != "repository.initialized" {
		t.Fatalf("unexpected event type %q", event.EventType)
	}
}

func TestEnsureSchemaMigratesLegacyVersionZeroState(t *testing.T) {
	repoRoot := t.TempDir()
	gitDir := filepath.Join(repoRoot, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	store := NewStore(DefaultPath(gitDir))
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Dir(store.Path), 0o755); err != nil {
		t.Fatalf("MkdirAll(state dir): %v", err)
	}

	db, err := sql.Open("sqlite", store.Path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(schemaSQL); err != nil {
		t.Fatalf("Exec schemaSQL: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 0;`); err != nil {
		t.Fatalf("reset user_version: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO repositories (canonical_path, protected_branch, remote_name, main_worktree_path, policy_version)
		VALUES (?, 'main', 'origin', ?, 'v1')
	`, repoRoot, repoRoot); err != nil {
		t.Fatalf("insert repository: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO integration_submissions (repo_id, branch_name, source_worktree_path, source_sha, requested_by, status, last_error)
		VALUES (1, 'feature/test', ?, 'abc123', 'tester', 'queued', '')
	`, repoRoot); err != nil {
		t.Fatalf("insert integration submission: %v", err)
	}

	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	version, err := schemaVersion(ctx, db)
	if err != nil {
		t.Fatalf("schemaVersion: %v", err)
	}
	if version != currentSchemaVersion {
		t.Fatalf("expected schema version %d, got %d", currentSchemaVersion, version)
	}

	found, err := store.GetRepositoryByPath(ctx, repoRoot)
	if err != nil {
		t.Fatalf("GetRepositoryByPath: %v", err)
	}
	if found.CanonicalPath != repoRoot {
		t.Fatalf("expected repository %q, got %q", repoRoot, found.CanonicalPath)
	}
	count, err := store.CountUnfinishedItems(ctx, found.ID)
	if err != nil {
		t.Fatalf("CountUnfinishedItems: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 unfinished item after migration, got %d", count)
	}
}

func TestStoreRejectsFutureSchemaVersion(t *testing.T) {
	repoRoot := t.TempDir()
	gitDir := filepath.Join(repoRoot, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	store := NewStore(DefaultPath(gitDir))
	if err := os.MkdirAll(filepath.Dir(store.Path), 0o755); err != nil {
		t.Fatalf("MkdirAll(state dir): %v", err)
	}

	db, err := sql.Open("sqlite", store.Path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 999;`); err != nil {
		t.Fatalf("set future user_version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	err = store.EnsureSchema(context.Background())
	if err == nil {
		t.Fatalf("expected future schema version rejection")
	}
	if !errors.Is(err, ErrUnsupportedSchemaVersion) {
		t.Fatalf("expected ErrUnsupportedSchemaVersion, got %v", err)
	}
	if !strings.Contains(err.Error(), "supports up to") {
		t.Fatalf("expected actionable version error, got %v", err)
	}
}
