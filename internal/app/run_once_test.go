package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/policy"
	"github.com/recallnet/mainline/internal/state"
)

func TestRunOnceIntegratesQueuedBranchesInOrder(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	featureOne := filepath.Join(t.TempDir(), "feature-one")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/one", featureOne)
	writeFileAndCommit(t, featureOne, "one.txt", "feature one\n", "feature one")
	submitBranch(t, featureOne)

	featureTwo := filepath.Join(t.TempDir(), "feature-two")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/two", featureTwo)
	writeFileAndCommit(t, featureTwo, "two.txt", "feature two\n", "feature two")
	submitBranch(t, featureTwo)

	runOnce(t, repoRoot)
	runOnce(t, repoRoot)

	if _, err := os.Stat(filepath.Join(repoRoot, "one.txt")); err != nil {
		t.Fatalf("expected one.txt after integration: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "two.txt")); err != nil {
		t.Fatalf("expected two.txt after integration: %v", err)
	}

	logOutput := runTestCommand(t, repoRoot, "git", "log", "--format=%s", "-2")
	if logOutput != "feature two\nfeature one\n" {
		t.Fatalf("expected deterministic integration order, got %q", logOutput)
	}

	layout, err := git.DiscoverRepositoryLayout(repoRoot)
	if err != nil {
		t.Fatalf("DiscoverRepositoryLayout: %v", err)
	}
	store := state.NewStore(state.DefaultPath(layout.GitDir))
	repoRecord, err := store.GetRepositoryByPath(context.Background(), layout.RepositoryRoot)
	if err != nil {
		t.Fatalf("GetRepositoryByPath: %v", err)
	}
	submissions, err := store.ListIntegrationSubmissions(context.Background(), repoRecord.ID)
	if err != nil {
		t.Fatalf("ListIntegrationSubmissions: %v", err)
	}
	if len(submissions) != 2 {
		t.Fatalf("expected 2 submissions, got %d", len(submissions))
	}
	if submissions[0].Status != "succeeded" || submissions[1].Status != "succeeded" {
		t.Fatalf("expected both submissions succeeded, got %+v", submissions)
	}

	clean, err := git.NewEngine(repoRoot).WorktreeIsClean(repoRoot)
	if err != nil {
		t.Fatalf("WorktreeIsClean: %v", err)
	}
	if !clean {
		t.Fatalf("expected protected branch worktree to remain clean")
	}
}

func TestRunOnceBlocksConflictAndLeavesProtectedBranchUntouched(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	featureOne := filepath.Join(t.TempDir(), "feature-one")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/one", featureOne)
	replaceFileAndCommit(t, featureOne, "README.md", "# alpha\n", "feature one")
	submitBranch(t, featureOne)

	featureTwo := filepath.Join(t.TempDir(), "feature-two")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/two", featureTwo)
	replaceFileAndCommit(t, featureTwo, "README.md", "# beta\n", "feature two")
	submitBranch(t, featureTwo)

	runOnce(t, repoRoot)
	protectedAfterFirst := runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD")

	var runOut bytes.Buffer
	var runErr bytes.Buffer
	if err := runRunOnce([]string{"--repo", repoRoot}, &runOut, &runErr); err != nil {
		t.Fatalf("runRunOnce returned error: %v", err)
	}
	if !strings.Contains(runOut.String(), "Blocked submission") {
		t.Fatalf("expected blocked output, got %q", runOut.String())
	}

	protectedAfterSecond := runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD")
	if protectedAfterFirst != protectedAfterSecond {
		t.Fatalf("expected protected branch to stay unchanged after conflict, got %q then %q", protectedAfterFirst, protectedAfterSecond)
	}

	layout, err := git.DiscoverRepositoryLayout(repoRoot)
	if err != nil {
		t.Fatalf("DiscoverRepositoryLayout: %v", err)
	}
	store := state.NewStore(state.DefaultPath(layout.GitDir))
	repoRecord, err := store.GetRepositoryByPath(context.Background(), layout.RepositoryRoot)
	if err != nil {
		t.Fatalf("GetRepositoryByPath: %v", err)
	}
	submissions, err := store.ListIntegrationSubmissions(context.Background(), repoRecord.ID)
	if err != nil {
		t.Fatalf("ListIntegrationSubmissions: %v", err)
	}
	if submissions[0].Status != "succeeded" {
		t.Fatalf("expected first submission succeeded, got %q", submissions[0].Status)
	}
	if submissions[1].Status != "blocked" {
		t.Fatalf("expected second submission blocked, got %q", submissions[1].Status)
	}

	if status := runTestCommand(t, featureTwo, "git", "status", "--short"); strings.TrimSpace(status) == "" {
		t.Fatalf("expected conflicted source worktree to remain non-clean")
	}
}

func TestRunOnceAutoQueuesPublishRequest(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	cfg, _, err := policy.LoadOrDefault(repoRoot)
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	cfg.Publish.Mode = "auto"
	if err := policy.SaveFile(repoRoot, cfg); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}
	runTestCommand(t, repoRoot, "git", "add", "mainline.toml")
	runTestCommand(t, repoRoot, "git", "commit", "-m", "enable auto publish")

	featureOne := filepath.Join(t.TempDir(), "feature-one")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/one", featureOne)
	writeFileAndCommit(t, featureOne, "one.txt", "feature one\n", "feature one")
	submitBranch(t, featureOne)

	runOnce(t, repoRoot)

	layout, err := git.DiscoverRepositoryLayout(repoRoot)
	if err != nil {
		t.Fatalf("DiscoverRepositoryLayout: %v", err)
	}
	store := state.NewStore(state.DefaultPath(layout.GitDir))
	repoRecord, err := store.GetRepositoryByPath(context.Background(), layout.RepositoryRoot)
	if err != nil {
		t.Fatalf("GetRepositoryByPath: %v", err)
	}
	requests, err := store.ListPublishRequests(context.Background(), repoRecord.ID)
	if err != nil {
		t.Fatalf("ListPublishRequests: %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("expected 1 publish request, got %d", len(requests))
	}
	if requests[0].Status != "queued" {
		t.Fatalf("expected queued publish request, got %q", requests[0].Status)
	}
}

func initRepoForWorker(t *testing.T, repoRoot string) {
	t.Helper()

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	runTestCommand(t, repoRoot, "git", "add", "mainline.toml")
	runTestCommand(t, repoRoot, "git", "commit", "-m", "track mainline config")
}

func submitBranch(t *testing.T, repoPath string) {
	t.Helper()

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	if err := runSubmit([]string{"--repo", repoPath}, &submitOut, &submitErr); err != nil {
		t.Fatalf("runSubmit returned error: %v", err)
	}
}

func runOnce(t *testing.T, repoPath string) {
	t.Helper()

	var runOut bytes.Buffer
	var runErr bytes.Buffer
	if err := runRunOnce([]string{"--repo", repoPath}, &runOut, &runErr); err != nil {
		t.Fatalf("runRunOnce returned error: %v", err)
	}
}

func writeFileAndCommit(t *testing.T, repoPath string, relativePath string, contents string, message string) {
	t.Helper()

	fullPath := filepath.Join(repoPath, relativePath)
	if err := os.WriteFile(fullPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	runTestCommand(t, repoPath, "git", "add", relativePath)
	runTestCommand(t, repoPath, "git", "commit", "-m", message)
}

func replaceFileAndCommit(t *testing.T, repoPath string, relativePath string, contents string, message string) {
	t.Helper()

	fullPath := filepath.Join(repoPath, relativePath)
	if err := os.WriteFile(fullPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	runTestCommand(t, repoPath, "git", "add", relativePath)
	runTestCommand(t, repoPath, "git", "commit", "-m", message)
}
