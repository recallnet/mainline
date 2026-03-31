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
