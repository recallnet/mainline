package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

type stressAgentPlan struct {
	Name     string
	Branch   string
	Worktree string
	Delay    time.Duration
	Mode     string
}

type stressReport struct {
	AgentCount           int      `json:"agent_count"`
	SucceededSubmissions int      `json:"succeeded_submissions"`
	BlockedSubmissions   int      `json:"blocked_submissions"`
	FailedSubmissions    int      `json:"failed_submissions"`
	CancelledSubmissions int      `json:"cancelled_submissions"`
	PublishRequests      int      `json:"publish_requests"`
	SucceededPublishes   int      `json:"succeeded_publishes"`
	SupersededPublishes  int      `json:"superseded_publishes"`
	FailedPublishes      int      `json:"failed_publishes"`
	MaxQueuedSubmissions int      `json:"max_queued_submissions"`
	MaxQueuedPublishes   int      `json:"max_queued_publishes"`
	DaemonCycles         int      `json:"daemon_cycles"`
	DurationMilliseconds int64    `json:"duration_ms"`
	RemoteMatchesLocal   bool     `json:"remote_matches_local"`
	BlockedBranches      []string `json:"blocked_branches"`
	SucceededBranches    []string `json:"succeeded_branches"`
}

func TestStressParallelAgentQueueAndPublishCoalescing(t *testing.T) {
	repoRoot, remoteDir := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	updatePublishMode(t, repoRoot, "auto")

	plans := prepareStressAgents(t, repoRoot)

	var daemonLogs bytes.Buffer
	daemonCtx, cancelDaemon := context.WithCancel(context.Background())
	defer cancelDaemon()

	daemonDone := make(chan error, 1)
	go func() {
		daemonDone <- runDaemonLoop(daemonCtx, daemonOptions{
			repoPath: repoRoot,
			interval: 10 * time.Millisecond,
			jsonLogs: true,
		}, &daemonLogs)
	}()

	start := time.Now()
	submitStressAgents(t, plans)

	report := waitForStressCompletion(t, repoRoot, remoteDir, plans, start, &daemonLogs)
	cancelDaemon()
	if err := <-daemonDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("runDaemonLoop returned error: %v", err)
	}

	if report.SucceededSubmissions != len(plans)-1 {
		t.Fatalf("expected %d succeeded submissions, got %+v", len(plans)-1, report)
	}
	if report.BlockedSubmissions != 1 {
		t.Fatalf("expected 1 blocked submission, got %+v", report)
	}
	if report.FailedSubmissions != 0 || report.CancelledSubmissions != 0 {
		t.Fatalf("expected no failed or cancelled submissions, got %+v", report)
	}
	if report.PublishRequests != report.SucceededSubmissions {
		t.Fatalf("expected one publish request per succeeded submission, got %+v", report)
	}
	if report.SucceededPublishes != 1 {
		t.Fatalf("expected one final succeeded publish, got %+v", report)
	}
	if report.SupersededPublishes != report.SucceededSubmissions-1 {
		t.Fatalf("expected %d superseded publishes, got %+v", report.SucceededSubmissions-1, report)
	}
	if report.FailedPublishes != 0 {
		t.Fatalf("expected no failed publishes, got %+v", report)
	}
	if !report.RemoteMatchesLocal {
		t.Fatalf("expected remote to match local head, got %+v", report)
	}
	if report.MaxQueuedSubmissions < 2 {
		t.Fatalf("expected queue depth to exceed 1, got %+v", report)
	}
	if report.MaxQueuedPublishes < 2 {
		t.Fatalf("expected publish coalescing queue depth to exceed 1, got %+v", report)
	}
	if report.DaemonCycles < len(plans) {
		t.Fatalf("expected daemon to process multiple cycles, got %+v", report)
	}
	if len(report.BlockedBranches) != 1 {
		t.Fatalf("expected one blocked branch, got %+v", report)
	}
	if !slices.Contains([]string{"feature/conflict-a", "feature/conflict-b"}, report.BlockedBranches[0]) {
		t.Fatalf("expected blocked conflict branch, got %+v", report)
	}

	assertProtectedWorktreeClean(t, repoRoot)
	assertRemoteHeadMatchesLocal(t, repoRoot, remoteDir)

	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("Marshal report: %v", err)
	}
	if reportPath := os.Getenv("MAINLINE_STRESS_REPORT_PATH"); reportPath != "" {
		if err := os.WriteFile(reportPath, payload, 0o644); err != nil {
			t.Fatalf("WriteFile stress report: %v", err)
		}
	}
	t.Logf("stress report:\n%s", payload)
}

