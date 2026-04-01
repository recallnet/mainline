package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
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

func TestRunOnceRejectsDirtyCanonicalRootCheckout(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	if err := os.WriteFile(filepath.Join(repoRoot, "DIRTY.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(DIRTY.txt): %v", err)
	}

	var runOut bytes.Buffer
	var runErr bytes.Buffer
	err := runRunOnce([]string{"--repo", repoRoot}, &runOut, &runErr)
	if err == nil {
		t.Fatalf("expected run-once to fail when canonical root is dirty")
	}
	if !strings.Contains(err.Error(), "protected branch worktree") || !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("expected dirty protected worktree error, got %v", err)
	}
}

func TestRunOnceIntegratesHigherPriorityBranchesFirst(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	lowPath := filepath.Join(t.TempDir(), "feature-low")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/low", lowPath)
	writeFileAndCommit(t, lowPath, "low.txt", "low\n", "feature low")
	submitBranchWithArgs(t, lowPath, "--priority", submissionPriorityLow)

	highPath := filepath.Join(t.TempDir(), "feature-high")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/high", highPath)
	writeFileAndCommit(t, highPath, "high.txt", "high\n", "feature high")
	submitBranchWithArgs(t, highPath, "--priority", submissionPriorityHigh)

	normalPath := filepath.Join(t.TempDir(), "feature-normal")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/normal", normalPath)
	writeFileAndCommit(t, normalPath, "normal.txt", "normal\n", "feature normal")
	submitBranchWithArgs(t, normalPath, "--priority", submissionPriorityNormal)

	runOnce(t, repoRoot)
	runOnce(t, repoRoot)
	runOnce(t, repoRoot)

	logOutput := runTestCommand(t, repoRoot, "git", "log", "--format=%s", "-3")
	if logOutput != "feature low\nfeature normal\nfeature high\n" {
		t.Fatalf("expected high->normal->low integration order, got %q", logOutput)
	}
}

