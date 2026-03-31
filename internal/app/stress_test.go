package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/recallnet/mainline/internal/policy"
	"github.com/recallnet/mainline/internal/state"
)

type stressAgentPlan struct {
	Name     string
	Branch   string
	Worktree string
	Delay    time.Duration
	Mode     string
}

type stressReport struct {
	Seed                     int64    `json:"seed,omitempty"`
	Randomized               bool     `json:"randomized"`
	FailureMode              string   `json:"failure_mode,omitempty"`
	AgentCount               int      `json:"agent_count"`
	SucceededSubmissions     int      `json:"succeeded_submissions"`
	BlockedSubmissions       int      `json:"blocked_submissions"`
	FailedSubmissions        int      `json:"failed_submissions"`
	CancelledSubmissions     int      `json:"cancelled_submissions"`
	PublishRequests          int      `json:"publish_requests"`
	SucceededPublishes       int      `json:"succeeded_publishes"`
	SupersededPublishes      int      `json:"superseded_publishes"`
	FailedPublishes          int      `json:"failed_publishes"`
	MaxQueuedSubmissions     int      `json:"max_queued_submissions"`
	MaxQueuedPublishes       int      `json:"max_queued_publishes"`
	DaemonCycles             int      `json:"daemon_cycles"`
	DurationMilliseconds     int64    `json:"duration_ms"`
	RemoteMatchesLocal       bool     `json:"remote_matches_local"`
	BlockedBranches          []string `json:"blocked_branches"`
	SucceededBranches        []string `json:"succeeded_branches"`
	InjectedPoints           []string `json:"injected_points,omitempty"`
	RetriedSubmissionIDs     []int64  `json:"retried_submission_ids,omitempty"`
	RetriedPublishRequestIDs []int64  `json:"retried_publish_request_ids,omitempty"`
}

type randomizedStressScenario struct {
	Seed           int64
	FailureMode    string
	DaemonInterval time.Duration
}

type stressFaultController struct {
	mu            sync.Mutex
	rng           *rand.Rand
	randomized    bool
	failureMode   string
	injected      bool
	injectedAt    []string
	delayCeiling  time.Duration
	stateDelayCap time.Duration
}

func TestStressParallelAgentQueueAndPublishCoalescing(t *testing.T) {
	report := runStressScenario(t, randomizedStressScenario{})

	if report.SucceededSubmissions != report.AgentCount-1 {
		t.Fatalf("expected %d succeeded submissions, got %+v", report.AgentCount-1, report)
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
	if report.DaemonCycles < report.AgentCount {
		t.Fatalf("expected daemon to process multiple cycles, got %+v", report)
	}
	if len(report.BlockedBranches) != 1 {
		t.Fatalf("expected one blocked branch, got %+v", report)
	}
	if !slices.Contains([]string{"feature/conflict-a", "feature/conflict-b"}, report.BlockedBranches[0]) {
		t.Fatalf("expected blocked conflict branch, got %+v", report)
	}
}

func TestStressRandomizedSeededReplay(t *testing.T) {
	seed := int64(20260331)
	if raw := os.Getenv("MAINLINE_STRESS_SEED"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			t.Fatalf("MAINLINE_STRESS_SEED: %v", err)
		}
		seed = parsed
	}

	first := runStressScenario(t, newRandomizedStressScenario(seed))
	second := runStressScenario(t, newRandomizedStressScenario(seed))

	if !first.Randomized || !second.Randomized {
		t.Fatalf("expected randomized runs, got %+v and %+v", first, second)
	}
	if first.FailureMode != second.FailureMode {
		t.Fatalf("expected replayed failure mode %q, got %q", first.FailureMode, second.FailureMode)
	}
	if !slices.Equal(first.InjectedPoints, second.InjectedPoints) {
		t.Fatalf("expected replayed injected points %v, got %v", first.InjectedPoints, second.InjectedPoints)
	}
	if first.BlockedSubmissions != 1 || second.BlockedSubmissions != 1 {
		t.Fatalf("expected blocked conflict path in both runs, got %+v and %+v", first, second)
	}
	if first.SupersededPublishes == 0 || second.SupersededPublishes == 0 {
		t.Fatalf("expected superseded publish path in both runs, got %+v and %+v", first, second)
	}
	if len(first.RetriedSubmissionIDs) == 0 && len(first.RetriedPublishRequestIDs) == 0 {
		t.Fatalf("expected randomized run to require an operator retry, got %+v", first)
	}
	if len(second.RetriedSubmissionIDs) == 0 && len(second.RetriedPublishRequestIDs) == 0 {
		t.Fatalf("expected replayed run to require an operator retry, got %+v", second)
	}
	if !first.RemoteMatchesLocal || !second.RemoteMatchesLocal {
		t.Fatalf("expected final replayed runs to publish latest head, got %+v and %+v", first, second)
	}
}

