package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/state"
)

func TestRunOnceRecoversRunningSubmissionAfterWorkerCrash(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	featurePath := filepath.Join(t.TempDir(), "feature-restart")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/restart", featurePath)
	writeFileAndCommit(t, featurePath, "restart.txt", "restart\n", "restart feature")
	submitBranch(t, featurePath)

	protectedBefore := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	cmd, _ := startWorkerHelper(t, "integration-crash", repoRoot)
	waitForHelperReady(t, cmd)
	killHelper(t, cmd)

	store, repoRecord := openRepoStore(t, repoRoot)
	submissions, err := store.ListIntegrationSubmissions(context.Background(), repoRecord.ID)
	if err != nil {
		t.Fatalf("ListIntegrationSubmissions: %v", err)
	}
	if len(submissions) != 1 || submissions[0].Status != "running" {
		t.Fatalf("expected crashed submission to remain running before recovery, got %+v", submissions)
	}

	runOnce(t, repoRoot)

	protectedAfter := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	if protectedAfter == protectedBefore {
		t.Fatalf("expected recovered run to advance protected head, still %q", protectedAfter)
	}

	submissions, err = store.ListIntegrationSubmissions(context.Background(), repoRecord.ID)
	if err != nil {
		t.Fatalf("ListIntegrationSubmissions: %v", err)
	}
	if submissions[0].Status != "succeeded" {
		t.Fatalf("expected recovered submission to succeed, got %+v", submissions[0])
	}
	assertStoreEventPresent(t, store, repoRecord.ID, "integration.recovered")
	assertProtectedWorktreeClean(t, repoRoot)
}

func TestRunOnceRecoversRunningPublishAfterWorkerCrash(t *testing.T) {
	repoRoot, remoteDir := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	writeFileAndCommit(t, repoRoot, "publish.txt", "publish\n", "publish candidate")
	queuePublish(t, repoRoot)

	cmd, _ := startWorkerHelper(t, "publish-crash", repoRoot)
	waitForHelperReady(t, cmd)
	killHelper(t, cmd)

	store, repoRecord := openRepoStore(t, repoRoot)
	requests, err := store.ListPublishRequests(context.Background(), repoRecord.ID)
	if err != nil {
		t.Fatalf("ListPublishRequests: %v", err)
	}
	if len(requests) != 1 || requests[0].Status != "running" {
		t.Fatalf("expected crashed publish to remain running before recovery, got %+v", requests)
	}

	runOnce(t, repoRoot)

	requests, err = store.ListPublishRequests(context.Background(), repoRecord.ID)
	if err != nil {
		t.Fatalf("ListPublishRequests: %v", err)
	}
	if requests[0].Status != "succeeded" {
		t.Fatalf("expected recovered publish to succeed, got %+v", requests[0])
	}
	assertStoreEventPresent(t, store, repoRecord.ID, "publish.recovered")

	localHead := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	remoteHead := trimNewline(runTestCommand(t, remoteDir, "git", "rev-parse", "refs/heads/main"))
	if remoteHead != localHead {
		t.Fatalf("expected recovered publish to converge remote to %q, got %q", localHead, remoteHead)
	}
}

func TestRunOnceRecoversRunningSubmissionAfterFastForwardBoundaryCrash(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	featurePath := filepath.Join(t.TempDir(), "feature-fast-forward-restart")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/fast-forward-restart", featurePath)
	writeFileAndCommit(t, featurePath, "ff.txt", "ff\n", "fast-forward restart feature")
	submitBranch(t, featurePath)

	protectedBefore := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	cmd, _ := startWorkerHelper(t, "integration-fast-forward-crash", repoRoot)
	waitForHelperReady(t, cmd)
	killHelper(t, cmd)

	store, repoRecord := openRepoStore(t, repoRoot)
	submissions, err := store.ListIntegrationSubmissions(context.Background(), repoRecord.ID)
	if err != nil {
		t.Fatalf("ListIntegrationSubmissions: %v", err)
	}
	if len(submissions) != 1 || submissions[0].Status != "running" {
		t.Fatalf("expected crashed submission to remain running before recovery, got %+v", submissions)
	}

	runOnce(t, repoRoot)

	protectedAfter := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	if protectedAfter == protectedBefore {
		t.Fatalf("expected recovered run to advance protected head, still %q", protectedAfter)
	}

	submissions, err = store.ListIntegrationSubmissions(context.Background(), repoRecord.ID)
	if err != nil {
		t.Fatalf("ListIntegrationSubmissions: %v", err)
	}
	if submissions[0].Status != "succeeded" {
		t.Fatalf("expected recovered submission to succeed, got %+v", submissions[0])
	}
	assertStoreEventPresent(t, store, repoRecord.ID, "integration.recovered")
	assertProtectedWorktreeClean(t, repoRoot)
}

