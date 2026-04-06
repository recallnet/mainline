package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/policy"
	"github.com/recallnet/mainline/internal/state"
)

func TestSubmitQueuesCleanFeatureBranch(t *testing.T) {
	t.Setenv("MAINLINE_DISABLE_SUBMIT_DRAIN", "1")

	repoRoot, _ := createTestRepo(t)
	featurePath := filepath.Join(t.TempDir(), "feature-worktree")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/submit", featurePath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, newStepPrinter(&initOut), &initErr); err != nil {
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
	if err := runSubmit([]string{"--repo", featurePath}, newStepPrinter(&submitOut), &submitErr); err != nil {
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

func TestSubmitAutoDetectsRepoFromCurrentWorktree(t *testing.T) {
	t.Setenv("MAINLINE_DISABLE_SUBMIT_DRAIN", "1")

	repoRoot, _ := createTestRepo(t)
	featurePath := filepath.Join(t.TempDir(), "feature-autodetect")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/autodetect", featurePath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, newStepPrinter(&initOut), &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	writeFileAndCommit(t, featurePath, "feature.txt", "feature\n", "feature commit")

	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer func() {
		if chdirErr := os.Chdir(originalWD); chdirErr != nil {
			t.Fatalf("restore cwd: %v", chdirErr)
		}
	}()
	if err := os.Chdir(featurePath); err != nil {
		t.Fatalf("Chdir(featurePath): %v", err)
	}

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	if err := runSubmit(nil, newStepPrinter(&submitOut), &submitErr); err != nil {
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
	if len(submissions) != 1 || submissions[0].BranchName != "feature/autodetect" {
		t.Fatalf("expected autodetected submission for feature/autodetect, got %+v", submissions)
	}
}

func TestSubmitOpportunisticallyDrainsQueuedWork(t *testing.T) {
	repoRoot, remoteDir := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	updatePublishMode(t, repoRoot, "auto")

	featurePath := filepath.Join(t.TempDir(), "feature-drain")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/drain", featurePath)
	writeFileAndCommit(t, featurePath, "drain.txt", "drain\n", "feature drain")

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	if err := runSubmit([]string{"--repo", featurePath, "--json"}, newStepPrinter(&submitOut), &submitErr); err != nil {
		t.Fatalf("runSubmit returned error: %v", err)
	}

	var result submitResult
	if err := json.Unmarshal(submitOut.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !result.DrainAttempted {
		t.Fatalf("expected drain attempt, got %+v", result)
	}
	if result.SubmissionStatus != "succeeded" || result.Outcome != submissionOutcomeLanded {
		t.Fatalf("expected submit json to reflect landed outcome after drain, got %+v", result)
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
	submission, err := store.GetIntegrationSubmission(context.Background(), result.SubmissionID)
	if err != nil {
		t.Fatalf("GetIntegrationSubmission: %v", err)
	}
	if submission.Status != "succeeded" {
		t.Fatalf("expected opportunistic drain to succeed submission, got %+v", submission)
	}
	requests, err := store.ListPublishRequests(context.Background(), repoRecord.ID)
	if err != nil {
		t.Fatalf("ListPublishRequests: %v", err)
	}
	if len(requests) != 1 || requests[0].Status != "succeeded" {
		t.Fatalf("expected succeeded publish request, got %+v", requests)
	}
	localHead := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	remoteHead := trimNewline(runTestCommand(t, remoteDir, "git", "rev-parse", "refs/heads/main"))
	if remoteHead != localHead {
		t.Fatalf("expected remote head %q, got %q", localHead, remoteHead)
	}
}

func TestSubmitQueuesAndExitsWhenAnotherWorkerHoldsLock(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	featurePath := filepath.Join(t.TempDir(), "feature-busy")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/busy", featurePath)
	writeFileAndCommit(t, featurePath, "busy.txt", "busy\n", "feature busy")

	layout, err := git.DiscoverRepositoryLayout(repoRoot)
	if err != nil {
		t.Fatalf("DiscoverRepositoryLayout: %v", err)
	}
	lockManager := state.NewLockManager(layout.RepositoryRoot, layout.GitDir)
	lease, err := lockManager.Acquire(state.IntegrationLock, "test")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lease.Release()

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	if err := runSubmit([]string{"--repo", featurePath, "--json"}, newStepPrinter(&submitOut), &submitErr); err != nil {
		t.Fatalf("runSubmit returned error: %v", err)
	}

	var result submitResult
	if err := json.Unmarshal(submitOut.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result.DrainResult != "Integration worker busy." {
		t.Fatalf("expected busy drain result, got %+v", result)
	}

	store := state.NewStore(state.DefaultPath(layout.GitDir))
	submission, err := store.GetIntegrationSubmission(context.Background(), result.SubmissionID)
	if err != nil {
		t.Fatalf("GetIntegrationSubmission: %v", err)
	}
	if submission.Status != "queued" {
		t.Fatalf("expected queued submission under held lock, got %+v", submission)
	}
}

func TestSubmitQueueOnlySkipsOpportunisticDrain(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	featurePath := filepath.Join(t.TempDir(), "feature-queue-only")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/queue-only", featurePath)
	writeFileAndCommit(t, featurePath, "queue-only.txt", "queue only\n", "feature queue only")

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	if err := runSubmit([]string{"--repo", featurePath, "--queue-only", "--json"}, newStepPrinter(&submitOut), &submitErr); err != nil {
		t.Fatalf("runSubmit returned error: %v", err)
	}

	var result submitResult
	if err := json.Unmarshal(submitOut.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result.DrainAttempted {
		t.Fatalf("expected queue-only submit to skip drain, got %+v", result)
	}
	if result.SubmissionStatus != "queued" {
		t.Fatalf("expected queued submission, got %+v", result)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "queue-only.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected queue-only branch not to land yet, got err=%v", err)
	}
}

func TestSubmitRejectsProtectedBranch(t *testing.T) {
	t.Setenv("MAINLINE_DISABLE_SUBMIT_DRAIN", "1")

	repoRoot, _ := createTestRepo(t)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, newStepPrinter(&initOut), &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	err := runSubmit([]string{"--repo", repoRoot}, newStepPrinter(&submitOut), &submitErr)
	if err == nil || !strings.Contains(err.Error(), "cannot submit protected branch") {
		t.Fatalf("expected protected branch rejection, got %v", err)
	}
}

func TestSubmitRejectsDirtyWorktree(t *testing.T) {
	t.Setenv("MAINLINE_DISABLE_SUBMIT_DRAIN", "1")

	repoRoot, _ := createTestRepo(t)
	featurePath := filepath.Join(t.TempDir(), "dirty-feature")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/dirty", featurePath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, newStepPrinter(&initOut), &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	if err := os.WriteFile(filepath.Join(featurePath, "dirty.txt"), []byte("dirty"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	err := runSubmit([]string{"--repo", featurePath}, newStepPrinter(&submitOut), &submitErr)
	if err == nil || !strings.Contains(err.Error(), "is dirty") {
		t.Fatalf("expected dirty worktree rejection, got %v", err)
	}
}

func TestSubmitRejectsDetachedHeadWithoutBranch(t *testing.T) {
	t.Setenv("MAINLINE_DISABLE_SUBMIT_DRAIN", "1")

	repoRoot, _ := createTestRepo(t)
	detachedPath := filepath.Join(t.TempDir(), "detached")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "--detach", detachedPath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, newStepPrinter(&initOut), &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	if err := os.WriteFile(filepath.Join(detachedPath, "detached.txt"), []byte("detached\n"), 0o644); err != nil {
		t.Fatalf("write detached file: %v", err)
	}
	runTestCommand(t, detachedPath, "git", "add", "detached.txt")
	runTestCommand(t, detachedPath, "git", "commit", "-m", "detached commit")
	runTestCommand(t, detachedPath, "git", "checkout", "--detach", "HEAD")

	if err := runSubmit([]string{"--repo", detachedPath, "--json"}, newStepPrinter(&submitOut), &submitErr); err != nil {
		t.Fatalf("expected detached HEAD submit to succeed, got %v", err)
	}

	var result submitResult
	if err := json.Unmarshal(submitOut.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !result.OK || !result.Queued || result.RefKind != submissionRefKindSHA {
		t.Fatalf("expected detached HEAD queued as sha submission, got %+v", result)
	}
	if result.SourceRef == "" || result.SourceRef != result.SourceSHA {
		t.Fatalf("expected detached submit to use head sha as source ref, got %+v", result)
	}
}

func TestSubmitAcceptsExplicitSHAWhenCheckedOut(t *testing.T) {
	t.Setenv("MAINLINE_DISABLE_SUBMIT_DRAIN", "1")

	repoRoot, _ := createTestRepo(t)
	featurePath := filepath.Join(t.TempDir(), "feature-sha")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/sha", featurePath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, newStepPrinter(&initOut), &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	writeFileAndCommit(t, featurePath, "feature.txt", "feature\n", "feature commit")
	headSHA := strings.TrimSpace(runTestCommand(t, featurePath, "git", "rev-parse", "HEAD"))

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	if err := runSubmit([]string{"--repo", featurePath, "--json", "--sha", headSHA}, newStepPrinter(&submitOut), &submitErr); err != nil {
		t.Fatalf("runSubmit returned error: %v", err)
	}

	var result submitResult
	if err := json.Unmarshal(submitOut.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result.RefKind != submissionRefKindSHA || result.SourceRef != headSHA {
		t.Fatalf("expected explicit sha submission, got %+v", result)
	}
}

func TestSubmitRejectsSHAWhenNotCheckedOut(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	featurePath := filepath.Join(t.TempDir(), "feature-sha-mismatch")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/sha-mismatch", featurePath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, newStepPrinter(&initOut), &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	writeFileAndCommit(t, featurePath, "feature.txt", "feature\n", "feature commit")
	parentSHA := strings.TrimSpace(runTestCommand(t, featurePath, "git", "rev-parse", "HEAD^"))

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	err := runSubmit([]string{"--repo", featurePath, "--sha", parentSHA}, newStepPrinter(&submitOut), &submitErr)
	if err == nil || !strings.Contains(err.Error(), "expected "+parentSHA+" to be checked out") {
		t.Fatalf("expected sha checkout mismatch rejection, got %v", err)
	}
}

func TestSubmitRejectsWorktreeFromDifferentRepository(t *testing.T) {
	repoRoot, _ := createTestRepo(t)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, newStepPrinter(&initOut), &initErr); err != nil {
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
	err := runSubmit([]string{"--repo", repoRoot, "--worktree", foreignWorktree, "--branch", "feature/foreign"}, newStepPrinter(&submitOut), &submitErr)
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
	if err := runRepoInit([]string{"--repo", repoRoot}, newStepPrinter(&initOut), &initErr); err != nil {
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
	if err := runSubmit([]string{"--repo", repoRoot, "--branch", "feature/symlink", "--worktree", aliasPath}, newStepPrinter(&submitOut), &submitErr); err != nil {
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
	if err := runRepoInit([]string{"--repo", repoRoot}, newStepPrinter(&initOut), &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	writeFileAndCommit(t, featurePath, "feature.txt", "feature\n", "feature commit")

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	if err := runSubmit([]string{"--repo", featurePath, "--json", "--requested-by", "factory"}, newStepPrinter(&submitOut), &submitErr); err != nil {
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
	if result.Priority != submissionPriorityNormal {
		t.Fatalf("expected default normal priority, got %+v", result)
	}
}

func TestSubmitStoresRequestedPriority(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	featurePath := filepath.Join(t.TempDir(), "feature-priority")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/priority", featurePath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, newStepPrinter(&initOut), &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	writeFileAndCommit(t, featurePath, "feature.txt", "feature\n", "feature commit")

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	if err := runSubmit([]string{"--repo", featurePath, "--json", "--priority", submissionPriorityHigh}, newStepPrinter(&submitOut), &submitErr); err != nil {
		t.Fatalf("runSubmit returned error: %v", err)
	}

	var result submitResult
	if err := json.Unmarshal(submitOut.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result.Priority != submissionPriorityHigh {
		t.Fatalf("expected high priority in json output, got %+v", result)
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
	if len(submissions) != 1 || submissions[0].Priority != submissionPriorityHigh {
		t.Fatalf("expected persisted high priority, got %+v", submissions)
	}
}

func TestSubmitStoresAllowNewerHead(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	featurePath := filepath.Join(t.TempDir(), "feature-allow-newer")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/allow-newer", featurePath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, newStepPrinter(&initOut), &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	writeFileAndCommit(t, featurePath, "feature.txt", "feature\n", "feature commit")

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	if err := runSubmit([]string{"--repo", featurePath, "--json", "--allow-newer-head"}, newStepPrinter(&submitOut), &submitErr); err != nil {
		t.Fatalf("runSubmit returned error: %v", err)
	}

	var result submitResult
	if err := json.Unmarshal(submitOut.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !result.AllowNewerHead {
		t.Fatalf("expected allow_newer_head in json output, got %+v", result)
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
	if len(submissions) != 1 || !submissions[0].AllowNewerHead {
		t.Fatalf("expected persisted allow_newer_head, got %+v", submissions)
	}
}

func TestSubmitRejectsUnknownPriority(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	featurePath := filepath.Join(t.TempDir(), "feature-bad-priority")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/bad-priority", featurePath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, newStepPrinter(&initOut), &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	writeFileAndCommit(t, featurePath, "feature.txt", "feature\n", "feature commit")

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	err := runSubmit([]string{"--repo", featurePath, "--priority", "urgent"}, newStepPrinter(&submitOut), &submitErr)
	if err == nil || !strings.Contains(err.Error(), "priority must be one of") {
		t.Fatalf("expected invalid priority rejection, got %v", err)
	}
}

func TestSubmitReprioritizesAlreadyQueuedSubmission(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	featurePath := filepath.Join(t.TempDir(), "feature-reprioritize")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/reprioritize", featurePath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, newStepPrinter(&initOut), &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	writeFileAndCommit(t, featurePath, "feature.txt", "feature\n", "feature commit")

	var firstOut bytes.Buffer
	var firstErr bytes.Buffer
	if err := runSubmit([]string{"--repo", featurePath, "--json", "--priority", submissionPriorityLow}, newStepPrinter(&firstOut), &firstErr); err != nil {
		t.Fatalf("first runSubmit returned error: %v", err)
	}

	var first submitResult
	if err := json.Unmarshal(firstOut.Bytes(), &first); err != nil {
		t.Fatalf("Unmarshal first: %v", err)
	}

	var secondOut bytes.Buffer
	var secondErr bytes.Buffer
	if err := runSubmit([]string{"--repo", featurePath, "--json", "--priority", submissionPriorityHigh}, newStepPrinter(&secondOut), &secondErr); err != nil {
		t.Fatalf("second runSubmit returned error: %v", err)
	}

	var second submitResult
	if err := json.Unmarshal(secondOut.Bytes(), &second); err != nil {
		t.Fatalf("Unmarshal second: %v", err)
	}
	if first.SubmissionID != second.SubmissionID {
		t.Fatalf("expected reprioritization to reuse submission id, got first=%d second=%d", first.SubmissionID, second.SubmissionID)
	}
	if second.Priority != submissionPriorityHigh {
		t.Fatalf("expected reprioritized high priority, got %+v", second)
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
	if len(submissions) != 1 || submissions[0].Priority != submissionPriorityHigh {
		t.Fatalf("expected one reprioritized queued submission, got %+v", submissions)
	}
}

func TestSubmitUpgradesExistingLegacyStateSchema(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	featurePath := filepath.Join(t.TempDir(), "feature-legacy-submit")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/legacy-submit", featurePath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, newStepPrinter(&initOut), &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	writeFileAndCommit(t, featurePath, "feature.txt", "feature\n", "feature commit")

	layout, err := git.DiscoverRepositoryLayout(featurePath)
	if err != nil {
		t.Fatalf("DiscoverRepositoryLayout: %v", err)
	}
	statePath := state.DefaultPath(layout.GitDir)
	if err := os.Remove(statePath); err != nil {
		t.Fatalf("Remove(state db): %v", err)
	}

	db, err := sql.Open("sqlite", statePath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
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
		INSERT INTO repositories (canonical_path, protected_branch, remote_name, main_worktree_path, policy_version)
		VALUES (?, 'main', 'origin', ?, 'v1');
		PRAGMA user_version = 1;
	`, layout.RepositoryRoot, repoRoot); err != nil {
		t.Fatalf("create legacy state schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	if err := runSubmit([]string{"--repo", featurePath, "--json", "--priority", submissionPriorityHigh}, newStepPrinter(&submitOut), &submitErr); err != nil {
		t.Fatalf("runSubmit returned error: %v", err)
	}

	var result submitResult
	if err := json.Unmarshal(submitOut.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !result.OK || !result.Queued || result.Priority != submissionPriorityHigh {
		t.Fatalf("expected successful queued result after legacy upgrade, got %+v", result)
	}
}

func TestSubmitCheckValidatesWithoutQueueing(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	featurePath := filepath.Join(t.TempDir(), "feature-check")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/check", featurePath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, newStepPrinter(&initOut), &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	writeFileAndCommit(t, featurePath, "feature.txt", "feature\n", "feature commit")

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	if err := runSubmit([]string{"--repo", featurePath, "--check", "--json"}, newStepPrinter(&submitOut), &submitErr); err != nil {
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

func TestSubmitCheckOnlyAliasValidatesWithoutQueueing(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	featurePath := filepath.Join(t.TempDir(), "feature-check-only")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/check-only", featurePath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, newStepPrinter(&initOut), &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	writeFileAndCommit(t, featurePath, "feature.txt", "feature\n", "feature commit")

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	if err := runSubmit([]string{"--repo", featurePath, "--check-only", "--json"}, newStepPrinter(&submitOut), &submitErr); err != nil {
		t.Fatalf("runSubmit returned error: %v", err)
	}

	var result submitResult
	if err := json.Unmarshal(submitOut.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !result.OK || !result.Checked || result.Queued {
		t.Fatalf("expected check-only success without queueing, got %+v", result)
	}
}

func TestSubmitCheckOnlyRejectsBranchBehindProtected(t *testing.T) {
	repoRoot, _ := createTestRepo(t)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, newStepPrinter(&initOut), &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	featurePath := filepath.Join(t.TempDir(), "feature-behind")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/behind", featurePath)
	writeFileAndCommit(t, featurePath, "feature.txt", "feature\n", "feature commit")
	writeFileAndCommit(t, repoRoot, "main.txt", "main\n", "main advance")

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	err := runSubmit([]string{"--repo", featurePath, "--check-only", "--json"}, newStepPrinter(&submitOut), &submitErr)
	if err == nil {
		t.Fatalf("expected behind-protected check-only failure")
	}

	var result submitResult
	if err := json.Unmarshal(submitOut.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result.ErrorCode != "branch_needs_rebase" {
		t.Fatalf("expected branch_needs_rebase, got %+v", result)
	}
}

func TestSubmitCheckOnlyRejectsAlreadyQueuedSubmission(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	featurePath := filepath.Join(t.TempDir(), "feature-queued")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/queued", featurePath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, newStepPrinter(&initOut), &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	writeFileAndCommit(t, featurePath, "feature.txt", "feature\n", "feature commit")
	if err := runSubmit([]string{"--repo", featurePath}, newStepPrinter(&bytes.Buffer{}), &bytes.Buffer{}); err != nil {
		t.Fatalf("first runSubmit returned error: %v", err)
	}

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	err := runSubmit([]string{"--repo", featurePath, "--check-only", "--json"}, newStepPrinter(&submitOut), &submitErr)
	if err == nil {
		t.Fatalf("expected already-queued check-only failure")
	}

	var result submitResult
	if err := json.Unmarshal(submitOut.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result.ErrorCode != "already_queued" {
		t.Fatalf("expected already_queued, got %+v", result)
	}
}

func TestSubmitRejectsExactDuplicateActiveSubmission(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	featurePath := filepath.Join(t.TempDir(), "feature-duplicate")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/duplicate", featurePath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, newStepPrinter(&initOut), &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	writeFileAndCommit(t, featurePath, "feature.txt", "feature\n", "feature commit")
	if err := runSubmit([]string{"--repo", featurePath}, newStepPrinter(&bytes.Buffer{}), &bytes.Buffer{}); err != nil {
		t.Fatalf("first runSubmit returned error: %v", err)
	}

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	err := runSubmit([]string{"--repo", featurePath, "--json"}, newStepPrinter(&submitOut), &submitErr)
	if err == nil {
		t.Fatalf("expected duplicate submit failure")
	}

	var result submitResult
	if err := json.Unmarshal(submitOut.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result.ErrorCode != "already_queued" {
		t.Fatalf("expected already_queued duplicate error, got %+v", result)
	}
}

func TestSubmitRejectsCheckOnlyWaitCombination(t *testing.T) {
	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	err := runSubmit([]string{"--repo", ".", "--check-only", "--wait"}, newStepPrinter(&submitOut), &submitErr)
	if err == nil {
		t.Fatalf("expected flag conflict failure")
	}
	if !strings.Contains(err.Error(), "--check/--check-only and --wait cannot be used together") {
		t.Fatalf("expected combined check/check-only conflict error, got %v", err)
	}
}

func TestSubmitRejectsWhenIntegrationQueueDepthReached(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	featureOne := filepath.Join(t.TempDir(), "feature-one")
	featureTwo := filepath.Join(t.TempDir(), "feature-two")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/one", featureOne)
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/two", featureTwo)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, newStepPrinter(&initOut), &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	cfg, _, err := policy.LoadOrDefault(repoRoot)
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	cfg.Integration.MaxQueueDepth = 1
	if err := policy.SaveFile(repoRoot, cfg); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}

	writeFileAndCommit(t, featureOne, "one.txt", "one\n", "feature one")
	writeFileAndCommit(t, featureTwo, "two.txt", "two\n", "feature two")

	if err := runSubmit([]string{"--repo", featureOne}, newStepPrinter(&bytes.Buffer{}), &bytes.Buffer{}); err != nil {
		t.Fatalf("first runSubmit returned error: %v", err)
	}

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	err = runSubmit([]string{"--repo", featureTwo, "--json"}, newStepPrinter(&submitOut), &submitErr)
	if err == nil {
		t.Fatalf("expected queue depth rejection")
	}

	var result submitResult
	if unmarshalErr := json.Unmarshal(submitOut.Bytes(), &result); unmarshalErr != nil {
		t.Fatalf("Unmarshal: %v", unmarshalErr)
	}
	if result.ErrorCode != "integration_queue_full" {
		t.Fatalf("expected integration_queue_full, got %+v", result)
	}
	if !strings.Contains(result.Error, "MaxQueueDepth=1") {
		t.Fatalf("expected MaxQueueDepth message, got %+v", result)
	}
}

func TestSubmitCheckOnlyIgnoresQueueDepthLimit(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	featureOne := filepath.Join(t.TempDir(), "feature-one")
	featureTwo := filepath.Join(t.TempDir(), "feature-two")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/one", featureOne)
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/two", featureTwo)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, newStepPrinter(&initOut), &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	cfg, _, err := policy.LoadOrDefault(repoRoot)
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	cfg.Integration.MaxQueueDepth = 1
	if err := policy.SaveFile(repoRoot, cfg); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}

	writeFileAndCommit(t, featureOne, "one.txt", "one\n", "feature one")
	writeFileAndCommit(t, featureTwo, "two.txt", "two\n", "feature two")

	if err := runSubmit([]string{"--repo", featureOne}, newStepPrinter(&bytes.Buffer{}), &bytes.Buffer{}); err != nil {
		t.Fatalf("first runSubmit returned error: %v", err)
	}

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	if err := runSubmit([]string{"--repo", featureTwo, "--check-only", "--json"}, newStepPrinter(&submitOut), &submitErr); err != nil {
		t.Fatalf("expected check-only to pass, got %v", err)
	}

	var result submitResult
	if unmarshalErr := json.Unmarshal(submitOut.Bytes(), &result); unmarshalErr != nil {
		t.Fatalf("Unmarshal: %v", unmarshalErr)
	}
	if !result.OK || result.Queued {
		t.Fatalf("expected successful dry-run result, got %+v", result)
	}
}

func TestSubmitCanReprioritizeQueuedDuplicateWhenQueueDepthReached(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	featurePath := filepath.Join(t.TempDir(), "feature-priority")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/priority", featurePath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, newStepPrinter(&initOut), &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	cfg, _, err := policy.LoadOrDefault(repoRoot)
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	cfg.Integration.MaxQueueDepth = 1
	if err := policy.SaveFile(repoRoot, cfg); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}

	writeFileAndCommit(t, featurePath, "feature.txt", "feature\n", "feature commit")

	if err := runSubmit([]string{"--repo", featurePath, "--json", "--priority", submissionPriorityLow}, newStepPrinter(&bytes.Buffer{}), &bytes.Buffer{}); err != nil {
		t.Fatalf("first runSubmit returned error: %v", err)
	}

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	if err := runSubmit([]string{"--repo", featurePath, "--json", "--priority", submissionPriorityHigh}, newStepPrinter(&submitOut), &submitErr); err != nil {
		t.Fatalf("reprioritize runSubmit returned error: %v", err)
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
	if len(submissions) != 1 || submissions[0].Priority != submissionPriorityHigh {
		t.Fatalf("expected queued duplicate reprioritized despite full queue, got %+v", submissions)
	}
}

func TestSubmitJSONFailureIncludesStableErrorCode(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	featurePath := filepath.Join(t.TempDir(), "dirty-json")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/dirty-json", featurePath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, newStepPrinter(&initOut), &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	if err := os.WriteFile(filepath.Join(featurePath, "dirty.txt"), []byte("dirty"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	err := runSubmit([]string{"--repo", featurePath, "--json"}, newStepPrinter(&submitOut), &submitErr)
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

func TestSubmitJSONDetachedFailureUsesStableErrorCode(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	detachedPath := filepath.Join(t.TempDir(), "detached-json")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "--detach", detachedPath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, newStepPrinter(&initOut), &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	err := runSubmit([]string{"--repo", detachedPath, "--branch", "main", "--json"}, newStepPrinter(&submitOut), &submitErr)
	if err == nil {
		t.Fatalf("expected detached submit failure")
	}

	var result submitResult
	if err := json.Unmarshal(submitOut.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result.OK || result.ErrorCode != "detached_head" {
		t.Fatalf("expected detached_head error code, got %+v", result)
	}
}

func TestSubmitWaitIntegratesBranchAndReturnsIntegratedOutcome(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	featurePath := filepath.Join(t.TempDir(), "feature-wait")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/wait", featurePath)
	writeFileAndCommit(t, featurePath, "wait.txt", "wait\n", "feature wait")

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	if err := runSubmit([]string{"--repo", featurePath, "--wait", "--json", "--timeout", "30s", "--poll-interval", "10ms"}, newStepPrinter(&submitOut), &submitErr); err != nil {
		t.Fatalf("runSubmit returned error: %v", err)
	}

	var result submitResult
	if err := json.Unmarshal(submitOut.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !result.OK || !result.Waited || result.SubmissionStatus != "succeeded" || result.Outcome != submissionOutcomeIntegrated {
		t.Fatalf("expected waited integrated result, got %+v", result)
	}
	if !strings.Contains(result.LastWorkerResult, "Integrated submission") {
		t.Fatalf("expected worker result, got %+v", result)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "wait.txt")); err != nil {
		t.Fatalf("expected integrated file, got %v", err)
	}
}

func TestSubmitWaitForLandedBlocksThroughAutoPublish(t *testing.T) {
	repoRoot, remoteDir := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	updatePublishMode(t, repoRoot, "auto")

	featurePath := filepath.Join(t.TempDir(), "feature-wait-landed")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/wait-landed", featurePath)
	writeFileAndCommit(t, featurePath, "wait-landed.txt", "wait landed\n", "feature wait landed")

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	if err := runSubmit([]string{"--repo", featurePath, "--wait", "--for", "landed", "--json", "--timeout", "30s", "--poll-interval", "10ms"}, newStepPrinter(&submitOut), &submitErr); err != nil {
		t.Fatalf("runSubmit returned error: %v", err)
	}

	var result submitResult
	if err := json.Unmarshal(submitOut.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !result.OK || !result.Waited || result.SubmissionStatus != "succeeded" || result.Outcome != submissionOutcomeLanded {
		t.Fatalf("expected waited landed result, got %+v", result)
	}
	if result.PublishRequestID == 0 || result.PublishStatus != "succeeded" {
		t.Fatalf("expected publish correlation in landed result, got %+v", result)
	}
	localHead := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	remoteHead := trimNewline(runTestCommand(t, remoteDir, "git", "rev-parse", "refs/heads/main"))
	if remoteHead != localHead {
		t.Fatalf("expected remote head %q, got %q", localHead, remoteHead)
	}
}

func TestSubmitWaitSucceedsAfterQueuedRebaseRewritesBranchSHA(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	featureOne := filepath.Join(t.TempDir(), "feature-serial-one")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/serial-one", featureOne)
	writeFileAndCommit(t, featureOne, "one.txt", "one\n", "feature one")

	featureTwo := filepath.Join(t.TempDir(), "feature-serial-two")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/serial-two", featureTwo)
	writeFileAndCommit(t, featureTwo, "two.txt", "two\n", "feature two")
	originalSHA := trimNewline(runTestCommand(t, featureTwo, "git", "rev-parse", "HEAD"))

	if err := runSubmit([]string{"--repo", featureOne}, newStepPrinter(&bytes.Buffer{}), &bytes.Buffer{}); err != nil {
		t.Fatalf("first runSubmit returned error: %v", err)
	}

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	if err := runSubmit([]string{"--repo", featureTwo, "--wait", "--json", "--timeout", "30s", "--poll-interval", "10ms"}, newStepPrinter(&submitOut), &submitErr); err != nil {
		t.Fatalf("second runSubmit returned error: %v", err)
	}

	var result submitResult
	if err := json.Unmarshal(submitOut.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !result.OK || result.Outcome != submissionOutcomeIntegrated || result.SubmissionStatus != "succeeded" {
		t.Fatalf("expected integrated wait result, got %+v", result)
	}

	finalProtectedSHA := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "main"))
	if finalProtectedSHA == originalSHA {
		t.Fatalf("expected queued rebase to rewrite final protected sha away from original %s", originalSHA)
	}
	if !strings.Contains(result.LastWorkerResult, "Integrated submission") {
		t.Fatalf("expected worker result, got %+v", result)
	}
}

func TestSubmitWaitTextWarnsWhenPublishModeIsManual(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	featurePath := filepath.Join(t.TempDir(), "feature-wait-text")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/wait-text", featurePath)
	writeFileAndCommit(t, featurePath, "wait.txt", "wait\n", "feature wait text")

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	if err := runSubmit([]string{"--repo", featurePath, "--wait", "--timeout", "30s", "--poll-interval", "10ms"}, newStepPrinter(&submitOut), &submitErr); err != nil {
		t.Fatalf("runSubmit returned error: %v", err)
	}

	text := submitOut.String()
	if !strings.Contains(text, "Outcome: integrated") {
		t.Fatalf("expected integrated outcome in text output, got %q", text)
	}
	if !strings.Contains(text, "Publish mode is manual") {
		t.Fatalf("expected manual publish warning in text output, got %q", text)
	}
}

func TestSubmitWaitForLandedFailsFastOnManualPublishRepo(t *testing.T) {
	repoRoot, remoteDir := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	featurePath := filepath.Join(t.TempDir(), "feature-wait-landed-manual")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/wait-landed-manual", featurePath)
	writeFileAndCommit(t, featurePath, "wait-landed.txt", "wait landed\n", "feature wait landed manual")
	featureSHA := trimNewline(runTestCommand(t, featurePath, "git", "rev-parse", "HEAD"))

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	err := runSubmit([]string{"--repo", featurePath, "--wait", "--for", "landed", "--json", "--timeout", "30s", "--poll-interval", "10ms"}, newStepPrinter(&submitOut), &submitErr)
	if err == nil {
		t.Fatalf("expected manual publish landed wait to fail fast")
	}

	var result submitResult
	if decodeErr := json.Unmarshal(submitOut.Bytes(), &result); decodeErr != nil {
		t.Fatalf("Unmarshal: %v", decodeErr)
	}
	if result.SubmissionStatus != "succeeded" || result.Outcome != submissionOutcomeIntegrated {
		t.Fatalf("expected integrated submission result, got %+v", result)
	}
	if result.PublishRequestID != 0 {
		t.Fatalf("expected no publish request on manual repo, got %+v", result)
	}
	if !strings.Contains(result.Error, "publish mode is manual") {
		t.Fatalf("expected manual publish error, got %+v", result)
	}

	localHead := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	remoteHead := trimNewline(runTestCommand(t, remoteDir, "git", "rev-parse", "refs/heads/main"))
	if localHead != featureSHA {
		t.Fatalf("expected local protected head %q, got %q", featureSHA, localHead)
	}
	if remoteHead == featureSHA {
		t.Fatalf("expected remote head to remain unpublished, got %q", remoteHead)
	}
}

func TestWaitForLandedFailsFastOnManualPublishRepoWithoutPublishRequest(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	featurePath := filepath.Join(t.TempDir(), "feature-manual-wait")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/manual-wait", featurePath)
	writeFileAndCommit(t, featurePath, "manual-wait.txt", "manual wait\n", "feature manual wait")

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	if err := runSubmit([]string{"--repo", featurePath, "--wait", "--json", "--timeout", "30s", "--poll-interval", "10ms"}, newStepPrinter(&submitOut), &submitErr); err != nil {
		t.Fatalf("runSubmit returned error: %v", err)
	}

	var submitResult submitResult
	if err := json.Unmarshal(submitOut.Bytes(), &submitResult); err != nil {
		t.Fatalf("Unmarshal submit result: %v", err)
	}
	if submitResult.SubmissionID == 0 {
		t.Fatalf("expected submission id, got %+v", submitResult)
	}

	var waitOut bytes.Buffer
	var waitErr bytes.Buffer
	err := runWait([]string{"--repo", repoRoot, "--submission", fmt.Sprintf("%d", submitResult.SubmissionID), "--for", "landed", "--json", "--timeout", "30s", "--poll-interval", "10ms"}, newStepPrinter(&waitOut), &waitErr)
	if err == nil {
		t.Fatalf("expected wait --for landed to fail fast on manual publish repo")
	}

	var result submissionWaitResult
	if decodeErr := json.Unmarshal(waitOut.Bytes(), &result); decodeErr != nil {
		t.Fatalf("Unmarshal wait result: %v", decodeErr)
	}
	if result.SubmissionStatus != "succeeded" || result.Outcome != waitOutcome("integrated") {
		t.Fatalf("expected integrated wait result, got %+v", result)
	}
	if result.PublishRequestID != 0 {
		t.Fatalf("expected no publish request on manual repo, got %+v", result)
	}
	if !strings.Contains(result.Error, "publish mode is manual") {
		t.Fatalf("expected manual publish error, got %+v", result)
	}
}

func TestSubmitTextShowsRoundedEstimatedCompletion(t *testing.T) {
	t.Setenv("MAINLINE_DISABLE_SUBMIT_DRAIN", "1")

	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	layout, err := git.DiscoverRepositoryLayout(repoRoot)
	if err != nil {
		t.Fatalf("DiscoverRepositoryLayout: %v", err)
	}
	store := state.NewStore(state.DefaultPath(layout.GitDir))
	repoRecord, err := store.GetRepositoryByPath(context.Background(), layout.RepositoryRoot)
	if err != nil {
		t.Fatalf("GetRepositoryByPath: %v", err)
	}

	startedAt := time.Now().UTC().Add(-9 * time.Minute)
	seedSubmission, err := store.CreateIntegrationSubmission(context.Background(), state.IntegrationSubmission{
		RepoID:         repoRecord.ID,
		BranchName:     "feature/estimate-first",
		SourceRef:      "feature/estimate-first",
		RefKind:        submissionRefKindBranch,
		SourceWorktree: repoRoot,
		SourceSHA:      "seed",
		Status:         "succeeded",
	})
	if err != nil {
		t.Fatalf("CreateIntegrationSubmission(seed): %v", err)
	}
	db, err := sql.Open("sqlite", state.DefaultPath(layout.GitDir))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		INSERT INTO events (repo_id, item_type, item_id, event_type, payload, created_at)
		VALUES (?, 'integration_submission', ?, 'integration.started', ?, ?)
	`, repoRecord.ID, seedSubmission.ID, []byte(`{"branch":"feature/estimate-first","protected_sha":"seed"}`), startedAt); err != nil {
		t.Fatalf("insert integration.started: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO events (repo_id, item_type, item_id, event_type, payload, created_at)
		VALUES (?, 'integration_submission', ?, 'integration.succeeded', ?, ?)
	`, repoRecord.ID, seedSubmission.ID, []byte(`{"branch":"feature/estimate-first","protected_sha":"seed","landed_sha":"seed"}`), time.Now().UTC()); err != nil {
		t.Fatalf("insert integration.succeeded: %v", err)
	}

	secondPath := filepath.Join(t.TempDir(), "feature-estimate-second")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/estimate-second", secondPath)
	writeFileAndCommit(t, secondPath, "second.txt", "second\n", "feature estimate second")

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	if err := runSubmit([]string{"--repo", secondPath}, newStepPrinter(&submitOut), &submitErr); err != nil {
		t.Fatalf("runSubmit returned error: %v", err)
	}

	text := submitOut.String()
	if !strings.Contains(text, "Queue position: 1") {
		t.Fatalf("expected queue position in text output, got %q", text)
	}
	if !strings.Contains(text, "Estimated completion: ~10m (integrated)") {
		t.Fatalf("expected rounded estimated completion in text output, got %q", text)
	}
	if !strings.Contains(text, "Follow: mq wait --submission ") {
		t.Fatalf("expected follow guidance in text output, got %q", text)
	}
}

func TestSubmitWaitReturnsBlockedExitCodeForConflict(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	featureOne := filepath.Join(t.TempDir(), "feature-one")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/one", featureOne)
	replaceFileAndCommit(t, featureOne, "README.md", "# alpha\n", "feature one")

	featureTwo := filepath.Join(t.TempDir(), "feature-two")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/two", featureTwo)
	replaceFileAndCommit(t, featureTwo, "README.md", "# beta\n", "feature two")

	if err := runSubmit([]string{"--repo", featureOne, "--wait", "--timeout", "30s", "--poll-interval", "10ms"}, newStepPrinter(&bytes.Buffer{}), &bytes.Buffer{}); err != nil {
		t.Fatalf("first runSubmit returned error: %v", err)
	}

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	err := runSubmit([]string{"--repo", featureTwo, "--wait", "--json", "--timeout", "30s", "--poll-interval", "10ms"}, newStepPrinter(&submitOut), &submitErr)
	if err == nil {
		t.Fatalf("expected blocked wait failure")
	}
	if got := CLIExitCode(err); got != 1 {
		t.Fatalf("expected exit code 1, got %d", got)
	}

	var result submitResult
	if err := json.Unmarshal(submitOut.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result.ErrorCode != "blocked" || result.Outcome != "blocked" || result.SubmissionStatus != "blocked" {
		t.Fatalf("expected blocked wait result, got %+v", result)
	}
}

func TestSubmitWaitReturnsTimeoutExitCodeWhenWorkerStaysBusy(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	featurePath := filepath.Join(t.TempDir(), "feature-timeout")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/timeout", featurePath)
	writeFileAndCommit(t, featurePath, "timeout.txt", "timeout\n", "feature timeout")

	layout, err := git.DiscoverRepositoryLayout(repoRoot)
	if err != nil {
		t.Fatalf("DiscoverRepositoryLayout: %v", err)
	}
	lockManager := state.NewLockManager(layout.RepositoryRoot, layout.GitDir)
	lease, err := lockManager.Acquire(state.IntegrationLock, "test-timeout")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lease.Release()

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	err = runSubmit([]string{"--repo", featurePath, "--wait", "--json", "--timeout", "20ms", "--poll-interval", "5ms"}, newStepPrinter(&submitOut), &submitErr)
	if err == nil {
		t.Fatalf("expected timeout wait failure")
	}
	if got := CLIExitCode(err); got != 2 {
		t.Fatalf("expected exit code 2, got %d", got)
	}

	var result submitResult
	if err := json.Unmarshal(submitOut.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result.ErrorCode != "timeout" || result.Outcome != "timed_out" {
		t.Fatalf("expected timeout wait result, got %+v", result)
	}
}

func TestSubmitWaitFailsIfSucceededSubmissionIsNotReachableFromProtectedBranch(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	featurePath := filepath.Join(t.TempDir(), "feature-verify")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/verify", featurePath)
	writeFileAndCommit(t, featurePath, "verify.txt", "verify\n", "feature verify")

	queued, err := queueSubmission(submitOptions{repoPath: featurePath})
	if err != nil {
		t.Fatalf("queueSubmission: %v", err)
	}
	if _, err := queued.Store.UpdateIntegrationSubmissionStatus(context.Background(), queued.Submission.ID, "succeeded", ""); err != nil {
		t.Fatalf("UpdateIntegrationSubmissionStatus: %v", err)
	}

	result, err := waitForIntegratedSubmission(queued, 100*time.Millisecond, time.Millisecond)
	if err == nil {
		t.Fatalf("expected verification failure")
	}
	if got := CLIExitCode(err); got != 1 {
		t.Fatalf("expected exit code 1, got %d", got)
	}
	if result.Outcome != waitOutcomeFailed {
		t.Fatalf("expected failed outcome, got %+v", result)
	}
	if !strings.Contains(result.Error, "has no integration.succeeded event with protected_sha") {
		t.Fatalf("expected reachability error, got %+v", result)
	}
}
