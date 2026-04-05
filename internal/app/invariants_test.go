package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/state"
)

func TestInvariantSuccessfulIntegrationPublishLeavesConsistentState(t *testing.T) {
	repoRoot, remoteDir := createTestRepoWithRemote(t)
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
	queuePublish(t, repoRoot)
	runOnce(t, repoRoot)

	assertProtectedWorktreeClean(t, repoRoot)
	assertRemoteHeadMatchesLocal(t, repoRoot, remoteDir)

	status := readStatusJSON(t, repoRoot)
	if status.Counts.QueuedSubmissions != 0 || status.Counts.QueuedPublishes != 0 {
		t.Fatalf("expected no queued work, got %+v", status.Counts)
	}
	if status.LatestSubmission == nil || status.LatestSubmission.Status != "succeeded" {
		t.Fatalf("expected latest submission succeeded, got %+v", status.LatestSubmission)
	}
	if status.LatestPublish == nil || status.LatestPublish.Status != "succeeded" {
		t.Fatalf("expected latest publish succeeded, got %+v", status.LatestPublish)
	}

	events := readRecentEvents(t, repoRoot, 10)
	assertEventPresent(t, events, "integration.succeeded")
	assertEventPresent(t, events, "publish.completed")
}

func TestInvariantAutoPublishMaintainsCleanProtectedState(t *testing.T) {
	repoRoot, remoteDir := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	updatePublishMode(t, repoRoot, "auto")

	feature := filepath.Join(t.TempDir(), "feature-auto")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/auto", feature)
	writeFileAndCommit(t, feature, "auto.txt", "auto\n", "feature auto")
	submitBranch(t, feature)

	runOnce(t, repoRoot)
	runOnce(t, repoRoot)

	assertProtectedWorktreeClean(t, repoRoot)
	assertRemoteHeadMatchesLocal(t, repoRoot, remoteDir)

	status := readStatusJSON(t, repoRoot)
	if status.Counts.QueuedSubmissions != 0 || status.Counts.QueuedPublishes != 0 {
		t.Fatalf("expected no queued work after auto publish, got %+v", status.Counts)
	}
	if status.LatestSubmission == nil || status.LatestSubmission.Status != "succeeded" {
		t.Fatalf("expected succeeded submission after auto publish, got %+v", status.LatestSubmission)
	}
	if status.LatestPublish == nil || status.LatestPublish.Status != "succeeded" {
		t.Fatalf("expected succeeded auto publish, got %+v", status.LatestPublish)
	}

	events := readRecentEvents(t, repoRoot, 10)
	assertEventPresent(t, events, "publish.requested")
	assertEventPresent(t, events, "publish.completed")
}

func TestInvariantBlockedSubmissionDoesNotAdvanceProtectedBranch(t *testing.T) {
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
	protectedAfterFirst := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	runOnce(t, repoRoot)

	protectedAfterSecond := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	if protectedAfterFirst != protectedAfterSecond {
		t.Fatalf("expected protected branch unchanged after blocked submission, got %q then %q", protectedAfterFirst, protectedAfterSecond)
	}
	assertProtectedWorktreeClean(t, repoRoot)

	status := readStatusJSON(t, repoRoot)
	if status.LatestSubmission == nil || status.LatestSubmission.Status != "blocked" {
		t.Fatalf("expected latest submission blocked, got %+v", status.LatestSubmission)
	}
	if status.Counts.BlockSubmissions != 1 {
		t.Fatalf("expected 1 blocked submission, got %+v", status.Counts)
	}
}

func TestInvariantCancelledSubmissionNeverLands(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	feature := filepath.Join(t.TempDir(), "feature-cancel")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/cancel", feature)
	writeFileAndCommit(t, feature, "cancel.txt", "cancel\n", "feature cancel")
	submitBranch(t, feature)

	store, repoRecord := openRepoStore(t, repoRoot)
	submissions, err := store.ListIntegrationSubmissions(context.Background(), repoRecord.ID)
	if err != nil {
		t.Fatalf("ListIntegrationSubmissions: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCancel([]string{"--repo", repoRoot, "--submission", strconv.FormatInt(submissions[0].ID, 10)}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCancel returned error: %v", err)
	}
	runOnce(t, repoRoot)

	if output := runTestCommand(t, repoRoot, "git", "ls-files", "--", "cancel.txt"); strings.TrimSpace(output) != "" {
		t.Fatalf("expected cancelled change to never land, got %q", output)
	}
	assertProtectedWorktreeClean(t, repoRoot)

	status := readStatusJSON(t, repoRoot)
	if status.LatestSubmission == nil || status.LatestSubmission.Status != "cancelled" {
		t.Fatalf("expected latest submission cancelled, got %+v", status.LatestSubmission)
	}
}