func TestRunOnceIntegratesDetachedHeadSubmissionBySHA(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	detachedPath := filepath.Join(t.TempDir(), "detached-feature")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "--detach", detachedPath)
	writeFileAndCommit(t, detachedPath, "detached.txt", "detached\n", "detached feature")
	runTestCommand(t, detachedPath, "git", "checkout", "--detach", "HEAD")

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	if err := runSubmit([]string{"--repo", detachedPath, "--json"}, &submitOut, &submitErr); err != nil {
		t.Fatalf("runSubmit returned error: %v", err)
	}

	runOnce(t, repoRoot)

	if _, err := os.Stat(filepath.Join(repoRoot, "detached.txt")); err != nil {
		t.Fatalf("expected detached.txt after integration: %v", err)
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
	if len(submissions) != 1 || submissions[0].RefKind != submissionRefKindSHA || submissions[0].Status != "succeeded" {
		t.Fatalf("expected succeeded sha submission, got %+v", submissions)
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

	var statusOut bytes.Buffer
	var statusErr bytes.Buffer
	if err := runStatus([]string{"--repo", repoRoot, "--json", "--events", "10"}, &statusOut, &statusErr); err != nil {
		t.Fatalf("runStatus returned error: %v", err)
	}

	var report statusResult
	if err := json.Unmarshal(statusOut.Bytes(), &report); err != nil {
		t.Fatalf("Unmarshal status: %v", err)
	}
	if report.LatestSubmission == nil {
		t.Fatalf("expected latest submission in status report")
	}
	if report.LatestSubmission.Status != "blocked" {
		t.Fatalf("expected blocked latest submission, got %+v", report.LatestSubmission)
	}
	if report.LatestSubmission.ProtectedTipSHA != strings.TrimSpace(protectedAfterFirst) {
		t.Fatalf("expected protected tip %q, got %+v", strings.TrimSpace(protectedAfterFirst), report.LatestSubmission)
	}
	if report.LatestSubmission.RetryHint != "manual-rebase-from-tip" {
		t.Fatalf("expected retry hint, got %+v", report.LatestSubmission)
	}
	if len(report.LatestSubmission.ConflictFiles) == 0 || report.LatestSubmission.ConflictFiles[0] != "README.md" {
		t.Fatalf("expected conflict files for blocked submission, got %+v", report.LatestSubmission)
	}
}

func TestStatusBlockedSubmissionUsesLatestConflictDiagnosticsWhenHistoryIsLong(t *testing.T) {
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
	for i := 0; i < 12; i++ {
		if _, err := store.AppendEvent(context.Background(), state.EventRecord{
			RepoID:    repoRecord.ID,
			ItemType:  "integration_submission",
			ItemID:    state.NullInt64(blockedID),
			EventType: "integration.debug",
			Payload:   mustJSON(map[string]any{"ordinal": i}),
		}); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}

	runOnce(t, repoRoot)
	protectedAfterFirst := strings.TrimSpace(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))

	var runOut bytes.Buffer
	var runErr bytes.Buffer
	if err := runRunOnce([]string{"--repo", repoRoot}, &runOut, &runErr); err != nil {
		t.Fatalf("runRunOnce returned error: %v", err)
	}

	var statusOut bytes.Buffer
	var statusErr bytes.Buffer
	if err := runStatus([]string{"--repo", repoRoot, "--json", "--events", "10"}, &statusOut, &statusErr); err != nil {
		t.Fatalf("runStatus returned error: %v", err)
	}

	var report statusResult
	if err := json.Unmarshal(statusOut.Bytes(), &report); err != nil {
		t.Fatalf("Unmarshal status: %v", err)
	}
	if report.LatestSubmission == nil || report.LatestSubmission.Status != "blocked" {
		t.Fatalf("expected blocked latest submission, got %+v", report.LatestSubmission)
	}
	if report.LatestSubmission.ProtectedTipSHA != protectedAfterFirst {
		t.Fatalf("expected protected tip %q, got %+v", protectedAfterFirst, report.LatestSubmission)
	}
	if len(report.LatestSubmission.ConflictFiles) == 0 || report.LatestSubmission.ConflictFiles[0] != "README.md" {
		t.Fatalf("expected latest blocked diagnostics, got %+v", report.LatestSubmission)
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

func TestRunOnceReportsProtectedBranchSyncFromUpstream(t *testing.T) {
	repoRoot, remoteDir := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	runTestCommand(t, repoRoot, "git", "push", "origin", "main")

	upstreamClone := filepath.Join(t.TempDir(), "upstream-clone")
	runTestCommand(t, t.TempDir(), "git", "clone", remoteDir, upstreamClone)
	runTestCommand(t, upstreamClone, "git", "config", "user.name", "Test User")
	runTestCommand(t, upstreamClone, "git", "config", "user.email", "test@example.com")
	runTestCommand(t, upstreamClone, "git", "config", "core.hooksPath", ".git/hooks")
	writeFileAndCommit(t, upstreamClone, "upstream.txt", "upstream\n", "upstream advance")
	upstreamHead := trimNewline(runTestCommand(t, upstreamClone, "git", "rev-parse", "HEAD"))
	runTestCommand(t, upstreamClone, "git", "push", "origin", "main")

	featureOne := filepath.Join(t.TempDir(), "feature-one")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/one", featureOne)
	writeFileAndCommit(t, featureOne, "one.txt", "feature one\n", "feature one")
	submitBranch(t, featureOne)

	var runOut bytes.Buffer
	var runErr bytes.Buffer
	if err := runRunOnce([]string{"--repo", repoRoot}, &runOut, &runErr); err != nil {
		t.Fatalf("runRunOnce returned error: %v", err)
	}
	output := runOut.String()
	if !strings.Contains(output, "Synced main from origin/main and integrated submission") {
		t.Fatalf("expected sync-aware integration output, got %q", output)
	}

	if got := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD^")); got != upstreamHead {
		t.Fatalf("expected upstream commit %q to be incorporated before feature commit, got parent %q", upstreamHead, got)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "upstream.txt")); err != nil {
		t.Fatalf("expected upstream.txt after sync, got %v", err)
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
	events, err := store.ListEvents(context.Background(), repoRecord.ID, 20)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	foundSync := false
	for _, event := range events {
		if event.EventType != "protected.synced_from_upstream" {
			continue
		}
		foundSync = true
		if event.ItemType != "repository" {
			t.Fatalf("expected repository event item type, got %q", event.ItemType)
		}
		if !strings.Contains(string(event.Payload), "\"upstream\":\"origin/main\"") {
			t.Fatalf("expected upstream payload, got %s", string(event.Payload))
		}
		if !strings.Contains(string(event.Payload), upstreamHead) {
			t.Fatalf("expected payload to reference synced SHA %q, got %s", upstreamHead, string(event.Payload))
		}
	}
	if !foundSync {
		t.Fatalf("expected protected.synced_from_upstream event, got %+v", events)
	}
}

func TestRunOnceReportsProtectedBranchSyncBeforeConflictBlock(t *testing.T) {
	repoRoot, remoteDir := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	runTestCommand(t, repoRoot, "git", "push", "origin", "main")

	upstreamClone := filepath.Join(t.TempDir(), "upstream-clone")
	runTestCommand(t, t.TempDir(), "git", "clone", remoteDir, upstreamClone)
	runTestCommand(t, upstreamClone, "git", "config", "user.name", "Test User")
	runTestCommand(t, upstreamClone, "git", "config", "user.email", "test@example.com")
	runTestCommand(t, upstreamClone, "git", "config", "core.hooksPath", ".git/hooks")
	replaceFileAndCommit(t, upstreamClone, "README.md", "# upstream\n", "upstream advance")
	runTestCommand(t, upstreamClone, "git", "push", "origin", "main")

	featureOne := filepath.Join(t.TempDir(), "feature-one")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/one", featureOne)
	replaceFileAndCommit(t, featureOne, "README.md", "# feature\n", "feature one")
	submitBranch(t, featureOne)

	var runOut bytes.Buffer
	var runErr bytes.Buffer
	if err := runRunOnce([]string{"--repo", repoRoot}, &runOut, &runErr); err != nil {
		t.Fatalf("runRunOnce returned error: %v", err)
	}
	output := runOut.String()
	if !strings.Contains(output, "Synced protected branch from origin/main before blocked submission") {
		t.Fatalf("expected sync-aware blocked output, got %q", output)
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
	if len(submissions) != 1 || submissions[0].Status != "blocked" {
		t.Fatalf("expected blocked submission, got %+v", submissions)
	}
}

func TestCreateTestRepoWithRemoteCloneChecksOutMain(t *testing.T) {
	_, remoteDir := createTestRepoWithRemote(t)

	upstreamClone := filepath.Join(t.TempDir(), "upstream-clone")
	runTestCommand(t, t.TempDir(), "git", "clone", remoteDir, upstreamClone)

	branch := strings.TrimSpace(runTestCommand(t, upstreamClone, "git", "branch", "--show-current"))
	if branch != "main" {
		t.Fatalf("expected clone to check out main, got %q", branch)
	}
}

func TestRunOncePreIntegrateChecksBlockBeforeProtectedBranchMutation(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	cfg, _, err := policy.LoadOrDefault(repoRoot)
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	cfg.Checks.PreIntegrate = []string{"exit 5"}
	if err := policy.SaveFile(repoRoot, cfg); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}
	runTestCommand(t, repoRoot, "git", "add", "mainline.toml")
	runTestCommand(t, repoRoot, "git", "commit", "-m", "configure pre integrate check")

	featureOne := filepath.Join(t.TempDir(), "feature-one")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/checks", featureOne)
	writeFileAndCommit(t, featureOne, "one.txt", "feature one\n", "feature one")
	submitBranch(t, featureOne)

	protectedBefore := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	var runOut bytes.Buffer
	var runErr bytes.Buffer
	if err := runRunOnce([]string{"--repo", repoRoot}, &runOut, &runErr); err != nil {
		t.Fatalf("runRunOnce returned error: %v", err)
	}
	if !strings.Contains(runOut.String(), "pre-integrate checks failed") {
		t.Fatalf("expected pre-integrate failure output, got %q", runOut.String())
	}
	protectedAfter := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	if protectedBefore != protectedAfter {
		t.Fatalf("expected protected branch unchanged, got %q then %q", protectedBefore, protectedAfter)
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
	if len(submissions) != 1 || submissions[0].Status != "blocked" {
		t.Fatalf("expected blocked submission, got %+v", submissions)
	}
}

func TestRunOnceFailsWhenSubmittedSourceWorktreeIsDeleted(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	featurePath := filepath.Join(t.TempDir(), "feature-deleted")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/deleted", featurePath)
	writeFileAndCommit(t, featurePath, "deleted.txt", "deleted\n", "deleted feature")
	submitBranch(t, featurePath)

	if err := os.RemoveAll(featurePath); err != nil {
		t.Fatalf("RemoveAll(featurePath): %v", err)
	}

	var runOut bytes.Buffer
	var runErr bytes.Buffer
	if err := runRunOnce([]string{"--repo", repoRoot}, &runOut, &runErr); err != nil {
		t.Fatalf("runRunOnce returned error: %v", err)
	}
	if !strings.Contains(runOut.String(), "source worktree is unavailable") {
		t.Fatalf("expected missing worktree failure, got %q", runOut.String())
	}

	status := readStatusJSON(t, repoRoot)
	if status.LatestSubmission == nil || status.LatestSubmission.Status != "failed" {
		t.Fatalf("expected failed latest submission, got %+v", status.LatestSubmission)
	}
	if !strings.Contains(status.LatestSubmission.LastError, "source worktree is unavailable") {
		t.Fatalf("expected missing worktree error, got %+v", status.LatestSubmission)
	}
}

func TestRunOnceFailsWhenSubmittedSourceWorktreeMovesAfterSubmit(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	featurePath := filepath.Join(t.TempDir(), "feature-moved")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/moved", featurePath)
	writeFileAndCommit(t, featurePath, "moved.txt", "moved\n", "moved feature")
	submitBranch(t, featurePath)

	movedPath := filepath.Join(t.TempDir(), "feature-moved-new")
	if err := os.Rename(featurePath, movedPath); err != nil {
		t.Fatalf("Rename(featurePath): %v", err)
	}

	var runOut bytes.Buffer
	var runErr bytes.Buffer
	if err := runRunOnce([]string{"--repo", repoRoot}, &runOut, &runErr); err != nil {
		t.Fatalf("runRunOnce returned error: %v", err)
	}
	if !strings.Contains(runOut.String(), "source worktree is unavailable") {
		t.Fatalf("expected moved worktree failure, got %q", runOut.String())
	}

	status := readStatusJSON(t, repoRoot)
	if status.LatestSubmission == nil || status.LatestSubmission.Status != "failed" {
		t.Fatalf("expected failed latest submission, got %+v", status.LatestSubmission)
	}
}

func TestRunOnceFailsWhenSubmittedSourceWorktreeTurnsDirtyAfterSubmit(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	featurePath := filepath.Join(t.TempDir(), "feature-dirty-later")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/dirty-later", featurePath)
	writeFileAndCommit(t, featurePath, "clean.txt", "clean\n", "clean feature")
	submitBranch(t, featurePath)

	if err := os.WriteFile(filepath.Join(featurePath, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(dirty.txt): %v", err)
	}

	var runOut bytes.Buffer
	var runErr bytes.Buffer
	if err := runRunOnce([]string{"--repo", repoRoot}, &runOut, &runErr); err != nil {
		t.Fatalf("runRunOnce returned error: %v", err)
	}
	if !strings.Contains(runOut.String(), "is dirty; clean it and resubmit") {
		t.Fatalf("expected dirty source failure, got %q", runOut.String())
	}

	status := readStatusJSON(t, repoRoot)
	if status.LatestSubmission == nil || status.LatestSubmission.Status != "failed" {
		t.Fatalf("expected failed latest submission, got %+v", status.LatestSubmission)
	}
}

func TestRunOnceFailsWhenQueuedBranchHeadDriftsAfterSubmit(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	featurePath := filepath.Join(t.TempDir(), "feature-head-drift")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/head-drift", featurePath)
	writeFileAndCommit(t, featurePath, "head-drift.txt", "one\n", "head drift one")
	submitBranch(t, featurePath)
	submittedSHA := trimNewline(runTestCommand(t, featurePath, "git", "rev-parse", "HEAD"))

	writeFileAndCommit(t, featurePath, "head-drift.txt", "two\n", "head drift two")
	driftedSHA := trimNewline(runTestCommand(t, featurePath, "git", "rev-parse", "HEAD"))
	if driftedSHA == submittedSHA {
		t.Fatalf("expected branch head to move after second commit")
	}

	protectedBefore := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))

	var runOut bytes.Buffer
	var runErr bytes.Buffer
	if err := runRunOnce([]string{"--repo", repoRoot}, &runOut, &runErr); err != nil {
		t.Fatalf("runRunOnce returned error: %v", err)
	}
	if !strings.Contains(runOut.String(), "moved from submitted SHA") {
		t.Fatalf("expected branch drift failure, got %q", runOut.String())
	}

	protectedAfter := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	if protectedAfter != protectedBefore {
		t.Fatalf("expected protected branch to remain unchanged, got %q then %q", protectedBefore, protectedAfter)
	}

	status := readStatusJSON(t, repoRoot)
	if status.LatestSubmission == nil || status.LatestSubmission.Status != "failed" {
		t.Fatalf("expected failed latest submission, got %+v", status.LatestSubmission)
	}
	if !strings.Contains(status.LatestSubmission.LastError, submittedSHA) || !strings.Contains(status.LatestSubmission.LastError, driftedSHA) {
		t.Fatalf("expected last error to include submitted and drifted shas, got %+v", status.LatestSubmission)
	}
}

func TestRunOnceAllowsQueuedBranchHeadAdvanceWhenSubmittedWithAllowNewerHead(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	featurePath := filepath.Join(t.TempDir(), "feature-head-drift-allowed")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/head-drift-allowed", featurePath)
	writeFileAndCommit(t, featurePath, "head-drift.txt", "one\n", "head drift one")
	submitBranchWithArgs(t, featurePath, "--allow-newer-head")
	submittedSHA := trimNewline(runTestCommand(t, featurePath, "git", "rev-parse", "HEAD"))

	writeFileAndCommit(t, featurePath, "head-drift.txt", "two\n", "head drift two")
	driftedSHA := trimNewline(runTestCommand(t, featurePath, "git", "rev-parse", "HEAD"))
	if driftedSHA == submittedSHA {
		t.Fatalf("expected branch head to move after second commit")
	}

	runOnce(t, repoRoot)

	protectedAfter := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	if protectedAfter != driftedSHA {
		t.Fatalf("expected protected branch to land newer head %q, got %q", driftedSHA, protectedAfter)
	}

	status := readStatusJSON(t, repoRoot)
	if status.LatestSubmission == nil || status.LatestSubmission.Status != "succeeded" {
		t.Fatalf("expected succeeded latest submission, got %+v", status.LatestSubmission)
	}
	if !status.LatestSubmission.AllowNewerHead {
		t.Fatalf("expected allow_newer_head on latest submission, got %+v", status.LatestSubmission)
	}
}

func TestRunOnceSyncsExternalProtectedAdvanceBeforeNextQueuedSubmission(t *testing.T) {
	repoRoot, remoteDir := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	runTestCommand(t, repoRoot, "git", "push", "origin", "main")

	featureOne := filepath.Join(t.TempDir(), "feature-one")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/one", featureOne)
	writeFileAndCommit(t, featureOne, "one.txt", "feature one\n", "feature one")
	submitBranch(t, featureOne)

	featureTwo := filepath.Join(t.TempDir(), "feature-two")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/two", featureTwo)
	writeFileAndCommit(t, featureTwo, "two.txt", "feature two\n", "feature two")
	queuedOriginalSHA := trimNewline(runTestCommand(t, featureTwo, "git", "rev-parse", "HEAD"))

	runOnce(t, repoRoot)
	queuePublish(t, repoRoot)
	runOnce(t, repoRoot)

	upstreamClone := filepath.Join(t.TempDir(), "upstream-clone")
	runTestCommand(t, t.TempDir(), "git", "clone", remoteDir, upstreamClone)
	runTestCommand(t, upstreamClone, "git", "config", "user.name", "Test User")
	runTestCommand(t, upstreamClone, "git", "config", "user.email", "test@example.com")
	runTestCommand(t, upstreamClone, "git", "config", "core.hooksPath", ".git/hooks")
	writeFileAndCommit(t, upstreamClone, "upstream.txt", "upstream\n", "upstream advance")
	upstreamHead := trimNewline(runTestCommand(t, upstreamClone, "git", "rev-parse", "HEAD"))
	runTestCommand(t, upstreamClone, "git", "push", "origin", "main")

	submitBranch(t, featureTwo)

	var runOut bytes.Buffer
	var runErr bytes.Buffer
	if err := runRunOnce([]string{"--repo", repoRoot}, &runOut, &runErr); err != nil {
		t.Fatalf("runRunOnce returned error: %v", err)
	}
	if !strings.Contains(runOut.String(), "Synced main from origin/main and integrated submission") {
		t.Fatalf("expected sync-aware output, got %q", runOut.String())
	}

	logLines := strings.Split(strings.TrimSpace(runTestCommand(t, repoRoot, "git", "log", "--format=%H:%s", "-3")), "\n")
	if len(logLines) != 3 {
		t.Fatalf("expected 3 log lines, got %q", logLines)
	}
	gotOrder := make([]string, 0, 3)
	for _, line := range logLines {
		parts := strings.SplitN(line, ":", 2)
		gotOrder = append(gotOrder, parts[1])
	}
	if strings.Join(gotOrder, "\n") != "feature two\nupstream advance\nfeature one" {
		t.Fatalf("expected feature two atop upstream advance atop feature one, got %v", gotOrder)
	}
	if trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD^")) != upstreamHead {
		t.Fatalf("expected feature two parent to be upstream head %q", upstreamHead)
	}

	status := readStatusJSON(t, repoRoot)
	if status.LatestSubmission == nil || status.LatestSubmission.Status != "succeeded" {
		t.Fatalf("expected succeeded latest submission, got %+v", status.LatestSubmission)
	}
	if status.LatestSubmission.SourceSHA != queuedOriginalSHA {
		t.Fatalf("expected durable source sha to remain original queued sha %q, got %+v", queuedOriginalSHA, status.LatestSubmission)
	}

	events := readRecentEvents(t, repoRoot, 20)
	eventTypes := make([]string, 0, len(events))
	for _, event := range events {
		eventTypes = append(eventTypes, event.EventType)
	}
	sort.Strings(eventTypes)
	if !slices.Contains(eventTypes, "protected.synced_from_upstream") {
		t.Fatalf("expected protected.synced_from_upstream event, got %+v", events)
	}
}

