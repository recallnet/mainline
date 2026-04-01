package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/policy"
	"github.com/recallnet/mainline/internal/state"
)

func TestPublishQueuesCurrentProtectedTip(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	t.Setenv("MAINLINE_DISABLE_MUTATION_DRAIN", "1")

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

func TestPublishRejectsDirtyCanonicalRootCheckout(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	if err := os.WriteFile(filepath.Join(repoRoot, "DIRTY.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(DIRTY.txt): %v", err)
	}

	var publishOut bytes.Buffer
	var publishErr bytes.Buffer
	err := runPublish([]string{"--repo", repoRoot, "--json"}, &publishOut, &publishErr)
	if err == nil {
		t.Fatalf("expected publish to fail when canonical root is dirty")
	}
	if !strings.Contains(err.Error(), "protected branch worktree") || !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("expected dirty protected worktree error, got %v", err)
	}
}

func TestPublishDrainsAndPublishesWithoutDaemon(t *testing.T) {
	repoRoot, remoteDir := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	writeFileAndCommit(t, repoRoot, "publish-now.txt", "publish now\n", "main change publish now")
	localHead := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))

	var publishOut bytes.Buffer
	var publishErr bytes.Buffer
	if err := runPublish([]string{"--repo", repoRoot}, &publishOut, &publishErr); err != nil {
		t.Fatalf("runPublish returned error: %v", err)
	}
	if !strings.Contains(publishOut.String(), "Published request") {
		t.Fatalf("expected publish drain output, got %q", publishOut.String())
	}

	remoteHead := trimNewline(runTestCommand(t, remoteDir, "git", "rev-parse", "refs/heads/main"))
	if remoteHead != localHead {
		t.Fatalf("expected remote head %q, got %q", localHead, remoteHead)
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
	t.Setenv("MAINLINE_DISABLE_MUTATION_DRAIN", "1")

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

func TestRunOnceSchedulesRetryForTransientPublishFailure(t *testing.T) {
	repoRoot, remoteDir := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	writeFileAndCommit(t, repoRoot, "retry.txt", "retry\n", "main change retry")
	localHead := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	queuePublish(t, repoRoot)

	restoreBackoff := setPublishRetryBackoffsForTest([]time.Duration{0, 0, 0})
	defer restoreBackoff()

	attempts := 0
	restoreFaults := setAppTestFaultHooks(testFaultHooks{
		before: func(point string) error {
			if point == "publish.push" {
				attempts++
				if attempts <= 2 {
					return errors.New("remote: HTTP 502 bad gateway")
				}
			}
			return nil
		},
	})
	defer restoreFaults()

	result, err := runOneCycle(repoRoot)
	if err != nil {
		t.Fatalf("first runOneCycle returned error: %v", err)
	}
	if !strings.Contains(result, "Scheduled publish retry 1") {
		t.Fatalf("expected retry scheduling output, got %q", result)
	}

	result, err = runOneCycle(repoRoot)
	if err != nil {
		t.Fatalf("second runOneCycle returned error: %v", err)
	}
	if !strings.Contains(result, "Scheduled publish retry 2") {
		t.Fatalf("expected second retry scheduling output, got %q", result)
	}

	result, err = runOneCycle(repoRoot)
	if err != nil {
		t.Fatalf("third runOneCycle returned error: %v", err)
	}
	if !strings.Contains(result, "Published request") {
		t.Fatalf("expected publish success output, got %q", result)
	}

	remoteHead := trimNewline(runTestCommand(t, remoteDir, "git", "rev-parse", "refs/heads/main"))
	if remoteHead != localHead {
		t.Fatalf("expected remote head %q, got %q", localHead, remoteHead)
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
	if requests[0].Status != "succeeded" || requests[0].AttemptCount != 2 {
		t.Fatalf("expected succeeded request after two retries, got %+v", requests[0])
	}
	if requests[0].NextAttemptAt.Valid {
		t.Fatalf("expected retry schedule to clear after success, got %+v", requests[0].NextAttemptAt)
	}
}

func TestRunOnceGivesUpAfterTransientPublishRetryBudget(t *testing.T) {
	repoRoot, remoteDir := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	writeFileAndCommit(t, repoRoot, "fail.txt", "fail\n", "main change fail")
	localHead := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	queuePublish(t, repoRoot)

	restoreBackoff := setPublishRetryBackoffsForTest([]time.Duration{0, 0, 0})
	defer restoreBackoff()
	restoreFaults := setAppTestFaultHooks(testFaultHooks{
		before: func(point string) error {
			if point == "publish.push" {
				return errors.New("remote: HTTP 503 service unavailable")
			}
			return nil
		},
	})
	defer restoreFaults()

	for attempt := 1; attempt <= 3; attempt++ {
		result, err := runOneCycle(repoRoot)
		if err != nil {
			t.Fatalf("retry cycle %d returned error: %v", attempt, err)
		}
		if !strings.Contains(result, "Scheduled publish retry") {
			t.Fatalf("expected scheduled retry output on cycle %d, got %q", attempt, result)
		}
	}

	result, err := runOneCycle(repoRoot)
	if err != nil {
		t.Fatalf("final runOneCycle returned error: %v", err)
	}
	if !strings.Contains(result, "Failed publish request") {
		t.Fatalf("expected terminal failure output, got %q", result)
	}

	remoteHead := trimNewline(runTestCommand(t, remoteDir, "git", "rev-parse", "refs/heads/main"))
	if remoteHead == localHead {
		t.Fatalf("expected remote head to remain behind local head %q", localHead)
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
	if requests[0].Status != "failed" || requests[0].AttemptCount != 3 {
		t.Fatalf("expected failed request after retry exhaustion, got %+v", requests[0])
	}
}

func TestRunOnceReportsDelayedPublishRetryWhenNothingIsReady(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	writeFileAndCommit(t, repoRoot, "delay.txt", "delay\n", "main change delay")
	queuePublish(t, repoRoot)

	restoreFaults := setAppTestFaultHooks(testFaultHooks{
		before: func(point string) error {
			if point == "publish.push" {
				return errors.New("remote: HTTP 500 internal server error")
			}
			return nil
		},
	})
	defer restoreFaults()

	result, err := runOneCycle(repoRoot)
	if err != nil {
		t.Fatalf("first runOneCycle returned error: %v", err)
	}
	if !strings.Contains(result, "Scheduled publish retry 1") {
		t.Fatalf("expected retry scheduling output, got %q", result)
	}

	restoreFaults()
	result, err = runOneCycle(repoRoot)
	if err != nil {
		t.Fatalf("second runOneCycle returned error: %v", err)
	}
	if !strings.Contains(result, "No ready publish requests. Next retry for request") {
		t.Fatalf("expected delayed retry output, got %q", result)
	}
}

func TestDrainRepoUntilSettledSleepsThroughDelayedPublishRetry(t *testing.T) {
	repoRoot, remoteDir := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	t.Setenv("MAINLINE_DISABLE_MUTATION_DRAIN", "1")

	writeFileAndCommit(t, repoRoot, "drain-retry.txt", "drain retry\n", "main change drain retry")
	localHead := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	queuePublish(t, repoRoot)

	restoreBackoff := setPublishRetryBackoffsForTest([]time.Duration{0, 0, 0})
	defer restoreBackoff()

	attempts := 0
	restoreFaults := setAppTestFaultHooks(testFaultHooks{
		before: func(point string) error {
			if point == "publish.push" {
				attempts++
				if attempts == 1 {
					return errors.New("remote: HTTP 502 bad gateway")
				}
			}
			return nil
		},
	})
	defer restoreFaults()

	result, err := drainRepoUntilSettled(repoRoot)
	if err != nil {
		t.Fatalf("drainRepoUntilSettled returned error: %v", err)
	}
	if !strings.Contains(result, "Published request") {
		t.Fatalf("expected publish success output, got %q", result)
	}

	remoteHead := trimNewline(runTestCommand(t, remoteDir, "git", "rev-parse", "refs/heads/main"))
	if remoteHead != localHead {
		t.Fatalf("expected remote head %q, got %q", localHead, remoteHead)
	}
}

func TestIsTransientPublishErrorRecognizesGitHTTPStatusShape(t *testing.T) {
	err := errors.New("fatal: unable to access 'https://github.com/acme/repo.git/': The requested URL returned error: 503")
	if !isTransientPublishError(err) {
		t.Fatalf("expected git HTTP 503 shape to be treated as transient")
	}
}

func TestPublishRespectsHookPolicyBypassingPrePushHook(t *testing.T) {
	repoRoot, remoteDir := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	hookPath := filepath.Join(hooksDirForRepo(t, repoRoot), "pre-push")
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

func TestRunOncePublishesWithInheritedPrePushHook(t *testing.T) {
	repoRoot, remoteDir := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	hookMarker := filepath.Join(t.TempDir(), "pre-push-ran")
	hookPath := filepath.Join(hooksDirForRepo(t, repoRoot), "pre-push")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\necho hook > "+hookMarker+"\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, _, err := policy.LoadOrDefault(repoRoot)
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	if cfg.Repo.HookPolicy != "inherit" {
		t.Fatalf("expected default hook policy inherit, got %+v", cfg.Repo.HookPolicy)
	}

	writeFileAndCommit(t, repoRoot, "inherit.txt", "inherit\n", "main change inherit")
	localHead := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	queuePublish(t, repoRoot)
	runOnce(t, repoRoot)

	if payload, err := os.ReadFile(hookMarker); err != nil || strings.TrimSpace(string(payload)) != "hook" {
		t.Fatalf("expected inherited pre-push hook marker, got %q err=%v", string(payload), err)
	}

	remoteHead := trimNewline(runTestCommand(t, remoteDir, "git", "rev-parse", "refs/heads/main"))
	if remoteHead != localHead {
		t.Fatalf("expected remote head %q, got %q", localHead, remoteHead)
	}
}

func TestPublishHookFailureThenManualRetrySucceeds(t *testing.T) {
	repoRoot, remoteDir := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	gateFlag := filepath.Join(t.TempDir(), "allow-push")
	hookPath := filepath.Join(hooksDirForRepo(t, repoRoot), "pre-push")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\nif [ ! -f "+gateFlag+" ]; then\n  echo gate failed >&2\n  exit 9\nfi\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, _, err := policy.LoadOrDefault(repoRoot)
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	if cfg.Repo.HookPolicy != "inherit" {
		t.Fatalf("expected default hook policy inherit, got %+v", cfg.Repo.HookPolicy)
	}

	writeFileAndCommit(t, repoRoot, "retry-hook.txt", "retry hook\n", "main change retry hook")
	localHead := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	queuePublish(t, repoRoot)

	result, err := runOneCycle(repoRoot)
	if err != nil {
		t.Fatalf("first runOneCycle returned error: %v", err)
	}
	if !strings.Contains(result, "Failed publish request") {
		t.Fatalf("expected failed publish output, got %q", result)
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
	if len(requests) != 1 || requests[0].Status != "failed" {
		t.Fatalf("expected failed publish request, got %+v", requests)
	}

	if err := os.WriteFile(gateFlag, []byte("ok\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(gateFlag): %v", err)
	}

	var retryOut bytes.Buffer
	var retryErr bytes.Buffer
	if err := runRetry([]string{"--repo", repoRoot, "--publish", strconv.FormatInt(requests[0].ID, 10)}, &retryOut, &retryErr); err != nil {
		t.Fatalf("runRetry returned error: %v", err)
	}

	result, err = runOneCycle(repoRoot)
	if err != nil {
		t.Fatalf("second runOneCycle returned error: %v", err)
	}
	if !strings.Contains(result, "Published request") {
		t.Fatalf("expected publish success output after retry, got %q", result)
	}

	remoteHead := trimNewline(runTestCommand(t, remoteDir, "git", "rev-parse", "refs/heads/main"))
	if remoteHead != localHead {
		t.Fatalf("expected remote head %q, got %q", localHead, remoteHead)
	}
	requests, err = store.ListPublishRequests(context.Background(), repoRecord.ID)
	if err != nil {
		t.Fatalf("ListPublishRequests: %v", err)
	}
	if requests[0].Status != "succeeded" {
		t.Fatalf("expected retried publish request to succeed, got %+v", requests[0])
	}
}

func TestRunOnceCanPreemptInFlightPublishForNewerTarget(t *testing.T) {
	repoRoot, remoteDir := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	hookPIDPath := filepath.Join(t.TempDir(), "pre-push.pid")
	hookPath := filepath.Join(hooksDirForRepo(t, repoRoot), "pre-push")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\necho $$ > "+hookPIDPath+"\nsleep 5\nexit 0\n"), 0o755); err != nil {
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

	hookPID := waitForHookPID(t, hookPIDPath)

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
	waitForProcessExit(t, hookPID)

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

func waitForHookPID(t *testing.T, path string) int {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		payload, err := os.ReadFile(path)
		if err == nil {
			pid, convErr := strconv.Atoi(strings.TrimSpace(string(payload)))
			if convErr != nil {
				t.Fatalf("Atoi: %v", convErr)
			}
			return pid
		}
		if !os.IsNotExist(err) {
			t.Fatalf("ReadFile: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for pre-push hook pid file %s", path)
	return 0
}

func waitForProcessExit(t *testing.T, pid int) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if err != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for process %d to exit", pid)
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

func setPublishRetryBackoffsForTest(backoffs []time.Duration) func() {
	previous := publishRetryBackoffs
	publishRetryBackoffs = append([]time.Duration(nil), backoffs...)
	return func() {
		publishRetryBackoffs = previous
	}
}