func TestInvariantCancelledPublishDoesNotPushUntilRetried(t *testing.T) {
	repoRoot, remoteDir := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	writeFileAndCommit(t, repoRoot, "publish.txt", "publish\n", "publish me")
	localHead := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	remoteBefore := trimNewline(runTestCommand(t, remoteDir, "git", "rev-parse", "refs/heads/main"))
	queuePublish(t, repoRoot)

	store, repoRecord := openRepoStore(t, repoRoot)
	requests, err := store.ListPublishRequests(context.Background(), repoRecord.ID)
	if err != nil {
		t.Fatalf("ListPublishRequests: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	requestID := requests[0].ID
	if err := runCancel([]string{"--repo", repoRoot, "--publish", strconv.FormatInt(requestID, 10)}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCancel returned error: %v", err)
	}
	runOnce(t, repoRoot)

	remoteAfterCancel := trimNewline(runTestCommand(t, remoteDir, "git", "rev-parse", "refs/heads/main"))
	if remoteAfterCancel != remoteBefore {
		t.Fatalf("expected cancelled publish not to push, got %q then %q", remoteBefore, remoteAfterCancel)
	}

	stdout.Reset()
	stderr.Reset()
	if err := runRetry([]string{"--repo", repoRoot, "--publish", strconv.FormatInt(requestID, 10)}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runRetry returned error: %v", err)
	}
	runOnce(t, repoRoot)

	remoteAfterRetry := trimNewline(runTestCommand(t, remoteDir, "git", "rev-parse", "refs/heads/main"))
	if remoteAfterRetry != localHead {
		t.Fatalf("expected retried publish to push %q, got %q", localHead, remoteAfterRetry)
	}
}

func TestInvariantInvalidControlActionsDoNotMutateCompletedState(t *testing.T) {
	repoRoot, remoteDir := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	feature := filepath.Join(t.TempDir(), "feature-complete")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/complete", feature)
	writeFileAndCommit(t, feature, "complete.txt", "complete\n", "feature complete")
	submitBranch(t, feature)
	runOnce(t, repoRoot)
	queuePublish(t, repoRoot)
	runOnce(t, repoRoot)

	assertRemoteHeadMatchesLocal(t, repoRoot, remoteDir)

	store, repoRecord := openRepoStore(t, repoRoot)
	submissions, err := store.ListIntegrationSubmissions(context.Background(), repoRecord.ID)
	if err != nil {
		t.Fatalf("ListIntegrationSubmissions: %v", err)
	}
	requests, err := store.ListPublishRequests(context.Background(), repoRecord.ID)
	if err != nil {
		t.Fatalf("ListPublishRequests: %v", err)
	}
	if len(submissions) != 1 || len(requests) != 1 {
		t.Fatalf("expected one completed submission and publish, got submissions=%d requests=%d", len(submissions), len(requests))
	}

	beforeEvents, err := store.ListEvents(context.Background(), repoRecord.ID, 100)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCancel([]string{"--repo", repoRoot, "--submission", strconv.FormatInt(submissions[0].ID, 10)}, newStepPrinter(&stdout), &stderr); err == nil {
		t.Fatalf("expected cancelling succeeded submission to fail")
	}
	if err := runRetry([]string{"--repo", repoRoot, "--publish", strconv.FormatInt(requests[0].ID, 10)}, newStepPrinter(&stdout), &stderr); err == nil {
		t.Fatalf("expected retrying succeeded publish to fail")
	}

	afterSubmission, err := store.GetIntegrationSubmission(context.Background(), submissions[0].ID)
	if err != nil {
		t.Fatalf("GetIntegrationSubmission: %v", err)
	}
	if afterSubmission.Status != "succeeded" {
		t.Fatalf("expected submission to remain succeeded, got %q", afterSubmission.Status)
	}
	afterPublish, err := store.GetPublishRequest(context.Background(), requests[0].ID)
	if err != nil {
		t.Fatalf("GetPublishRequest: %v", err)
	}
	if afterPublish.Status != "succeeded" {
		t.Fatalf("expected publish to remain succeeded, got %q", afterPublish.Status)
	}

	afterEvents, err := store.ListEvents(context.Background(), repoRecord.ID, 100)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(afterEvents) != len(beforeEvents) {
		t.Fatalf("expected invalid control actions to append no events, got before=%d after=%d", len(beforeEvents), len(afterEvents))
	}
}

func assertProtectedWorktreeClean(t *testing.T, repoRoot string) {
	t.Helper()

	clean, err := git.NewEngine(repoRoot).WorktreeIsClean(repoRoot)
	if err != nil {
		t.Fatalf("WorktreeIsClean: %v", err)
	}
	if !clean {
		t.Fatalf("expected protected worktree clean")
	}
}

func assertRemoteHeadMatchesLocal(t *testing.T, repoRoot string, remoteDir string) {
	t.Helper()

	localHead := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	remoteHead := trimNewline(runTestCommand(t, remoteDir, "git", "rev-parse", "refs/heads/main"))
	if remoteHead != localHead {
		t.Fatalf("expected remote head %q, got %q", localHead, remoteHead)
	}
}

func readStatusJSON(t *testing.T, repoRoot string) statusResult {
	t.Helper()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLI([]string{"status", "--repo", repoRoot, "--json", "--events", "10"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCLI status returned error: %v", err)
	}

	var status statusResult
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("Unmarshal status: %v", err)
	}
	return status
}

func readRecentEvents(t *testing.T, repoRoot string, limit int) []state.EventRecord {
	t.Helper()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runEvents([]string{"--repo", repoRoot, "--json", "--limit", strconv.Itoa(limit)}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runEvents returned error: %v", err)
	}

	var events []state.EventRecord
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	for {
		var event state.EventRecord
		err := decoder.Decode(&event)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Decode event stream: %v", err)
		}
		events = append(events, event)
	}
	return events
}

func assertEventPresent(t *testing.T, events []state.EventRecord, eventType string) {
	t.Helper()

	for _, event := range events {
		if string(event.EventType) == eventType {
			return
		}
	}
	t.Fatalf("expected event %q in %+v", eventType, events)
}

func openRepoStore(t *testing.T, repoRoot string) (state.Store, state.RepositoryRecord) {
	t.Helper()

	layout, err := git.DiscoverRepositoryLayout(repoRoot)
	if err != nil {
		t.Fatalf("DiscoverRepositoryLayout: %v", err)
	}
	store := state.NewStore(state.DefaultPath(layout.GitDir))
	repoRecord, err := store.GetRepositoryByPath(context.Background(), layout.RepositoryRoot)
	if err != nil {
		t.Fatalf("GetRepositoryByPath: %v", err)
	}
	return store, repoRecord
}
