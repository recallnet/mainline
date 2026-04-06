package app

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/recallnet/mainline/internal/domain"
	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/state"
)

func TestRetrySubmissionRequeuesBlockedWork(t *testing.T) {
	t.Setenv("MAINLINE_DISABLE_MUTATION_DRAIN", "1")
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	featureOne := t.TempDir() + "/feature-one"
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/one", featureOne)
	replaceFileAndCommit(t, featureOne, "README.md", "# alpha\n", "feature one")
	submitBranch(t, featureOne)

	featureTwo := t.TempDir() + "/feature-two"
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
	if err := runRetry([]string{"--repo", repoRoot, "--submission", strconv.FormatInt(blockedID, 10)}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runRetry returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Retried submission") {
		t.Fatalf("expected retry output, got %q", stdout.String())
	}

	retried, err := store.GetIntegrationSubmission(context.Background(), blockedID)
	if err != nil {
		t.Fatalf("GetIntegrationSubmission: %v", err)
	}
	if retried.Status != "queued" {
		t.Fatalf("expected retried submission queued, got %q", retried.Status)
	}
	if retried.LastError != "" {
		t.Fatalf("expected last error cleared on retry, got %q", retried.LastError)
	}
}

func TestCancelSubmissionMarksQueuedWorkCancelled(t *testing.T) {
	t.Setenv("MAINLINE_DISABLE_MUTATION_DRAIN", "1")
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	feature := t.TempDir() + "/feature-cancel"
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/cancel", feature)
	writeFileAndCommit(t, feature, "cancel.txt", "cancel\n", "feature cancel")
	submitBranch(t, feature)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
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
	submissionID := submissions[0].ID

	if err := runCancel([]string{"--repo", repoRoot, "--submission", strconv.FormatInt(submissionID, 10)}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCancel returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Cancelled submission") {
		t.Fatalf("expected cancel output, got %q", stdout.String())
	}
	submission, err := store.GetIntegrationSubmission(context.Background(), submissionID)
	if err != nil {
		t.Fatalf("GetIntegrationSubmission: %v", err)
	}
	if submission.Status != "cancelled" {
		t.Fatalf("expected cancelled submission, got %q", submission.Status)
	}
	events, err := store.ListEvents(context.Background(), repoRecord.ID, 10)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := false
	for _, event := range events {
		if event.EventType == "submission.cancelled" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected submission.cancelled event in history")
	}
}

func TestCancelAndRetryPublishRequest(t *testing.T) {
	t.Setenv("MAINLINE_DISABLE_MUTATION_DRAIN", "1")
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	queuePublish(t, repoRoot)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
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
	requestID := requests[0].ID

	if err := runCancel([]string{"--repo", repoRoot, "--publish", strconv.FormatInt(requestID, 10)}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCancel returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Cancelled publish request") {
		t.Fatalf("expected publish cancel output, got %q", stdout.String())
	}

	if err := runRetry([]string{"--repo", repoRoot, "--publish", strconv.FormatInt(requestID, 10)}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runRetry returned error: %v", err)
	}
	request, err := store.GetPublishRequest(context.Background(), requestID)
	if err != nil {
		t.Fatalf("GetPublishRequest: %v", err)
	}
	if request.Status != "queued" {
		t.Fatalf("expected retried publish queued, got %q", request.Status)
	}
}