func prepareStressAgents(t *testing.T, repoRoot string) []stressAgentPlan {
	t.Helper()

	var plans []stressAgentPlan
	for i := range 8 {
		branch := fmt.Sprintf("feature/agent-%d", i+1)
		worktree := filepath.Join(t.TempDir(), fmt.Sprintf("agent-%d", i+1))
		runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", branch, worktree)
		writeFileAndCommit(t, worktree, fmt.Sprintf("agent-%d.txt", i+1), fmt.Sprintf("agent %d\n", i+1), branch)
		plans = append(plans, stressAgentPlan{
			Name:     fmt.Sprintf("agent-%d", i+1),
			Branch:   branch,
			Worktree: worktree,
			Delay:    time.Duration(i*5) * time.Millisecond,
			Mode:     "unique",
		})
	}

	conflictA := filepath.Join(t.TempDir(), "conflict-a")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/conflict-a", conflictA)
	replaceFileAndCommit(t, conflictA, "README.md", "# conflict a\n", "conflict a")
	plans = append(plans, stressAgentPlan{
		Name:     "conflict-a",
		Branch:   "feature/conflict-a",
		Worktree: conflictA,
		Delay:    10 * time.Millisecond,
		Mode:     "conflict",
	})

	conflictB := filepath.Join(t.TempDir(), "conflict-b")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/conflict-b", conflictB)
	replaceFileAndCommit(t, conflictB, "README.md", "# conflict b\n", "conflict b")
	plans = append(plans, stressAgentPlan{
		Name:     "conflict-b",
		Branch:   "feature/conflict-b",
		Worktree: conflictB,
		Delay:    15 * time.Millisecond,
		Mode:     "conflict",
	})

	return plans
}

func submitStressAgents(t *testing.T, plans []stressAgentPlan) {
	t.Helper()

	var wg sync.WaitGroup
	errCh := make(chan error, len(plans))

	for _, plan := range plans {
		plan := plan
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(plan.Delay)

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if err := runSubmit([]string{"--repo", plan.Worktree}, &stdout, &stderr); err != nil {
				errCh <- fmt.Errorf("submit %s: %w (%s)", plan.Branch, err, stderr.String())
				return
			}
		}()
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("stress submit failed: %v", err)
		}
	}
}

func waitForStressCompletion(t *testing.T, repoRoot string, remoteDir string, plans []stressAgentPlan, start time.Time, daemonLogs *bytes.Buffer) stressReport {
	t.Helper()

	store, repoRecord := openRepoStore(t, repoRoot)
	deadline := time.Now().Add(60 * time.Second)

	report := stressReport{
		AgentCount: len(plans),
	}

	for time.Now().Before(deadline) {
		status, err := collectStatus(repoRoot, 1)
		if err != nil {
			t.Fatalf("collectStatus: %v", err)
		}
		if status.Counts.QueuedSubmissions > report.MaxQueuedSubmissions {
			report.MaxQueuedSubmissions = status.Counts.QueuedSubmissions
		}
		if status.Counts.QueuedPublishes > report.MaxQueuedPublishes {
			report.MaxQueuedPublishes = status.Counts.QueuedPublishes
		}

		submissions, err := store.ListIntegrationSubmissions(context.Background(), repoRecord.ID)
		if err != nil {
			t.Fatalf("ListIntegrationSubmissions: %v", err)
		}
		requests, err := store.ListPublishRequests(context.Background(), repoRecord.ID)
		if err != nil {
			t.Fatalf("ListPublishRequests: %v", err)
		}

		terminalSubmissions := 0
		activePublishes := 0
		report.SucceededSubmissions = 0
		report.BlockedSubmissions = 0
		report.FailedSubmissions = 0
		report.CancelledSubmissions = 0
		report.SucceededBranches = report.SucceededBranches[:0]
		report.BlockedBranches = report.BlockedBranches[:0]
		for _, submission := range submissions {
			switch submission.Status {
			case "succeeded":
				terminalSubmissions++
				report.SucceededSubmissions++
				report.SucceededBranches = append(report.SucceededBranches, submission.BranchName)
			case "blocked":
				terminalSubmissions++
				report.BlockedSubmissions++
				report.BlockedBranches = append(report.BlockedBranches, submission.BranchName)
			case "failed":
				terminalSubmissions++
				report.FailedSubmissions++
			case "cancelled":
				terminalSubmissions++
				report.CancelledSubmissions++
			case "queued", "running":
			}
		}

		report.PublishRequests = len(requests)
		report.SucceededPublishes = 0
		report.SupersededPublishes = 0
		report.FailedPublishes = 0
		for _, request := range requests {
			switch request.Status {
			case "queued", "running":
				activePublishes++
			case "succeeded":
				report.SucceededPublishes++
			case "superseded":
				report.SupersededPublishes++
			case "failed":
				report.FailedPublishes++
			}
		}

		localHead := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
		remoteHead := trimNewline(runTestCommand(t, remoteDir, "git", "rev-parse", "refs/heads/main"))
		report.RemoteMatchesLocal = localHead == remoteHead

		if terminalSubmissions == len(plans) && activePublishes == 0 && report.RemoteMatchesLocal {
			report.DurationMilliseconds = time.Since(start).Milliseconds()
			report.DaemonCycles = countDaemonCycles(daemonLogs.Bytes())
			return report
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("stress run did not settle before timeout")
	return stressReport{}
}

func countDaemonCycles(raw []byte) int {
	type record struct {
		Event string `json:"event"`
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	count := 0
	for {
		var item record
		if err := decoder.Decode(&item); err != nil {
			break
		}
		if item.Event == "cycle.completed" {
			count++
		}
	}
	return count
}
