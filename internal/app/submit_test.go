package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/state"
)

func TestSubmitQueuesCleanFeatureBranch(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	featurePath := filepath.Join(t.TempDir(), "feature-worktree")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/submit", featurePath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	filePath := filepath.Join(featurePath, "feature.txt")
	if err := os.WriteFile(filePath, []byte("feature\n"), 0o644); err != nil {
		t.Fatalf("write feature file: %v", err)
	}
	runTestCommand(t, featurePath, "git", "add", "feature.txt")
	runTestCommand(t, featurePath, "git", "commit", "-m", "feature commit")

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	if err := runSubmit([]string{"--repo", featurePath}, &submitOut, &submitErr); err != nil {
		t.Fatalf("runSubmit returned error: %v", err)
	}

	layout, err := git.DiscoverRepositoryLayout(featurePath)
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
	if len(submissions) != 1 {
		t.Fatalf("expected 1 submission, got %d", len(submissions))
	}
	if submissions[0].BranchName != "feature/submit" {
		t.Fatalf("expected feature/submit, got %q", submissions[0].BranchName)
	}
	if submissions[0].Status != "queued" {
		t.Fatalf("expected queued status, got %q", submissions[0].Status)
	}
	if !strings.Contains(submitOut.String(), "Queued submission") {
		t.Fatalf("expected queued output, got %q", submitOut.String())
	}
}

func TestSubmitRejectsProtectedBranch(t *testing.T) {
	repoRoot, _ := createTestRepo(t)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	err := runSubmit([]string{"--repo", repoRoot}, &submitOut, &submitErr)
	if err == nil || !strings.Contains(err.Error(), "cannot submit protected branch") {
		t.Fatalf("expected protected branch rejection, got %v", err)
	}
}

func TestSubmitRejectsDirtyWorktree(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	featurePath := filepath.Join(t.TempDir(), "dirty-feature")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/dirty", featurePath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	if err := os.WriteFile(filepath.Join(featurePath, "dirty.txt"), []byte("dirty"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	err := runSubmit([]string{"--repo", featurePath}, &submitOut, &submitErr)
	if err == nil || !strings.Contains(err.Error(), "is dirty") {
		t.Fatalf("expected dirty worktree rejection, got %v", err)
	}
}

func TestSubmitRejectsDetachedHeadWithoutBranch(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	detachedPath := filepath.Join(t.TempDir(), "detached")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "--detach", detachedPath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	err := runSubmit([]string{"--repo", detachedPath}, &submitOut, &submitErr)
	if err == nil || !strings.Contains(err.Error(), "detached HEAD") {
		t.Fatalf("expected detached HEAD rejection, got %v", err)
	}
}

func TestSubmitRejectsWorktreeFromDifferentRepository(t *testing.T) {
	repoRoot, _ := createTestRepo(t)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	otherRepoRoot, _ := createTestRepo(t)
	foreignWorktree := filepath.Join(t.TempDir(), "foreign-feature")
	runTestCommand(t, otherRepoRoot, "git", "worktree", "add", "-b", "feature/foreign", foreignWorktree)

	filePath := filepath.Join(foreignWorktree, "feature.txt")
	if err := os.WriteFile(filePath, []byte("foreign feature\n"), 0o644); err != nil {
		t.Fatalf("write foreign feature file: %v", err)
	}
	runTestCommand(t, foreignWorktree, "git", "add", "feature.txt")
	runTestCommand(t, foreignWorktree, "git", "commit", "-m", "foreign feature commit")

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	err := runSubmit([]string{"--repo", repoRoot, "--worktree", foreignWorktree, "--branch", "feature/foreign"}, &submitOut, &submitErr)
	if err == nil || !strings.Contains(err.Error(), "does not belong to repository") {
		t.Fatalf("expected cross-repo worktree rejection, got %v", err)
	}
}

func TestSubmitAcceptsSymlinkedWorktreePath(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	featurePath := filepath.Join(t.TempDir(), "feature-worktree")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/symlink", featurePath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	filePath := filepath.Join(featurePath, "feature.txt")
	if err := os.WriteFile(filePath, []byte("feature\n"), 0o644); err != nil {
		t.Fatalf("write feature file: %v", err)
	}
	runTestCommand(t, featurePath, "git", "add", "feature.txt")
	runTestCommand(t, featurePath, "git", "commit", "-m", "feature commit")

	aliasDir := filepath.Join(t.TempDir(), "aliases")
	if err := os.MkdirAll(aliasDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	aliasPath := filepath.Join(aliasDir, "feature-symlink")
	if err := os.Symlink(featurePath, aliasPath); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	if err := runSubmit([]string{"--repo", repoRoot, "--branch", "feature/symlink", "--worktree", aliasPath}, &submitOut, &submitErr); err != nil {
		t.Fatalf("runSubmit returned error: %v", err)
	}

	if !strings.Contains(submitOut.String(), "Queued submission") {
		t.Fatalf("expected queued output, got %q", submitOut.String())
	}
}

func TestSubmitJSONReturnsSubmissionMetadata(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	featurePath := filepath.Join(t.TempDir(), "feature-json")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/json", featurePath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	writeFileAndCommit(t, featurePath, "feature.txt", "feature\n", "feature commit")

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	if err := runSubmit([]string{"--repo", featurePath, "--json", "--requested-by", "factory"}, &submitOut, &submitErr); err != nil {
		t.Fatalf("runSubmit returned error: %v", err)
	}

	var result submitResult
	if err := json.Unmarshal(submitOut.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !result.OK || !result.Queued {
		t.Fatalf("expected queued json result, got %+v", result)
	}
	if result.SubmissionID == 0 {
		t.Fatalf("expected submission id, got %+v", result)
	}
	if result.Branch != "feature/json" || result.RequestedBy != "factory" {
		t.Fatalf("expected branch/requested_by metadata, got %+v", result)
	}
}

func TestSubmitCheckValidatesWithoutQueueing(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	featurePath := filepath.Join(t.TempDir(), "feature-check")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/check", featurePath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	writeFileAndCommit(t, featurePath, "feature.txt", "feature\n", "feature commit")

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	if err := runSubmit([]string{"--repo", featurePath, "--check", "--json"}, &submitOut, &submitErr); err != nil {
		t.Fatalf("runSubmit returned error: %v", err)
	}

	var result submitResult
	if err := json.Unmarshal(submitOut.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !result.OK || !result.Checked || result.Queued {
		t.Fatalf("expected check-only success without queueing, got %+v", result)
	}

	layout, err := git.DiscoverRepositoryLayout(featurePath)
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
	if len(submissions) != 0 {
		t.Fatalf("expected no queued submissions after --check, got %+v", submissions)
	}
}

func TestSubmitJSONFailureIncludesStableErrorCode(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	featurePath := filepath.Join(t.TempDir(), "dirty-json")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/dirty-json", featurePath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	if err := os.WriteFile(filepath.Join(featurePath, "dirty.txt"), []byte("dirty"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	err := runSubmit([]string{"--repo", featurePath, "--json"}, &submitOut, &submitErr)
	if err == nil {
		t.Fatalf("expected submit failure")
	}

	var result submitResult
	if err := json.Unmarshal(submitOut.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result.OK || result.ErrorCode != "dirty_worktree" {
		t.Fatalf("expected dirty_worktree error code, got %+v", result)
	}
}