func TestStressFailureInjectionMatrix(t *testing.T) {
	testCases := []struct {
		name string
		mode string
		seed int64
	}{
		{name: "fetch", mode: "integration.fetch", seed: 101},
		{name: "rebase", mode: "integration.rebase", seed: 102},
		{name: "checks", mode: "checks.start", seed: 103},
		{name: "push", mode: "publish.push", seed: 104},
		{name: "state-write", mode: "state-write", seed: 105},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			report := runStressScenario(t, randomizedStressScenario{
				Seed:        tc.seed,
				FailureMode: tc.mode,
			})

			if len(report.InjectedPoints) == 0 {
				t.Fatalf("expected injected point for %s, got %+v", tc.mode, report)
			}
			if !report.RemoteMatchesLocal {
				t.Fatalf("expected final publish to recover for %s, got %+v", tc.mode, report)
			}
			if report.BlockedSubmissions != 1 {
				t.Fatalf("expected conflict path to stay covered for %s, got %+v", tc.mode, report)
			}
			if report.SupersededPublishes == 0 {
				t.Fatalf("expected publish superseding for %s, got %+v", tc.mode, report)
			}
		})
	}
}

func runStressScenario(t *testing.T, scenario randomizedStressScenario) stressReport {
	t.Helper()

	repoRoot, remoteDir := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	updatePublishMode(t, repoRoot, "auto")
	if scenario.FailureMode == "checks.start" {
		enableStressPrePublishCheck(t, repoRoot)
	}

	controller := newStressFaultController(scenario)
	restoreApp := setAppTestFaultHooks(testFaultHooks{before: controller.applyAppFault})
	restoreState := state.SetTestFaultHooks(controller.applyStateFault)
	defer restoreState()
	defer restoreApp()

	plans := prepareStressAgents(t, repoRoot, controller)

	var daemonLogs bytes.Buffer
	daemonCtx, cancelDaemon := context.WithCancel(context.Background())
	defer cancelDaemon()

	daemonDone := make(chan error, 1)
	go func() {
		daemonDone <- runStressWorkerLoop(daemonCtx, repoRoot, controller.daemonInterval(), &daemonLogs)
	}()

	start := time.Now()
	submitStressAgents(t, plans)

	report := waitForStressSettlement(t, repoRoot, remoteDir, plans, start, &daemonLogs, false)
	report.Seed = scenario.Seed
	report.Randomized = scenario.Seed != 0
	report.FailureMode = scenario.FailureMode
	report.InjectedPoints = append(report.InjectedPoints[:0], controller.snapshotInjectedPoints()...)

	controller.clearFailure()
	retryFailedStressItems(t, repoRoot, &report)

	finalReport := waitForStressSettlement(t, repoRoot, remoteDir, plans, start, &daemonLogs, true)
	finalReport.Seed = scenario.Seed
	finalReport.Randomized = scenario.Seed != 0
	finalReport.FailureMode = scenario.FailureMode
	finalReport.InjectedPoints = append(finalReport.InjectedPoints[:0], controller.snapshotInjectedPoints()...)
	finalReport.RetriedSubmissionIDs = append(finalReport.RetriedSubmissionIDs, report.RetriedSubmissionIDs...)
	finalReport.RetriedPublishRequestIDs = append(finalReport.RetriedPublishRequestIDs, report.RetriedPublishRequestIDs...)
	if report.MaxQueuedSubmissions > finalReport.MaxQueuedSubmissions {
		finalReport.MaxQueuedSubmissions = report.MaxQueuedSubmissions
	}
	if report.MaxQueuedPublishes > finalReport.MaxQueuedPublishes {
		finalReport.MaxQueuedPublishes = report.MaxQueuedPublishes
	}

	cancelDaemon()
	if err := <-daemonDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("runStressWorkerLoop returned error: %v", err)
	}

	assertProtectedWorktreeClean(t, repoRoot)
	assertRemoteHeadMatchesLocal(t, repoRoot, remoteDir)
	writeStressReport(t, finalReport)
	return finalReport
}

