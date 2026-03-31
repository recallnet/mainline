package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
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
		SourceWorktree: repoRoot,
		SourceSHA:      "abc123",
		RequestedBy:    "tester",
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