func TestRunOnceReportsBusyWhileAnotherWorkerHoldsIntegrationLock(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	featurePath := filepath.Join(t.TempDir(), "feature-held-lock")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/held-lock", featurePath)
	writeFileAndCommit(t, featurePath, "lock.txt", "lock\n", "held lock feature")
	submitBranch(t, featurePath)

	cmd, _ := startWorkerHelper(t, "hold-integration-lock", repoRoot)
	waitForHelperReady(t, cmd)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runRunOnce([]string{"--repo", repoRoot}, &stdout, &stderr); err != nil {
		t.Fatalf("runRunOnce returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Integration worker busy.") {
		t.Fatalf("expected busy output, got %q", stdout.String())
	}

	killHelper(t, cmd)
	runOnce(t, repoRoot)

	store, repoRecord := openRepoStore(t, repoRoot)
	submissions, err := store.ListIntegrationSubmissions(context.Background(), repoRecord.ID)
	if err != nil {
		t.Fatalf("ListIntegrationSubmissions: %v", err)
	}
	if submissions[0].Status != "succeeded" {
		t.Fatalf("expected submission to succeed after held lock release, got %+v", submissions[0])
	}
}

func TestDoctorReportsStaleLockMetadata(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	layout, err := git.DiscoverRepositoryLayout(repoRoot)
	if err != nil {
		t.Fatalf("DiscoverRepositoryLayout: %v", err)
	}
	lockManager := state.NewLockManager(layout.RepositoryRoot, layout.GitDir)
	lockPath := filepath.Join(layout.GitDir, "mainline", "locks", state.IntegrationLock+".lock.json")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	payload, err := json.Marshal(state.LeaseMetadata{
		Domain:    state.IntegrationLock,
		RepoRoot:  layout.RepositoryRoot,
		Owner:     "crashed-worker",
		PID:       999999,
		CreatedAt: time.Now().Add(-2 * time.Hour).UTC(),
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(lockPath, payload, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runDoctor([]string{"--repo", repoRoot}, &stdout, &stderr); err != nil {
		t.Fatalf("runDoctor returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Stale locks: 1") {
		t.Fatalf("expected stale lock count, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "integration:crashed-worker") {
		t.Fatalf("expected stale lock detail, got %q", stdout.String())
	}

	stale, err := lockManager.InspectStale(time.Hour)
	if err != nil {
		t.Fatalf("InspectStale: %v", err)
	}
	if len(stale) != 1 {
		t.Fatalf("expected one stale metadata record, got %+v", stale)
	}
}

func TestWorkerCrashHelper(t *testing.T) {
	mode := os.Getenv("MAINLINE_HELPER_MODE")
	if mode == "" {
		return
	}

	repoRoot := os.Getenv("MAINLINE_HELPER_REPO")
	readyPath := os.Getenv("MAINLINE_HELPER_READY")
	if repoRoot == "" || readyPath == "" {
		t.Fatalf("helper missing repo or ready path")
	}

	switch mode {
	case "integration-crash":
		restore := setAppTestFaultHooks(testFaultHooks{
			before: func(point string) error {
				if point == "integration.rebase" {
					markHelperReady(t, readyPath)
					time.Sleep(30 * time.Second)
				}
				return nil
			},
		})
		defer restore()
		_, _ = runOneCycle(repoRoot)
	case "integration-fast-forward-crash":
		restore := setAppTestFaultHooks(testFaultHooks{
			before: func(point string) error {
				if point == "integration.fast_forward" {
					markHelperReady(t, readyPath)
					time.Sleep(30 * time.Second)
				}
				return nil
			},
		})
		defer restore()
		_, _ = runOneCycle(repoRoot)
	case "publish-crash":
		restore := setAppTestFaultHooks(testFaultHooks{
			before: func(point string) error {
				if point == "publish.push" {
					markHelperReady(t, readyPath)
					time.Sleep(30 * time.Second)
				}
				return nil
			},
		})
		defer restore()
		_, _ = runOneCycle(repoRoot)
	case "hold-integration-lock":
		layout, err := git.DiscoverRepositoryLayout(repoRoot)
		if err != nil {
			t.Fatalf("DiscoverRepositoryLayout: %v", err)
		}
		lockManager := state.NewLockManager(layout.RepositoryRoot, layout.GitDir)
		lease, err := lockManager.Acquire(state.IntegrationLock, "helper-hold")
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		defer lease.Release()
		markHelperReady(t, readyPath)
		time.Sleep(30 * time.Second)
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
}

func startWorkerHelper(t *testing.T, mode string, repoRoot string) (*exec.Cmd, string) {
	t.Helper()

	readyPath := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], "-test.run=TestWorkerCrashHelper")
	cmd.Env = append(os.Environ(),
		"MAINLINE_HELPER_MODE="+mode,
		"MAINLINE_HELPER_REPO="+repoRoot,
		"MAINLINE_HELPER_READY="+readyPath,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start helper: %v", err)
	}
	return cmd, readyPath
}

func waitForHelperReady(t *testing.T, cmd *exec.Cmd) {
	t.Helper()

	readyPath := ""
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, "MAINLINE_HELPER_READY=") {
			readyPath = strings.TrimPrefix(entry, "MAINLINE_HELPER_READY=")
			break
		}
	}
	if readyPath == "" {
		t.Fatalf("helper ready path missing")
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(readyPath); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for helper readiness")
}

func killHelper(t *testing.T, cmd *exec.Cmd) {
	t.Helper()

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("Kill helper: %v", err)
	}
	_ = cmd.Wait()
}

func markHelperReady(t *testing.T, path string) {
	t.Helper()

	if err := os.WriteFile(path, []byte("ready\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func assertStoreEventPresent(t *testing.T, store state.Store, repoID int64, eventType string) {
	t.Helper()

	events, err := store.ListEvents(context.Background(), repoID, 50)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	for _, event := range events {
		if event.EventType == eventType {
			return
		}
	}
	t.Fatalf("expected event %q in %+v", eventType, events)
}
