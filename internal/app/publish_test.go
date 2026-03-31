package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/policy"
	"github.com/recallnet/mainline/internal/state"
)

func TestPublishQueuesCurrentProtectedTip(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	protectedHead := runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD")

	var publishOut bytes.Buffer
	var publishErr bytes.Buffer
	if err := runPublish([]string{"--repo", repoRoot}, &publishOut, &publishErr); err != nil {
		t.Fatalf("runPublish returned error: %v", err)
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
	requests, err := store.ListPublishRequests(context.Background(), repoRecord.ID)
	if err != nil {
		t.Fatalf("ListPublishRequests: %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("expected 1 publish request, got %d", len(requests))
	}
	if requests[0].TargetSHA != protectedHead[:len(protectedHead)-1] {
		t.Fatalf("expected target sha %q, got %q", protectedHead, requests[0].TargetSHA)
	}
	if requests[0].Status != "queued" {
		t.Fatalf("expected queued publish request, got %q", requests[0].Status)
	}
}

func TestRunOncePublishesLatestQueuedTipAndSupersedesOlderRequests(t *testing.T) {
	repoRoot, remoteDir := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	writeFileAndCommit(t, repoRoot, "one.txt", "one\n", "main change one")
	queuePublish(t, repoRoot)

	writeFileAndCommit(t, repoRoot, "two.txt", "two\n", "main change two")
	currentHead := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	queuePublish(t, repoRoot)

	var runOut bytes.Buffer
	var runErr bytes.Buffer
	if err := runRunOnce([]string{"--repo", repoRoot}, &runOut, &runErr); err != nil {
		t.Fatalf("runRunOnce returned error: %v", err)
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
	requests, err := store.ListPublishRequests(context.Background(), repoRecord.ID)
	if err != nil {
		t.Fatalf("ListPublishRequests: %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("expected 2 publish requests, got %d", len(requests))
	}
	if requests[0].Status != "superseded" {
		t.Fatalf("expected first request superseded, got %q", requests[0].Status)
	}
	if !requests[0].SupersededBy.Valid || requests[0].SupersededBy.Int64 != requests[1].ID {
		t.Fatalf("expected first request superseded by second, got %+v", requests[0].SupersededBy)
	}
	if requests[1].Status != "succeeded" {
		t.Fatalf("expected second request succeeded, got %q", requests[1].Status)
	}

	remoteHead := trimNewline(runTestCommand(t, remoteDir, "git", "rev-parse", "refs/heads/main"))
	if remoteHead != currentHead {
		t.Fatalf("expected remote head %q, got %q", currentHead, remoteHead)
	}
}

func TestRunOnceLinksSupersededStalePublishRequestToReplacement(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	queuePublish(t, repoRoot)
	writeFileAndCommit(t, repoRoot, "new.txt", "new\n", "advance main")

	var runOut bytes.Buffer
	var runErr bytes.Buffer
	if err := runRunOnce([]string{"--repo", repoRoot}, &runOut, &runErr); err != nil {
		t.Fatalf("runRunOnce returned error: %v", err)
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
	requests, err := store.ListPublishRequests(context.Background(), repoRecord.ID)
	if err != nil {
		t.Fatalf("ListPublishRequests: %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("expected 2 publish requests, got %d", len(requests))
	}
	if requests[0].Status != "superseded" {
		t.Fatalf("expected first request superseded, got %q", requests[0].Status)
	}
	if !requests[0].SupersededBy.Valid || requests[0].SupersededBy.Int64 != requests[1].ID {
		t.Fatalf("expected superseded_by=%d, got %+v", requests[1].ID, requests[0].SupersededBy)
	}
	if requests[1].Status != "queued" {
		t.Fatalf("expected replacement request queued, got %q", requests[1].Status)
	}
}

func TestRunOnceWithNoQueueReportsNoPublishWork(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	var runOut bytes.Buffer
	var runErr bytes.Buffer
	if err := runRunOnce([]string{"--repo", repoRoot}, &runOut, &runErr); err != nil {
		t.Fatalf("runRunOnce returned error: %v", err)
	}
	if trimNewline(runOut.String()) != "No queued publish requests." {
		t.Fatalf("expected no publish work output, got %q", runOut.String())
	}
}

func queuePublish(t *testing.T, repoRoot string) {
	t.Helper()

	var publishOut bytes.Buffer
	var publishErr bytes.Buffer
	if err := runPublish([]string{"--repo", repoRoot}, &publishOut, &publishErr); err != nil {
		t.Fatalf("runPublish returned error: %v", err)
	}
}

func trimNewline(s string) string {
	if len(s) > 0 && s[len(s)-1] == '\n' {
		return s[:len(s)-1]
	}
	return s
}

func TestRunOnceAutoPublishQueuesAndPublishesOnSecondCycle(t *testing.T) {
	repoRoot, remoteDir := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	updatePublishMode(t, repoRoot, "auto")

	featureOne := filepath.Join(t.TempDir(), "feature-one")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/one", featureOne)
	writeFileAndCommit(t, featureOne, "one.txt", "feature one\n", "feature one")
	submitBranch(t, featureOne)

	runOnce(t, repoRoot)
	runOnce(t, repoRoot)

	localHead := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	remoteHead := trimNewline(runTestCommand(t, remoteDir, "git", "rev-parse", "refs/heads/main"))
	if remoteHead != localHead {
		t.Fatalf("expected remote head %q, got %q", localHead, remoteHead)
	}
}

func TestRunOncePrePublishChecksFailBeforePush(t *testing.T) {
	repoRoot, remoteDir := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	cfg, _, err := policy.LoadOrDefault(repoRoot)
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	cfg.Checks.PrePublish = []string{"exit 9"}
	if err := policy.SaveFile(repoRoot, cfg); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}
	runTestCommand(t, repoRoot, "git", "add", "mainline.toml")
	runTestCommand(t, repoRoot, "git", "commit", "-m", "configure pre publish check")

	writeFileAndCommit(t, repoRoot, "one.txt", "one\n", "main change one")
	localHead := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	queuePublish(t, repoRoot)

	var runOut bytes.Buffer
	var runErr bytes.Buffer
	if err := runRunOnce([]string{"--repo", repoRoot}, &runOut, &runErr); err != nil {
		t.Fatalf("runRunOnce returned error: %v", err)
	}
	if !strings.Contains(runOut.String(), "pre-publish checks failed") {
		t.Fatalf("expected pre-publish check failure output, got %q", runOut.String())
	}

	remoteHead := trimNewline(runTestCommand(t, remoteDir, "git", "rev-parse", "refs/heads/main"))
	if remoteHead == localHead {
		t.Fatalf("expected remote head to remain behind local head %q", localHead)
	}
}

func TestPublishRespectsHookPolicyBypassingPrePushHook(t *testing.T) {
	repoRoot, remoteDir := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	hookPath := filepath.Join(repoRoot, ".git", "hooks", "pre-push")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\nexit 7\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, _, err := policy.LoadOrDefault(repoRoot)
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	cfg.Repo.HookPolicy = "replace-with-mainline-checks"
	if err := policy.SaveFile(repoRoot, cfg); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}
	runTestCommand(t, repoRoot, "git", "add", "mainline.toml")
	runTestCommand(t, repoRoot, "git", "commit", "-m", "configure hook policy")

	writeFileAndCommit(t, repoRoot, "hook.txt", "hook\n", "main change")
	localHead := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	queuePublish(t, repoRoot)
	runOnce(t, repoRoot)

	remoteHead := trimNewline(runTestCommand(t, remoteDir, "git", "rev-parse", "refs/heads/main"))
	if remoteHead != localHead {
		t.Fatalf("expected hook-bypassed publish to update remote to %q, got %q", localHead, remoteHead)
	}
}

func TestRunOnceCanPreemptInFlightPublishForNewerTarget(t *testing.T) {
	repoRoot, remoteDir := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	hookPath := filepath.Join(repoRoot, ".git", "hooks", "pre-push")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\nsleep 2\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, _, err := policy.LoadOrDefault(repoRoot)
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	cfg.Publish.InterruptInflight = true
	cfg.Repo.HookPolicy = "inherit"
	if err := policy.SaveFile(repoRoot, cfg); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}
	runTestCommand(t, repoRoot, "git", "add", "mainline.toml")
	runTestCommand(t, repoRoot, "git", "commit", "-m", "enable publish preemption")

	writeFileAndCommit(t, repoRoot, "one.txt", "one\n", "main change one")
	queuePublish(t, repoRoot)

	resultCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := runOneCycle(repoRoot)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	time.Sleep(200 * time.Millisecond)

	writeFileAndCommit(t, repoRoot, "two.txt", "two\n", "main change two")
	latestHead := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	queuePublish(t, repoRoot)

	preemptResult, err := runOneCycle(repoRoot)
	if err != nil {
		t.Fatalf("second runOneCycle returned error: %v", err)
	}
	if !strings.Contains(preemptResult, "Requested publish preemption") {
		t.Fatalf("expected preemption request result, got %q", preemptResult)
	}

	select {
	case err := <-errCh:
		t.Fatalf("first runOneCycle returned error: %v", err)
	case result := <-resultCh:
		if !strings.Contains(result, "Preempted publish request") {
			t.Fatalf("expected interrupted publish result, got %q", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for interrupted publish cycle")
	}

	if _, err := runOneCycle(repoRoot); err != nil {
		t.Fatalf("final runOneCycle returned error: %v", err)
	}

	remoteHead := trimNewline(runTestCommand(t, remoteDir, "git", "rev-parse", "refs/heads/main"))
	if remoteHead != latestHead {
		t.Fatalf("expected remote head %q, got %q", latestHead, remoteHead)
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
	requests, err := store.ListPublishRequests(context.Background(), repoRecord.ID)
	if err != nil {
		t.Fatalf("ListPublishRequests: %v", err)
	}
	if len(requests) < 2 {
		t.Fatalf("expected at least 2 publish requests, got %d", len(requests))
	}
	if requests[0].Status != "superseded" {
		t.Fatalf("expected first request superseded, got %q", requests[0].Status)
	}
}

func updatePublishMode(t *testing.T, repoRoot string, mode string) {
	t.Helper()

	cfg, _, err := policy.LoadOrDefault(repoRoot)
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	cfg.Publish.Mode = mode
	if err := policy.SaveFile(repoRoot, cfg); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}
	runTestCommand(t, repoRoot, "git", "add", "mainline.toml")
	runTestCommand(t, repoRoot, "git", "commit", "-m", "update publish mode")
}
