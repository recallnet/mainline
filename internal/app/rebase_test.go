package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/state"
)

func TestRebaseBranchOntoLocalProtectedMain(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	featurePath := filepath.Join(t.TempDir(), "feature-rebase")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/rebase", featurePath)
	writeFileAndCommit(t, featurePath, "feature.txt", "feature\n", "feature commit")
	writeFileAndCommit(t, repoRoot, "main.txt", "main\n", "main advance")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runRebase([]string{"--repo", featurePath, "--branch", "feature/rebase", "--json"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runRebase returned error: %v", err)
	}

	var result rebaseResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !result.OK || !result.Rebased || result.Status != "rebased" {
		t.Fatalf("expected successful rebase, got %+v", result)
	}

	comparison, err := git.NewEngine(featurePath).CompareBranches("main", "feature/rebase")
	if err != nil {
		t.Fatalf("CompareBranches: %v", err)
	}
	if comparison.BehindCount != 0 {
		t.Fatalf("expected rebased branch to stop being behind main, got %+v", comparison)
	}
}

func TestRebaseSubmissionAbortsInProgressOperationAndReportsConflict(t *testing.T) {
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
	submissions, err := store.ListIntegrationSubmissions(context.Background(), repoRecord.ID)
	if err != nil {
		t.Fatalf("ListIntegrationSubmissions: %v", err)
	}
	blockedID := submissions[1].ID

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = runRebase([]string{"--repo", repoRoot, "--submission", formatInt64(blockedID), "--json"}, newStepPrinter(&stdout), &stderr)
	if err == nil {
		t.Fatalf("expected conflicted rebase to fail")
	}
	if CLIExitCode(err) != 1 {
		t.Fatalf("expected exit code 1, got %v", err)
	}

	var result rebaseResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result.SubmissionID != blockedID || result.Status != "conflict" || len(result.ConflictFiles) == 0 || result.ConflictFiles[0] != "README.md" {
		t.Fatalf("expected conflict result for blocked submission, got %+v", result)
	}
	if result.AbortedOperation != "rebase" {
		t.Fatalf("expected in-progress rebase to be aborted before rerun, got %+v", result)
	}
}

func TestRebaseSubmissionSyncsProtectedMainBeforeRebasing(t *testing.T) {
	repoRoot, remoteDir := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	runTestCommand(t, repoRoot, "git", "push", "origin", "main")

	featurePath := filepath.Join(t.TempDir(), "feature-sync-rebase")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/sync-rebase", featurePath)
	writeFileAndCommit(t, featurePath, "feature.txt", "feature\n", "feature commit")

	upstreamClone := filepath.Join(t.TempDir(), "upstream-clone")
	runTestCommand(t, t.TempDir(), "git", "clone", remoteDir, upstreamClone)
	runTestCommand(t, upstreamClone, "git", "config", "user.name", "Test User")
	runTestCommand(t, upstreamClone, "git", "config", "user.email", "test@example.com")
	writeFileAndCommit(t, upstreamClone, "upstream.txt", "upstream\n", "upstream advance")
	runTestCommand(t, upstreamClone, "git", "push", "origin", "main")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runRebase([]string{"--repo", featurePath, "--branch", "feature/sync-rebase", "--json"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runRebase returned error: %v", err)
	}

	var result rebaseResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !result.SyncedProtected || !result.Rebased {
		t.Fatalf("expected protected branch sync then rebase, got %+v", result)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "upstream.txt")); err != nil {
		t.Fatalf("expected synced protected main to include upstream file, got %v", err)
	}
}

func formatInt64(v int64) string {
	return fmt.Sprintf("%d", v)
}
