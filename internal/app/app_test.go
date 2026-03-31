package app

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
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

func TestMQHelpUsesMQIdentity(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := runCLIWithName("mq", []string{"--help"}, &stdout, &stderr); err != nil {
		t.Fatalf("runCLIWithName returned error: %v", err)
	}

	if !strings.Contains(stdout.String(), "mq coordinates local protected-branch integrations and publishes.") {
		t.Fatalf("expected mq help identity, got %q", stdout.String())
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

func TestVersionCommandsReportBuildMetadata(t *testing.T) {
	originalVersion, originalCommit, originalDate := Version, Commit, Date
	Version, Commit, Date = "v1.2.3", "abc1234", "2026-03-31T00:00:00Z"
	t.Cleanup(func() {
		Version, Commit, Date = originalVersion, originalCommit, originalDate
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLIWithName("mq", []string{"version"}, &stdout, &stderr); err != nil {
		t.Fatalf("runCLIWithName version returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "mq v1.2.3 commit=abc1234 date=2026-03-31T00:00:00Z") {
		t.Fatalf("expected version output, got %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := runDaemonWithName("mainlined", []string{"--version"}, &stdout, &stderr); err != nil {
		t.Fatalf("runDaemonWithName version returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "mainlined v1.2.3 commit=abc1234 date=2026-03-31T00:00:00Z") {
		t.Fatalf("expected daemon version output, got %q", stdout.String())
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
	if !strings.Contains(output, "retry cancel publish") {
		t.Fatalf("expected completion script to include retry and cancel, got %q", output)
	}
	if !strings.Contains(output, "publish logs watch events doctor completion version config") {
		t.Fatalf("expected completion script to include config surface, got %q", output)
	}
	if strings.Contains(output, "run-once|publish|doctor") {
		t.Fatalf("expected split completion cases for real command flags, got %q", output)
	}
	if strings.Contains(output, "run-once|publish)\n      COMPREPLY=( $(compgen -W \"--repo --json\" -- \"$cur\") )") {
		t.Fatalf("expected bash completion to avoid generic unsupported json flag suggestions, got %q", output)
	}

	stdout.Reset()
	stderr.Reset()
	if err := runCLI([]string{"completion", "fish"}, &stdout, &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	output = stdout.String()
	if !strings.Contains(output, "__fish_seen_subcommand_from logs events\" -l json") {
		t.Fatalf("expected fish completion to include logs/events --json, got %q", output)
	}
	if !strings.Contains(output, "__fish_seen_subcommand_from watch\" -l interval") {
		t.Fatalf("expected fish completion to include watch flags, got %q", output)
	}
	if !strings.Contains(output, "__fish_seen_subcommand_from config edit\" -l editor") {
		t.Fatalf("expected fish completion to include config edit flags, got %q", output)
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

func TestEventsJSONListsRecentEventsChronologically(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	queuePublish(t, repoRoot)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLI([]string{"events", "--repo", repoRoot, "--json", "--limit", "3"}, &stdout, &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) == 0 {
		t.Fatalf("expected event output, got none")
	}
	var lastID int64
	foundPublishRequested := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event state.EventRecord
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if event.ID <= lastID {
			t.Fatalf("expected chronological event order, got ids %d then %d", lastID, event.ID)
		}
		lastID = event.ID
		if event.EventType == "publish.requested" {
			foundPublishRequested = true
		}
	}
	if !foundPublishRequested {
		t.Fatalf("expected publish.requested event in stream")
	}
}

func TestEventsFollowStreamsNewEvent(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	var output lockedBuffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- runEventStream(ctx, eventOptions{
			repoPath:     repoRoot,
			limit:        1,
			follow:       true,
			pollInterval: 20 * time.Millisecond,
		}, &output)
	}()

	time.Sleep(50 * time.Millisecond)
	queuePublish(t, repoRoot)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(output.String(), "publish.requested") {
			cancel()
			if err := <-done; err != nil {
				t.Fatalf("runEventStream returned error: %v", err)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	t.Fatalf("timed out waiting for publish.requested in streamed events, got %q", output.String())
}

func TestLogsMatchesEventOutput(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	queuePublish(t, repoRoot)

	var eventStdout bytes.Buffer
	var logsStdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLI([]string{"events", "--repo", repoRoot, "--json", "--limit", "3"}, &eventStdout, &stderr); err != nil {
		t.Fatalf("events runCLI returned error: %v", err)
	}
	stderr.Reset()
	if err := runCLI([]string{"logs", "--repo", repoRoot, "--json", "--limit", "3"}, &logsStdout, &stderr); err != nil {
		t.Fatalf("logs runCLI returned error: %v", err)
	}
	if logsStdout.String() != eventStdout.String() {
		t.Fatalf("expected logs output to match events output\nlogs:\n%s\nevents:\n%s", logsStdout.String(), eventStdout.String())
	}
}

func TestLogsHelpUsesLogsCommandName(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runCLI([]string{"logs", "--help"}, &stdout, &stderr)
	if err == nil {
		t.Fatalf("expected help error for logs command")
	}
	output := stderr.String()
	if !strings.Contains(output, "Usage of mainline logs:") {
		t.Fatalf("expected logs help usage, got %q", output)
	}
	if strings.Contains(output, "Usage of mainline events:") {
		t.Fatalf("expected logs help to avoid events alias wording, got %q", output)
	}
}

func TestWatchJSONEmitsSnapshots(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	queuePublish(t, repoRoot)

	var stdout bytes.Buffer
	if err := runWatchLoop(context.Background(), watchOptions{
		repoPath:   repoRoot,
		interval:   10 * time.Millisecond,
		eventLimit: 2,
		maxCycles:  2,
		asJSON:     true,
	}, &stdout); err != nil {
		t.Fatalf("runWatchLoop returned error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 json snapshots, got %d: %q", len(lines), stdout.String())
	}
	for _, line := range lines {
		var frame watchFrame
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if frame.Status.Counts.QueuedPublishes != 1 {
			t.Fatalf("expected queued publish in watch snapshot, got %+v", frame.Status.Counts)
		}
		if len(frame.Status.RecentEvents) == 0 {
			t.Fatalf("expected recent events in watch snapshot")
		}
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

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}