func TestRunOncePreIntegrateTimeoutBlocksWithCheckTimeoutReason(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	cfg, _, err := policy.LoadOrDefault(repoRoot)
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	cfg.Checks.PreIntegrate = []string{"sleep 1"}
	cfg.Checks.CommandTimeout = "100ms"
	if err := policy.SaveFile(repoRoot, cfg); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}
	runTestCommand(t, repoRoot, "git", "add", "mainline.toml")
	runTestCommand(t, repoRoot, "git", "commit", "-m", "configure pre integrate timeout")

	featurePath := filepath.Join(t.TempDir(), "feature-timeout")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/check-timeout", featurePath)
	writeFileAndCommit(t, featurePath, "timeout.txt", "timeout\n", "timeout feature")
	submitBranch(t, featurePath)

	protectedBefore := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))

	var runOut bytes.Buffer
	var runErr bytes.Buffer
	if err := runRunOnce([]string{"--repo", repoRoot}, &runOut, &runErr); err != nil {
		t.Fatalf("runRunOnce returned error: %v", err)
	}
	output := runOut.String()
	if !strings.Contains(output, "check_timeout") {
		t.Fatalf("expected check_timeout output, got %q", output)
	}

	protectedAfter := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	if protectedBefore != protectedAfter {
		t.Fatalf("expected protected branch unchanged, got %q then %q", protectedBefore, protectedAfter)
	}

	report, err := collectStatus(repoRoot, 10)
	if err != nil {
		t.Fatalf("collectStatus: %v", err)
	}
	if report.LatestSubmission == nil {
		t.Fatalf("expected latest submission")
	}
	if report.LatestSubmission.Status != "blocked" {
		t.Fatalf("expected blocked submission, got %+v", report.LatestSubmission)
	}
	if report.LatestSubmission.BlockedReason != "check_timeout" {
		t.Fatalf("expected blocked reason check_timeout, got %+v", report.LatestSubmission)
	}
	if report.LatestSubmission.RetryHint != "rerun-after-fixing-hanging-check" {
		t.Fatalf("expected timeout retry hint, got %+v", report.LatestSubmission)
	}
	if report.LatestSubmission.ProtectedTipSHA != protectedBefore {
		t.Fatalf("expected protected tip %q, got %+v", protectedBefore, report.LatestSubmission)
	}
	if !strings.Contains(report.LatestSubmission.LastError, "check_timeout") {
		t.Fatalf("expected timeout last error, got %+v", report.LatestSubmission)
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

	submitBranchWithArgs(t, repoPath)
}

func submitBranchWithArgs(t *testing.T, repoPath string, extraArgs ...string) {
	t.Helper()
	t.Setenv("MAINLINE_DISABLE_SUBMIT_DRAIN", "1")

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	args := append([]string{"--repo", repoPath}, extraArgs...)
	if err := runSubmit(args, &submitOut, &submitErr); err != nil {
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