func newRandomizedStressScenario(seed int64) randomizedStressScenario {
	rng := rand.New(rand.NewSource(seed))
	modes := []string{"integration.fetch", "integration.rebase", "checks.start", "state-write"}
	return randomizedStressScenario{
		Seed:           seed,
		FailureMode:    modes[rng.Intn(len(modes))],
		DaemonInterval: time.Duration(5+rng.Intn(11)) * time.Millisecond,
	}
}

func newStressFaultController(scenario randomizedStressScenario) *stressFaultController {
	if scenario.Seed == 0 {
		return &stressFaultController{
			rng:           rand.New(rand.NewSource(1)),
			delayCeiling:  0,
			stateDelayCap: 0,
		}
	}

	return &stressFaultController{
		rng:           rand.New(rand.NewSource(scenario.Seed)),
		randomized:    true,
		failureMode:   scenario.FailureMode,
		delayCeiling:  12 * time.Millisecond,
		stateDelayCap: 6 * time.Millisecond,
	}
}

func (c *stressFaultController) daemonInterval() time.Duration {
	if !c.randomized {
		return 10 * time.Millisecond
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Duration(5+c.rng.Intn(11)) * time.Millisecond
}

func (c *stressFaultController) applyAppFault(point string) error {
	c.maybeDelay(c.delayCeiling)

	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.randomized || c.injected {
		return nil
	}
	if point != c.failureMode {
		return nil
	}

	c.injected = true
	c.injectedAt = append(c.injectedAt, point)
	return fmt.Errorf("injected failure at %s", point)
}

func (c *stressFaultController) applyStateFault(point string) error {
	c.maybeDelay(c.stateDelayCap)

	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.randomized || c.injected || c.failureMode != "state-write" {
		return nil
	}
	if point != "UpdatePublishRequestStatus" {
		return nil
	}

	c.injected = true
	c.injectedAt = append(c.injectedAt, "state."+point)
	return fmt.Errorf("injected failure at state.%s", point)
}

func (c *stressFaultController) maybeDelay(cap time.Duration) {
	if !c.randomized || cap <= 0 {
		return
	}
	c.mu.Lock()
	delay := time.Duration(c.rng.Int63n(int64(cap) + 1))
	c.mu.Unlock()
	time.Sleep(delay)
}

func (c *stressFaultController) clearFailure() {
	c.mu.Lock()
	c.failureMode = ""
	c.mu.Unlock()
}

func (c *stressFaultController) snapshotInjectedPoints() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.injectedAt...)
}

