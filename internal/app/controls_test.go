package app

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/state"
)

func TestRetrySubmissionRequeuesBlockedWork(t *testing.T) {
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
	if err := runRetry([]string{"--repo", repoRoot, "--submission", strconv.FormatInt(blockedID, 10)}, &stdout, &stderr); err != nil {
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

	if err := runCancel([]string{"--repo", repoRoot, "--submission", strconv.FormatInt(submissionID, 10)}, &stdout, &stderr); err != nil {
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

	if err := runCancel([]string{"--repo", repoRoot, "--publish", strconv.FormatInt(requestID, 10)}, &stdout, &stderr); err != nil {
		t.Fatalf("runCancel returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Cancelled publish request") {
		t.Fatalf("expected publish cancel output, got %q", stdout.String())
	}

	if err := runRetry([]string{"--repo", repoRoot, "--publish", strconv.FormatInt(requestID, 10)}, &stdout, &stderr); err != nil {
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
	if err := runRetry([]string{"--repo", repoRoot, "--submission", strconv.FormatInt(blockedID, 10), "--json"}, &stdout, &stderr); err != nil {
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
	if err := runCancel([]string{"--repo", repoRoot, "--publish", strconv.FormatInt(requestID, 10), "--json"}, &stdout, &stderr); err != nil {
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