func TestRetrySubmissionSupportsJSONOutput(t *testing.T) {
	t.Setenv("MAINLINE_DISABLE_MUTATION_DRAIN", "1")
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	featureOne := t.TempDir() + "/feature-one"
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/one", featureOne)
	replaceFileAndCommit(t, featureOne, "README.md", "# alpha\n", "feature one")
	submitBranch(t, featureOne)

	featureTwo := t.TempDir() + "/feature-two"
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
	if err := runRetry([]string{"--repo", repoRoot, "--submission", strconv.FormatInt(blockedID, 10), "--json"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runRetry returned error: %v", err)
	}

	var result controlResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !result.OK || result.Action != "retry" || result.ItemType != "submission" || result.ID != blockedID || result.Status != "queued" {
		t.Fatalf("unexpected retry json: %+v", result)
	}
}

func TestCancelPublishSupportsJSONOutput(t *testing.T) {
	t.Setenv("MAINLINE_DISABLE_MUTATION_DRAIN", "1")
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	queuePublish(t, repoRoot)

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
	requestID := requests[0].ID

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCancel([]string{"--repo", repoRoot, "--publish", strconv.FormatInt(requestID, 10), "--json"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCancel returned error: %v", err)
	}

	var result controlResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !result.OK || result.Action != "cancel" || result.ItemType != "publish_request" || result.ID != requestID || result.Status != "cancelled" {
		t.Fatalf("unexpected cancel json: %+v", result)
	}
}

func TestBlockedListsBlockedSubmissionsWithActions(t *testing.T) {
	t.Setenv("MAINLINE_DISABLE_MUTATION_DRAIN", "1")
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	featureOne := t.TempDir() + "/feature-one"
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/one", featureOne)
	replaceFileAndCommit(t, featureOne, "README.md", "# alpha\n", "feature one")
	submitBranch(t, featureOne)

	featureTwo := t.TempDir() + "/feature-two"
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/two", featureTwo)
	replaceFileAndCommit(t, featureTwo, "README.md", "# beta\n", "feature two")
	submitBranch(t, featureTwo)

	runOnce(t, repoRoot)
	runOnce(t, repoRoot)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runBlocked([]string{"--repo", repoRoot, "--json"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runBlocked returned error: %v", err)
	}

	var result blockedResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result.Count != 1 || len(result.Submissions) != 1 {
		t.Fatalf("expected one blocked submission, got %+v", result)
	}
	if result.Submissions[0].BlockedReason != domain.BlockedReasonRebaseConflict {
		t.Fatalf("expected rebase conflict blocked reason, got %+v", result.Submissions[0])
	}
	if len(result.Submissions[0].NextActions) == 0 || !strings.Contains(result.Submissions[0].NextActions[0].Command, "git rebase main") {
		t.Fatalf("expected local rebase action, got %+v", result.Submissions[0].NextActions)
	}
}

func TestRetryAllSafeRetriesOnlyCheckTimeoutBlockedSubmissions(t *testing.T) {
	t.Setenv("MAINLINE_DISABLE_MUTATION_DRAIN", "1")
	repoRoot, _ := createTestRepo(t)
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

	safeSubmission, err := store.CreateIntegrationSubmission(context.Background(), state.IntegrationSubmission{
		RepoID:         repoRecord.ID,
		BranchName:     "feature/safe",
		SourceRef:      "feature/safe",
		RefKind:        domain.RefKindBranch,
		SourceWorktree: t.TempDir() + "/feature-safe",
		SourceSHA:      "safe123",
		RequestedBy:    "test",
		Priority:       submissionPriorityNormal,
		Status:         domain.SubmissionStatusQueued,
	})
	if err != nil {
		t.Fatalf("CreateIntegrationSubmission(safe): %v", err)
	}
	safeSubmission, err = store.UpdateIntegrationSubmissionStatus(context.Background(), safeSubmission.ID, domain.SubmissionStatusBlocked, "timed out")
	if err != nil {
		t.Fatalf("UpdateIntegrationSubmissionStatus(safe): %v", err)
	}
	if _, err := store.AppendEvent(context.Background(), state.EventRecord{
		RepoID:    repoRecord.ID,
		ItemType:  domain.ItemTypeIntegrationSubmission,
		ItemID:    state.NullInt64(safeSubmission.ID),
		EventType: domain.EventTypeIntegrationBlocked,
		Payload: mustJSON(blockedSubmissionDetails{
			Error:           "timed out",
			BlockedReason:   domain.BlockedReasonCheckTimeout,
			ProtectedTipSHA: trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD")),
			RetryHint:       "rerun-after-fixing-hanging-check",
		}),
	}); err != nil {
		t.Fatalf("AppendEvent(safe): %v", err)
	}

	unsafeSubmission, err := store.CreateIntegrationSubmission(context.Background(), state.IntegrationSubmission{
		RepoID:         repoRecord.ID,
		BranchName:     "feature/unsafe",
		SourceRef:      "feature/unsafe",
		RefKind:        domain.RefKindBranch,
		SourceWorktree: t.TempDir() + "/feature-unsafe",
		SourceSHA:      "unsafe123",
		RequestedBy:    "test",
		Priority:       submissionPriorityNormal,
		Status:         domain.SubmissionStatusQueued,
	})
	if err != nil {
		t.Fatalf("CreateIntegrationSubmission(unsafe): %v", err)
	}
	unsafeSubmission, err = store.UpdateIntegrationSubmissionStatus(context.Background(), unsafeSubmission.ID, domain.SubmissionStatusBlocked, "rebase conflict")
	if err != nil {
		t.Fatalf("UpdateIntegrationSubmissionStatus(unsafe): %v", err)
	}
	if _, err := store.AppendEvent(context.Background(), state.EventRecord{
		RepoID:    repoRecord.ID,
		ItemType:  domain.ItemTypeIntegrationSubmission,
		ItemID:    state.NullInt64(unsafeSubmission.ID),
		EventType: domain.EventTypeIntegrationBlocked,
		Payload: mustJSON(blockedSubmissionDetails{
			Error:           "rebase conflict",
			BlockedReason:   domain.BlockedReasonRebaseConflict,
			ConflictFiles:   []string{"README.md"},
			ProtectedTipSHA: trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD")),
			RetryHint:       "manual-rebase-from-tip",
		}),
	}); err != nil {
		t.Fatalf("AppendEvent(unsafe): %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runRetry([]string{"--repo", repoRoot, "--all-safe", "--json"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runRetry returned error: %v", err)
	}

	var result batchControlResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result.Count != 1 || len(result.Results) != 1 || result.Results[0].ID != safeSubmission.ID {
		t.Fatalf("expected only safe blocked retry, got %+v", result)
	}
	refreshedSafe, err := store.GetIntegrationSubmission(context.Background(), safeSubmission.ID)
	if err != nil {
		t.Fatalf("GetIntegrationSubmission(safe): %v", err)
	}
	if refreshedSafe.Status != domain.SubmissionStatusQueued {
		t.Fatalf("expected safe submission queued, got %+v", refreshedSafe)
	}
	refreshedUnsafe, err := store.GetIntegrationSubmission(context.Background(), unsafeSubmission.ID)
	if err != nil {
		t.Fatalf("GetIntegrationSubmission(unsafe): %v", err)
	}
	if refreshedUnsafe.Status != domain.SubmissionStatusBlocked {
		t.Fatalf("expected unsafe submission still blocked, got %+v", refreshedUnsafe)
	}
}

func TestCancelBlockedCancelsAllBlockedSubmissions(t *testing.T) {
	t.Setenv("MAINLINE_DISABLE_MUTATION_DRAIN", "1")
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	featureOne := t.TempDir() + "/feature-one"
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/one", featureOne)
	replaceFileAndCommit(t, featureOne, "README.md", "# alpha\n", "feature one")
	submitBranch(t, featureOne)

	featureTwo := t.TempDir() + "/feature-two"
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/two", featureTwo)
	replaceFileAndCommit(t, featureTwo, "README.md", "# beta\n", "feature two")
	submitBranch(t, featureTwo)

	runOnce(t, repoRoot)
	runOnce(t, repoRoot)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCancel([]string{"--repo", repoRoot, "--blocked", "--json"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCancel returned error: %v", err)
	}

	var result batchControlResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result.Count != 1 || len(result.Results) != 1 {
		t.Fatalf("expected one blocked cancellation, got %+v", result)
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
	if submissions[1].Status != domain.SubmissionStatusCancelled {
		t.Fatalf("expected blocked submission cancelled, got %+v", submissions[1])
	}
}