func prepareStressAgents(t *testing.T, repoRoot string, controller *stressFaultController) []stressAgentPlan {
	t.Helper()

	var plans []stressAgentPlan
	for i := range 8 {
		branch := fmt.Sprintf("feature/agent-%d", i+1)
		worktree := filepath.Join(t.TempDir(), fmt.Sprintf("agent-%d", i+1))
		runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", branch, worktree)
		writeFileAndCommit(t, worktree, fmt.Sprintf("agent-%d.txt", i+1), fmt.Sprintf("agent %d\n", i+1), branch)
		delay := time.Duration(i*5) * time.Millisecond
		if controller.randomized {
			delay += time.Duration(controller.rng.Intn(25)) * time.Millisecond
		}
		plans = append(plans, stressAgentPlan{
			Name:     fmt.Sprintf("agent-%d", i+1),
			Branch:   branch,
			Worktree: worktree,
			Delay:    delay,
			Mode:     "unique",
		})
	}

	conflictA := filepath.Join(t.TempDir(), "conflict-a")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/conflict-a", conflictA)
	replaceFileAndCommit(t, conflictA, "README.md", "# conflict a\n", "conflict a")
	conflictADelay := 10 * time.Millisecond
	if controller.randomized {
		conflictADelay += time.Duration(controller.rng.Intn(25)) * time.Millisecond
	}
	plans = append(plans, stressAgentPlan{
		Name:     "conflict-a",
		Branch:   "feature/conflict-a",
		Worktree: conflictA,
		Delay:    conflictADelay,
		Mode:     "conflict",
	})

	conflictB := filepath.Join(t.TempDir(), "conflict-b")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/conflict-b", conflictB)
	replaceFileAndCommit(t, conflictB, "README.md", "# conflict b\n", "conflict b")
	conflictBDelay := 15 * time.Millisecond
	if controller.randomized {
		conflictBDelay += time.Duration(controller.rng.Intn(25)) * time.Millisecond
	}
	plans = append(plans, stressAgentPlan{
		Name:     "conflict-b",
		Branch:   "feature/conflict-b",
		Worktree: conflictB,
		Delay:    conflictBDelay,
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

func runStressWorkerLoop(ctx context.Context, repoRoot string, interval time.Duration, daemonLogs *bytes.Buffer) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		result, err := runOneCycle(repoRoot)
		if err != nil {
			fmt.Fprintf(daemonLogs, "{\"event\":\"cycle.failed\",\"message\":%q}\n", err.Error())
		} else {
			fmt.Fprintf(daemonLogs, "{\"event\":\"cycle.completed\",\"message\":%q}\n", result)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func waitForStressSettlement(t *testing.T, repoRoot string, remoteDir string, plans []stressAgentPlan, start time.Time, daemonLogs *bytes.Buffer, requireRemoteMatch bool) stressReport {
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

		if terminalSubmissions == len(plans) && activePublishes == 0 && (!requireRemoteMatch || report.RemoteMatchesLocal) {
			report.DurationMilliseconds = time.Since(start).Milliseconds()
			report.DaemonCycles = countDaemonCycles(daemonLogs.Bytes())
			return report
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("stress run did not settle before timeout")
	return stressReport{}
}

func retryFailedStressItems(t *testing.T, repoRoot string, report *stressReport) {
	t.Helper()

	store, repoRecord := openRepoStore(t, repoRoot)
	submissions, err := store.ListIntegrationSubmissions(context.Background(), repoRecord.ID)
	if err != nil {
		t.Fatalf("ListIntegrationSubmissions: %v", err)
	}
	for _, submission := range submissions {
		if submission.Status != "failed" {
			continue
		}

		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if err := runRetry([]string{"--repo", repoRoot, "--submission", strconv.FormatInt(submission.ID, 10)}, &stdout, &stderr); err != nil {
			t.Fatalf("retry submission %d: %v (%s)", submission.ID, err, stderr.String())
		}
		report.RetriedSubmissionIDs = append(report.RetriedSubmissionIDs, submission.ID)
	}

	requests, err := store.ListPublishRequests(context.Background(), repoRecord.ID)
	if err != nil {
		t.Fatalf("ListPublishRequests: %v", err)
	}
	for _, request := range requests {
		if request.Status != "failed" {
			continue
		}

		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if err := runRetry([]string{"--repo", repoRoot, "--publish", strconv.FormatInt(request.ID, 10)}, &stdout, &stderr); err != nil {
			t.Fatalf("retry publish %d: %v (%s)", request.ID, err, stderr.String())
		}
		report.RetriedPublishRequestIDs = append(report.RetriedPublishRequestIDs, request.ID)
	}
}

func enableStressPrePublishCheck(t *testing.T, repoRoot string) {
	t.Helper()

	cfg, _, err := policy.LoadOrDefault(repoRoot)
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	cfg.Checks.PrePublish = []string{"true"}
	if err := policy.SaveFile(repoRoot, cfg); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}
	runTestCommand(t, repoRoot, "git", "add", "mainline.toml")
	runTestCommand(t, repoRoot, "git", "commit", "-m", "enable stress pre-publish check")
}

func writeStressReport(t *testing.T, report stressReport) {
	t.Helper()

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
