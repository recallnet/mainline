package app

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/state"
)

func TestCLIHelp(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := runCLI([]string{"--help"}, &stdout, &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	if !strings.Contains(stdout.String(), "Usage:") {
		t.Fatalf("expected help output, got %q", stdout.String())
	}
}

func TestDaemonHelp(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := runDaemon([]string{"--help"}, &stdout, &stderr); err != nil {
		t.Fatalf("runDaemon returned error: %v", err)
	}

	if !strings.Contains(stdout.String(), "mainlined runs the background worker loop") {
		t.Fatalf("expected daemon help output, got %q", stdout.String())
	}
}

func TestDaemonProcessesIntegrationAndPublishWork(t *testing.T) {
	repoRoot, remoteDir := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	updatePublishMode(t, repoRoot, "auto")

	featurePath := filepath.Join(t.TempDir(), "feature-daemon")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/daemon", featurePath)
	writeFileAndCommit(t, featurePath, "daemon.txt", "daemon\n", "feature daemon")
	submitBranch(t, featurePath)

	var stdout bytes.Buffer
	opts := daemonOptions{
		repoPath:  repoRoot,
		interval:  time.Millisecond,
		maxCycles: 2,
	}
	if err := runDaemonLoop(context.Background(), opts, &stdout); err != nil {
		t.Fatalf("runDaemonLoop returned error: %v", err)
	}

	localHead := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
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
	submissions, err := store.ListIntegrationSubmissions(context.Background(), repoRecord.ID)
	if err != nil {
		t.Fatalf("ListIntegrationSubmissions: %v", err)
	}
	if len(submissions) != 1 || submissions[0].Status != "succeeded" {
		t.Fatalf("expected succeeded submission, got %+v", submissions)
	}
	requests, err := store.ListPublishRequests(context.Background(), repoRecord.ID)
	if err != nil {
		t.Fatalf("ListPublishRequests: %v", err)
	}
	if len(requests) != 1 || requests[0].Status != "succeeded" {
		t.Fatalf("expected succeeded publish request, got %+v", requests)
	}
}

func TestDaemonIdleExitEmitsJSONLog(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	var stdout bytes.Buffer
	opts := daemonOptions{
		repoPath: repoRoot,
		interval: time.Millisecond,
		jsonLogs: true,
		idleExit: true,
	}
	if err := runDaemonLoop(context.Background(), opts, &stdout); err != nil {
		t.Fatalf("runDaemonLoop returned error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 json log lines, got %q", stdout.String())
	}

	var last daemonLog
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if last.Event != "daemon.idle_exit" {
		t.Fatalf("expected daemon.idle_exit event, got %+v", last)
	}
}

func TestDaemonTreatsHeldLockAsBusyNotFatal(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	layout, err := git.DiscoverRepositoryLayout(repoRoot)
	if err != nil {
		t.Fatalf("DiscoverRepositoryLayout: %v", err)
	}
	lockManager := state.NewLockManager(layout.RepositoryRoot, layout.GitDir)
	lease, err := lockManager.Acquire(state.PublishLock, "test")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lease.Release()

	var stdout bytes.Buffer
	opts := daemonOptions{
		repoPath:  repoRoot,
		interval:  time.Millisecond,
		maxCycles: 1,
	}
	if err := runDaemonLoop(context.Background(), opts, &stdout); err != nil {
		t.Fatalf("runDaemonLoop returned error: %v", err)
	}

	if !strings.Contains(stdout.String(), "Publish worker busy.") {
		t.Fatalf("expected busy log output, got %q", stdout.String())
	}
}

func TestCLIAcceptsSubcommandFlagsForPlannedCommands(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := runCLI([]string{"completion", "bash"}, &stdout, &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "complete -F _mainline_completions mainline") {
		t.Fatalf("expected completion script output, got %q", output)
	}
}

func TestStatusJSONReportsQueuedWork(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	featurePath := filepath.Join(t.TempDir(), "feature-status")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/status", featurePath)
	writeFileAndCommit(t, featurePath, "status.txt", "status\n", "feature status")
	submitBranch(t, featurePath)
	queuePublish(t, repoRoot)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLI([]string{"status", "--repo", repoRoot, "--json", "--events", "2"}, &stdout, &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	var status statusResult
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if status.Counts.QueuedSubmissions != 1 {
		t.Fatalf("expected 1 queued submission, got %+v", status.Counts)
	}
	if status.Counts.QueuedPublishes != 1 {
		t.Fatalf("expected 1 queued publish, got %+v", status.Counts)
	}
	if status.LatestSubmission == nil || status.LatestSubmission.BranchName != "feature/status" {
		t.Fatalf("expected latest submission for feature/status, got %+v", status.LatestSubmission)
	}
	if status.LatestPublish == nil || status.LatestPublish.Status != "queued" {
		t.Fatalf("expected latest queued publish, got %+v", status.LatestPublish)
	}
	if len(status.RecentEvents) == 0 {
		t.Fatalf("expected recent events, got none")
	}
}

func TestStatusHumanOutputIncludesRecentSummary(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	queuePublish(t, repoRoot)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLI([]string{"status", "--repo", repoRoot}, &stdout, &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "Queue: submissions queued=0") {
		t.Fatalf("expected queue summary, got %q", output)
	}
	if !strings.Contains(output, "Latest publish: #") {
		t.Fatalf("expected latest publish summary, got %q", output)
	}
	if !strings.Contains(output, "Recent events:") {
		t.Fatalf("expected recent events section, got %q", output)
	}
}

func TestCLIRepoSubcommandsRemainReachable(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	repoRoot, _ := createTestRepo(t)

	if err := runCLI([]string{"repo", "init", "--repo", repoRoot}, &stdout, &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	if !strings.Contains(stdout.String(), "Initialized ") {
		t.Fatalf("expected repo init output, got %q", stdout.String())
	}
}
