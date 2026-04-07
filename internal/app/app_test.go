package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/recallnet/mainline/internal/domain"
	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/policy"
	"github.com/recallnet/mainline/internal/state"
	_ "modernc.org/sqlite"
)

func sortedJSONKeys(payload map[string]any) []string {
	keys := make([]string, 0, len(payload))
	for key := range payload {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func TestCLIHelp(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := runCLI([]string{"--help"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	if !strings.Contains(stdout.String(), "Usage:") {
		t.Fatalf("expected help output, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "submit --check-only --json") {
		t.Fatalf("expected turbo submit guidance in help, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "wait --submission 42 --for landed --json --timeout 30m") {
		t.Fatalf("expected wait guidance in help, got %q", stdout.String())
	}
}

func TestMQHelpUsesMQIdentity(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := runCLIWithName("mq", []string{"--help"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCLIWithName returned error: %v", err)
	}

	if !strings.Contains(stdout.String(), "mq coordinates local protected-branch integrations and publishes.") {
		t.Fatalf("expected mq help identity, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "mq land --json --timeout 30m") {
		t.Fatalf("expected mq help to include controller path, got %q", stdout.String())
	}
}

func TestDaemonHelp(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := runDaemon([]string{"--help"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runDaemon returned error: %v", err)
	}

	if !strings.Contains(stdout.String(), "mainlined runs the optional background worker loop") {
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
	if err := runCLIWithName("mq", []string{"version"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCLIWithName version returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "mq v1.2.3 commit=abc1234 date=2026-03-31T00:00:00Z") {
		t.Fatalf("expected version output, got %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := runDaemonWithName("mainlined", []string{"--version"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runDaemonWithName version returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "mainlined v1.2.3 commit=abc1234 date=2026-03-31T00:00:00Z") {
		t.Fatalf("expected daemon version output, got %q", stdout.String())
	}
}

func TestGlobalJSONVersionReportsStructuredBuildMetadata(t *testing.T) {
	originalVersion, originalCommit, originalDate := Version, Commit, Date
	Version, Commit, Date = "v1.2.3", "abc1234", "2026-03-31T00:00:00Z"
	t.Cleanup(func() {
		Version, Commit, Date = originalVersion, originalCommit, originalDate
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLIWithName("mq", []string{"--json", "version"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCLIWithName version returned error: %v", err)
	}

	var result versionResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result.Program != "mq" || result.Version != "v1.2.3" || result.Commit != "abc1234" || result.Date != "2026-03-31T00:00:00Z" {
		t.Fatalf("unexpected version json: %+v", result)
	}
}

func TestGlobalJSONCompletionReportsStructuredScript(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLI([]string{"--json", "completion", "bash"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	var result struct {
		Shell  string `json:"shell"`
		Script string `json:"script"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result.Shell != "bash" || !strings.Contains(result.Script, "complete -F _mainline_completions mainline") {
		t.Fatalf("unexpected completion json: %+v", result)
	}
}

func TestGlobalJSONForwardsToRunOnceAndPublish(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLI([]string{"--json", "publish", "--repo", repoRoot}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("publish returned error: %v", err)
	}
	var publish publishResult
	if err := json.Unmarshal(stdout.Bytes(), &publish); err != nil {
		t.Fatalf("Unmarshal publish: %v", err)
	}
	if !publish.OK || publish.PublishRequestID == 0 {
		t.Fatalf("unexpected publish json: %+v", publish)
	}
	if publish.Status != "queued" && publish.Status != "succeeded" {
		t.Fatalf("unexpected publish json: %+v", publish)
	}

	stdout.Reset()
	stderr.Reset()
	if err := runCLI([]string{"--json", "run-once", "--repo", repoRoot}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("run-once returned error: %v", err)
	}
	var result struct {
		OK     bool   `json:"ok"`
		Repo   string `json:"repo"`
		Result string `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal run-once: %v", err)
	}
	if !result.OK || result.Repo != repoRoot || result.Result == "" {
		t.Fatalf("unexpected run-once json: %+v", result)
	}
}

func TestStatusJSONContractContainsStableTopLevelFields(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLI([]string{"status", "--repo", repoRoot, "--json"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	wantKeys := []string{
		"counts",
		"current_branch",
		"current_worktree",
		"execution_estimate",
		"has_blocked_submissions",
		"has_queued_work",
		"has_running_publishes",
		"has_running_submissions",
		"protected_branch",
		"protected_branch_sha",
		"protected_upstream",
		"queue_length",
		"queue_summary",
		"recent_events",
		"repository_root",
		"state",
		"state_path",
	}
	gotKeys := sortedJSONKeys(payload)
	for _, key := range wantKeys {
		if !slices.Contains(gotKeys, key) {
			t.Fatalf("expected status json to contain key %q, got %v", key, gotKeys)
		}
	}
}

func TestStatusJSONIncludesOperatorSummaryFields(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	featurePath := filepath.Join(t.TempDir(), "feature-status-summary")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/status-summary", featurePath)
	writeFileAndCommit(t, featurePath, "summary.txt", "summary\n", "status summary")
	t.Setenv("MAINLINE_DISABLE_SUBMIT_DRAIN", "1")
	submitBranch(t, featurePath)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLI([]string{"status", "--repo", repoRoot, "--json"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	var status statusResult
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if status.State != "queued" {
		t.Fatalf("expected queued state, got %+v", status)
	}
	if status.QueueLength != 1 {
		t.Fatalf("expected queue length 1, got %+v", status)
	}
	if status.HasQueuedWork != true || status.HasRunningPublishes || status.HasRunningSubmissions || status.HasBlockedSubmissions {
		t.Fatalf("expected explicit queued-work booleans, got %+v", status)
	}
	if status.QueueSummary.Headline != "queued" || status.QueueSummary.QueueLength != 1 || !status.QueueSummary.HasQueuedWork {
		t.Fatalf("expected queue_summary to mirror queued state, got %+v", status.QueueSummary)
	}
}

func TestStatusJSONIncludesLocalProtectedRebaseGuidance(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	featurePath := filepath.Join(t.TempDir(), "feature-status-rebase")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/status-rebase", featurePath)
	writeFileAndCommit(t, featurePath, "feature.txt", "feature\n", "feature commit")
	writeFileAndCommit(t, repoRoot, "main.txt", "main\n", "main commit")

	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer func() {
		if chdirErr := os.Chdir(originalWD); chdirErr != nil {
			t.Fatalf("restore cwd: %v", chdirErr)
		}
	}()
	if err := os.Chdir(featurePath); err != nil {
		t.Fatalf("Chdir(featurePath): %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLI([]string{"status", "--json"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	var status statusResult
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if status.RebaseGuidance == nil || !status.RebaseGuidance.NeedsRebase {
		t.Fatalf("expected rebase guidance, got %+v", status)
	}
	if status.RebaseGuidance.BaseBranch != "main" || !strings.Contains(status.RebaseGuidance.Command, "mq rebase --repo") || !strings.Contains(status.RebaseGuidance.Command, "--branch feature/status-rebase") {
		t.Fatalf("expected mq rebase command, got %+v", status.RebaseGuidance)
	}
	if status.CurrentBranchStatus == nil || status.CurrentBranchStatus.BehindCount == 0 {
		t.Fatalf("expected current branch behind local protected branch, got %+v", status.CurrentBranchStatus)
	}
}

func TestStatusTextShowsLocalProtectedRebaseGuidance(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	featurePath := filepath.Join(t.TempDir(), "feature-status-rebase-text")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/status-rebase-text", featurePath)
	writeFileAndCommit(t, featurePath, "feature.txt", "feature\n", "feature commit")
	writeFileAndCommit(t, repoRoot, "main.txt", "main\n", "main commit")

	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer func() {
		if chdirErr := os.Chdir(originalWD); chdirErr != nil {
			t.Fatalf("restore cwd: %v", chdirErr)
		}
	}()
	if err := os.Chdir(featurePath); err != nil {
		t.Fatalf("Chdir(featurePath): %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLI([]string{"status"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	text := stdout.String()
	if !strings.Contains(text, `Rebase guidance: current branch is behind local protected branch "main"`) {
		t.Fatalf("expected rebase guidance in text output, got %q", text)
	}
	if !strings.Contains(text, "Recommended command: mq rebase --repo") || !strings.Contains(text, "--branch feature/status-rebase-text") {
		t.Fatalf("expected mq rebase command in text output, got %q", text)
	}
}

func TestStatusPrefersPublishingHeadlineWhenBlockedSubmissionAlsoExists(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	updatePublishMode(t, repoRoot, "auto")

	layout, err := git.DiscoverRepositoryLayout(repoRoot)
	if err != nil {
		t.Fatalf("DiscoverRepositoryLayout: %v", err)
	}
	store := state.NewStore(state.DefaultPath(layout.GitDir))
	repoRecord, err := store.GetRepositoryByPath(context.Background(), layout.RepositoryRoot)
	if err != nil {
		t.Fatalf("GetRepositoryByPath: %v", err)
	}
	protectedSHA := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	runningRequest, err := store.CreatePublishRequest(context.Background(), state.PublishRequest{
		RepoID:    repoRecord.ID,
		TargetSHA: protectedSHA,
		Status:    domain.PublishStatusQueued,
	})
	if err != nil {
		t.Fatalf("CreatePublishRequest: %v", err)
	}
	if _, err := store.UpdatePublishRequestStatus(context.Background(), runningRequest.ID, domain.PublishStatusRunning, sql.NullInt64{}); err != nil {
		t.Fatalf("UpdatePublishRequestStatus: %v", err)
	}

	blockedSubmission, err := store.CreateIntegrationSubmission(context.Background(), state.IntegrationSubmission{
		RepoID:         repoRecord.ID,
		BranchName:     "feature/blocked",
		SourceRef:      "feature/blocked",
		RefKind:        domain.RefKindBranch,
		SourceWorktree: filepath.Join(t.TempDir(), "feature-blocked"),
		SourceSHA:      "deadbeef",
		RequestedBy:    "test",
		Priority:       submissionPriorityNormal,
		Status:         domain.SubmissionStatusQueued,
	})
	if err != nil {
		t.Fatalf("CreateIntegrationSubmission: %v", err)
	}
	blockedSubmission, err = store.UpdateIntegrationSubmissionStatus(context.Background(), blockedSubmission.ID, domain.SubmissionStatusBlocked, "rebase conflict")
	if err != nil {
		t.Fatalf("UpdateIntegrationSubmissionStatus: %v", err)
	}
	if _, err := store.AppendEvent(context.Background(), state.EventRecord{
		RepoID:    repoRecord.ID,
		ItemType:  domain.ItemTypeIntegrationSubmission,
		ItemID:    state.NullInt64(blockedSubmission.ID),
		EventType: domain.EventTypeIntegrationBlocked,
		Payload: mustJSON(blockedSubmissionDetails{
			Error:           "rebase conflict",
			BlockedReason:   domain.BlockedReasonRebaseConflict,
			ConflictFiles:   []string{"README.md"},
			ProtectedTipSHA: protectedSHA,
			RetryHint:       "manual-rebase-from-tip",
		}),
	}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLI([]string{"status", "--repo", repoRoot, "--json"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	var status statusResult
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if status.State != "publishing" {
		t.Fatalf("expected publishing headline, got %+v", status)
	}
	if len(status.Alerts) == 0 || !strings.Contains(status.Alerts[0], "Separate blocked submissions") {
		t.Fatalf("expected mixed-state alert, got %+v", status.Alerts)
	}
}

func TestStatusAndSubmitJSONIncludeRollingExecutionEstimate(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	updatePublishMode(t, repoRoot, "auto")

	restoreHooks := setAppTestFaultHooks(testFaultHooks{
		before: func(point string) error {
			if point == "publish.push" {
				time.Sleep(1100 * time.Millisecond)
			}
			return nil
		},
	})
	defer restoreHooks()

	featureOne := filepath.Join(t.TempDir(), "feature-estimate-one")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/estimate-one", featureOne)
	writeFileAndCommit(t, featureOne, "estimate-one.txt", "one\n", "estimate one")
	submitBranch(t, featureOne)
	runOnce(t, repoRoot)
	runOnce(t, repoRoot)

	t.Setenv("MAINLINE_DISABLE_SUBMIT_DRAIN", "1")
	featureTwo := filepath.Join(t.TempDir(), "feature-estimate-two")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/estimate-two", featureTwo)
	writeFileAndCommit(t, featureTwo, "estimate-two.txt", "two\n", "estimate two")

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	if err := runSubmit([]string{"--repo", featureTwo, "--json"}, newStepPrinter(&submitOut), &submitErr); err != nil {
		t.Fatalf("runSubmit returned error: %v", err)
	}
	var submit submitResult
	if err := json.Unmarshal(submitOut.Bytes(), &submit); err != nil {
		t.Fatalf("Unmarshal submit: %v", err)
	}
	if submit.QueuePosition != 1 || submit.EstimatedCompletionMS <= 0 || submit.EstimateBasis != string(submissionOutcomeLanded) {
		t.Fatalf("unexpected submit estimate: %+v", submit)
	}

	var statusOut bytes.Buffer
	var statusErr bytes.Buffer
	if err := runCLI([]string{"status", "--repo", repoRoot, "--json"}, newStepPrinter(&statusOut), &statusErr); err != nil {
		t.Fatalf("runCLI status returned error: %v", err)
	}
	var status statusResult
	if err := json.Unmarshal(statusOut.Bytes(), &status); err != nil {
		t.Fatalf("Unmarshal status: %v", err)
	}
	if status.ExecutionEstimate.SampleCount < 1 || status.ExecutionEstimate.AvgExecutionMS <= 0 || status.ExecutionEstimate.Basis != submissionOutcomeLanded {
		t.Fatalf("unexpected execution estimate: %+v", status.ExecutionEstimate)
	}
	if status.LatestSubmission == nil || status.LatestSubmission.QueuePosition != 1 || status.LatestSubmission.EstimatedCompletionMS <= 0 {
		t.Fatalf("expected queued submission estimate in status, got %+v", status.LatestSubmission)
	}
}

func TestWaitBySubmissionIDReturnsIntegratedOutcome(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	featurePath := filepath.Join(t.TempDir(), "feature-wait-integrated")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/wait-integrated", featurePath)
	writeFileAndCommit(t, featurePath, "wait.txt", "wait\n", "wait feature")

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	if err := runSubmit([]string{"--repo", featurePath, "--json"}, newStepPrinter(&submitOut), &submitErr); err != nil {
		t.Fatalf("runSubmit returned error: %v", err)
	}
	var submit submitResult
	if err := json.Unmarshal(submitOut.Bytes(), &submit); err != nil {
		t.Fatalf("Unmarshal submit: %v", err)
	}

	var waitOut bytes.Buffer
	var waitErr bytes.Buffer
	if err := runCLI([]string{"wait", "--repo", repoRoot, "--submission", strconv.FormatInt(submit.SubmissionID, 10), "--for", "integrated", "--json"}, newStepPrinter(&waitOut), &waitErr); err != nil {
		t.Fatalf("runCLI wait returned error: %v", err)
	}

	var result submissionWaitResult
	if err := json.Unmarshal(waitOut.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal wait: %v", err)
	}
	if result.Outcome != waitOutcome("integrated") || result.SubmissionStatus != "succeeded" {
		t.Fatalf("expected integrated wait outcome, got %+v", result)
	}
	if result.PublishRequestID != 0 || result.PublishStatus != "" {
		t.Fatalf("expected no publish correlation for integrated-only wait, got %+v", result)
	}
	if result.QueueState != "idle" || result.QueueLength != 0 || result.HasQueuedWork || result.HasRunningPublishes || result.HasRunningSubmissions || result.HasBlockedSubmissions {
		t.Fatalf("expected idle queue summary after integrated wait, got %+v", result)
	}
}

func TestWaitBySubmissionIDReturnsLandedOutcome(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	updatePublishMode(t, repoRoot, "auto")

	featurePath := filepath.Join(t.TempDir(), "feature-wait-landed")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/wait-landed", featurePath)
	writeFileAndCommit(t, featurePath, "wait.txt", "wait\n", "wait feature")

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	if err := runSubmit([]string{"--repo", featurePath, "--json"}, newStepPrinter(&submitOut), &submitErr); err != nil {
		t.Fatalf("runSubmit returned error: %v", err)
	}
	var submit submitResult
	if err := json.Unmarshal(submitOut.Bytes(), &submit); err != nil {
		t.Fatalf("Unmarshal submit: %v", err)
	}

	var waitOut bytes.Buffer
	var waitErr bytes.Buffer
	if err := runCLI([]string{"wait", "--repo", repoRoot, "--submission", strconv.FormatInt(submit.SubmissionID, 10), "--for", "landed", "--json"}, newStepPrinter(&waitOut), &waitErr); err != nil {
		t.Fatalf("runCLI wait returned error: %v", err)
	}

	var result submissionWaitResult
	if err := json.Unmarshal(waitOut.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal wait: %v", err)
	}
	if result.Outcome != waitOutcome("landed") || result.SubmissionStatus != "succeeded" || result.PublishStatus != "succeeded" || result.PublishRequestID == 0 {
		t.Fatalf("expected landed wait outcome with publish correlation, got %+v", result)
	}
	if result.QueueState != "idle" || result.QueueLength != 0 || result.HasQueuedWork || result.HasRunningPublishes || result.HasRunningSubmissions || result.HasBlockedSubmissions {
		t.Fatalf("expected idle queue summary after landed wait, got %+v", result)
	}
}

func TestWaitBySubmissionIDReturnsFailedWhenCorrelatedPublishFails(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	updatePublishMode(t, repoRoot, "auto")

	gateFlag := filepath.Join(t.TempDir(), "block-publish")
	hookPath := filepath.Join(hooksDirForRepo(t, repoRoot), "pre-push")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\nif [ ! -f "+gateFlag+" ]; then\n  echo gate failed >&2\n  exit 9\nfi\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	featurePath := filepath.Join(t.TempDir(), "feature-wait-publish-failed")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/wait-publish-failed", featurePath)
	writeFileAndCommit(t, featurePath, "wait.txt", "wait\n", "wait feature")

	var submitOut bytes.Buffer
	var submitErr bytes.Buffer
	if err := runSubmit([]string{"--repo", featurePath, "--json"}, newStepPrinter(&submitOut), &submitErr); err != nil {
		t.Fatalf("runSubmit returned error: %v", err)
	}
	var submit submitResult
	if err := json.Unmarshal(submitOut.Bytes(), &submit); err != nil {
		t.Fatalf("Unmarshal submit: %v", err)
	}

	var waitOut bytes.Buffer
	var waitErr bytes.Buffer
	err := runCLI([]string{"wait", "--repo", repoRoot, "--submission", strconv.FormatInt(submit.SubmissionID, 10), "--for", "landed", "--json", "--timeout", "10s"}, newStepPrinter(&waitOut), &waitErr)
	if err == nil {
		t.Fatalf("expected landed wait to fail when publish fails")
	}
	var exitErr *cliExitError
	if !errors.As(err, &exitErr) || CLIExitCode(err) != 1 {
		t.Fatalf("expected exit code 1, got %v", err)
	}

	var result submissionWaitResult
	if err := json.Unmarshal(waitOut.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal wait: %v", err)
	}
	if result.Outcome != waitOutcomeFailed || result.PublishStatus != "failed" || result.PublishRequestID == 0 {
		t.Fatalf("expected failed landed wait with failed publish correlation, got %+v", result)
	}
	if result.QueueState == "" {
		t.Fatalf("expected explicit queue state on failed wait, got %+v", result)
	}
}

func TestWaitAndStatusClassifyFailedPublishWithDirtyProtectedRoot(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	updatePublishMode(t, repoRoot, "auto")
	t.Setenv("MAINLINE_DISABLE_MUTATION_DRAIN", "1")

	featurePath := filepath.Join(t.TempDir(), "feature-wait-dirty-root")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/wait-dirty-root", featurePath)
	writeFileAndCommit(t, featurePath, "wait.txt", "wait\n", "wait feature")
	submitBranch(t, featurePath)
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
	requests, err := store.ListPublishRequests(context.Background(), repoRecord.ID)
	if err != nil {
		t.Fatalf("ListPublishRequests: %v", err)
	}
	if len(submissions) != 1 || len(requests) != 1 {
		t.Fatalf("expected one submission and one publish request, got %+v %+v", submissions, requests)
	}
	if _, err := store.UpdatePublishRequestStatus(context.Background(), requests[0].ID, domain.PublishStatusFailed, sql.NullInt64{}); err != nil {
		t.Fatalf("UpdatePublishRequestStatus: %v", err)
	}
	if err := appendStateEvent(context.Background(), store, state.EventRecord{
		RepoID:    repoRecord.ID,
		ItemType:  domain.ItemTypePublishRequest,
		ItemID:    state.NullInt64(requests[0].ID),
		EventType: domain.EventTypePublishFailed,
		Payload: mustJSON(map[string]string{
			"target_sha": requests[0].TargetSHA,
			"error":      "git push was rejected: hook failed",
		}),
	}); err != nil {
		t.Fatalf("appendStateEvent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "dirty-root.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("WriteFile dirty-root.txt: %v", err)
	}

	var waitOut bytes.Buffer
	var waitErr bytes.Buffer
	err = runCLI([]string{"wait", "--repo", repoRoot, "--submission", strconv.FormatInt(submissions[0].ID, 10), "--for", "landed", "--json", "--timeout", "1s"}, newStepPrinter(&waitOut), &waitErr)
	if err == nil {
		t.Fatalf("expected landed wait to fail")
	}

	var waitResult submissionWaitResult
	if err := json.Unmarshal(waitOut.Bytes(), &waitResult); err != nil {
		t.Fatalf("Unmarshal wait: %v", err)
	}
	if waitResult.PublishFailureCause != "protected_root_dirty" {
		t.Fatalf("expected protected_root_dirty cause, got %+v", waitResult)
	}
	if waitResult.ResubmitRequired {
		t.Fatalf("expected resubmit_required=false, got %+v", waitResult)
	}
	if !strings.Contains(waitResult.Error, "protected root checkout is dirty") {
		t.Fatalf("expected dirty-root error, got %+v", waitResult)
	}

	var statusOut bytes.Buffer
	var statusErr bytes.Buffer
	if err := runCLI([]string{"status", "--repo", repoRoot, "--json"}, newStepPrinter(&statusOut), &statusErr); err != nil {
		t.Fatalf("runCLI status returned error: %v", err)
	}

	var statusResult statusResult
	if err := json.Unmarshal(statusOut.Bytes(), &statusResult); err != nil {
		t.Fatalf("Unmarshal status: %v", err)
	}
	if statusResult.LatestSubmission == nil || statusResult.LatestSubmission.PublishFailureCause != "protected_root_dirty" {
		t.Fatalf("expected status publish failure classification, got %+v", statusResult.LatestSubmission)
	}
}

func TestStatusJSONCorrelatesSubmissionToPublish(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	updatePublishMode(t, repoRoot, "auto")

	featurePath := filepath.Join(t.TempDir(), "feature-status-landed")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/status-landed", featurePath)
	writeFileAndCommit(t, featurePath, "status.txt", "status\n", "status feature")
	submitBranch(t, featurePath)

	runOnce(t, repoRoot)
	runOnce(t, repoRoot)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLI([]string{"status", "--repo", repoRoot, "--json"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	var result statusResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result.LatestSubmission == nil {
		t.Fatalf("expected latest submission, got %+v", result)
	}
	if result.LatestSubmission.PublishRequestID == 0 || result.LatestSubmission.PublishStatus != "succeeded" || result.LatestSubmission.Outcome != submissionOutcomeLanded {
		t.Fatalf("expected landed submission correlation, got %+v", result.LatestSubmission)
	}
}

func TestStatusJSONReportsPublishExecutionAndProtectedWorktreeActivity(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	cfg, _, err := policy.LoadOrDefault(repoRoot)
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	cfg.Repo.HookPolicy = "replace-with-mainline-checks"
	cfg.Checks.PreparePublish = []string{"pnpm install --frozen-lockfile"}
	cfg.Checks.ValidatePublish = []string{"pnpm test --filter smoke"}
	if err := policy.SaveFile(repoRoot, cfg); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}
	runTestCommand(t, repoRoot, "git", "add", "mainline.toml")
	runTestCommand(t, repoRoot, "git", "commit", "-m", "configure publish stages")

	layout, err := git.DiscoverRepositoryLayout(repoRoot)
	if err != nil {
		t.Fatalf("DiscoverRepositoryLayout: %v", err)
	}
	lockDir := filepath.Join(layout.GitDir, "mainline", "locks")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(lockDir): %v", err)
	}
	payload, err := json.Marshal(state.LeaseMetadata{
		Domain:    state.PublishLock,
		RepoRoot:  layout.RepositoryRoot,
		Owner:     "publish-worker",
		Stage:     publishStageValidate,
		RequestID: 88,
		PID:       5150,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lockDir, state.PublishLock+".lock.json"), payload, 0o644); err != nil {
		t.Fatalf("WriteFile(lock): %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLI([]string{"status", "--repo", repoRoot, "--json"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	var result statusResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !result.PublishExecution.HooksBypassedForPush || result.PublishExecution.EffectiveHookPolicy != "replace-with-mainline-checks" {
		t.Fatalf("expected publish execution policy in status, got %+v", result.PublishExecution)
	}
	if !result.PublishExecution.PreparePublishEnabled || !result.PublishExecution.ValidatePublishEnabled {
		t.Fatalf("expected prepare/validate enabled, got %+v", result.PublishExecution)
	}
	if result.PublishWorker == nil || result.PublishWorker.Stage != publishStageValidate {
		t.Fatalf("expected publish worker stage, got %+v", result.PublishWorker)
	}
	if result.ProtectedWorktreeActivity == nil || result.ProtectedWorktreeActivity.Stage != publishStageValidate {
		t.Fatalf("expected protected worktree activity stage, got %+v", result.ProtectedWorktreeActivity)
	}
}

func TestEventsLifecycleJSONContractContainsStableFields(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	featurePath := filepath.Join(t.TempDir(), "feature-events-contract")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/events-contract", featurePath)
	writeFileAndCommit(t, featurePath, "events.txt", "events\n", "events feature")
	submitBranch(t, featurePath)
	runOnce(t, repoRoot)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLI([]string{"events", "--repo", repoRoot, "--json", "--lifecycle", "--limit", "20"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) == 0 {
		t.Fatalf("expected lifecycle json lines")
	}

	foundIntegrated := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			t.Fatalf("Unmarshal line %q: %v", line, err)
		}
		if payload["event"] == "integrated" {
			foundIntegrated = true
			wantKeys := []string{"branch", "event", "repository_root", "sha", "status", "submission_id", "timestamp"}
			if gotKeys := sortedJSONKeys(payload); !slices.Equal(gotKeys, wantKeys) {
				t.Fatalf("expected integrated lifecycle keys %v, got %v", wantKeys, gotKeys)
			}
		}
	}
	if !foundIntegrated {
		t.Fatalf("expected integrated lifecycle event in %q", stdout.String())
	}
}

func TestEventsJSONContractContainsStableFields(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	queuePublish(t, repoRoot)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLI([]string{"events", "--repo", repoRoot, "--json", "--limit", "5"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) == 0 {
		t.Fatalf("expected event json lines")
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			t.Fatalf("Unmarshal line %q: %v", line, err)
		}
		wantKeys := []string{"created_at", "event_type", "id", "item_id", "item_type", "payload", "repo_id"}
		if gotKeys := sortedJSONKeys(payload); !slices.Equal(gotKeys, wantKeys) {
			t.Fatalf("expected raw event json keys %v, got %v", wantKeys, gotKeys)
		}
		break
	}
}

func TestDaemonJSONLogContractContainsStableFields(t *testing.T) {
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
		t.Fatalf("expected daemon json log lines, got %q", stdout.String())
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("Unmarshal first line %q: %v", lines[0], err)
	}
	if gotKeys := sortedJSONKeys(first); !slices.Equal(gotKeys, []string{"event", "level", "message", "repo", "timestamp"}) {
		t.Fatalf("unexpected daemon started json keys: %v", gotKeys)
	}

	var last map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatalf("Unmarshal last line %q: %v", lines[len(lines)-1], err)
	}
	if gotKeys := sortedJSONKeys(last); !slices.Equal(gotKeys, []string{"cycle", "event", "level", "message", "repo", "timestamp"}) {
		t.Fatalf("unexpected daemon terminal json keys: %v", gotKeys)
	}
}

func TestWatchJSONContractContainsStableFields(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	var stdout bytes.Buffer
	if err := runWatchLoop(context.Background(), watchOptions{
		repoPath:   repoRoot,
		interval:   10 * time.Millisecond,
		eventLimit: 1,
		maxCycles:  1,
		asJSON:     true,
	}, newStepPrinter(&stdout)); err != nil {
		t.Fatalf("runWatchLoop returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if gotKeys := sortedJSONKeys(payload); !slices.Equal(gotKeys, []string{"observed_at", "status"}) {
		t.Fatalf("unexpected watch json keys: %v", gotKeys)
	}
}

func TestDaemonProcessesWorkFromBareCloneLinkedWorktree(t *testing.T) {
	bareDir, worktreePath := createBareCloneWorktree(t)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", worktreePath}, newStepPrinter(&initOut), &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}
	runTestCommand(t, worktreePath, "git", "add", "mainline.toml")
	runTestCommand(t, worktreePath, "git", "commit", "-m", "init mainline")
	featurePath := filepath.Join(t.TempDir(), "bare-feature")
	runTestCommand(t, worktreePath, "git", "worktree", "add", "-b", "feature/bare-daemon", featurePath)
	writeFileAndCommit(t, featurePath, "bare.txt", "bare\n", "bare feature")
	submitBranch(t, featurePath)

	var stdout bytes.Buffer
	opts := daemonOptions{
		repoPath:  worktreePath,
		interval:  time.Millisecond,
		maxCycles: 1,
	}
	if err := runDaemonLoop(context.Background(), opts, &stdout); err != nil {
		t.Fatalf("runDaemonLoop returned error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(worktreePath, "bare.txt")); err != nil {
		t.Fatalf("expected bare.txt after daemon integration: %v", err)
	}

	layout, err := git.DiscoverRepositoryLayout(worktreePath)
	if err != nil {
		t.Fatalf("DiscoverRepositoryLayout: %v", err)
	}
	wantBareDir, err := filepath.EvalSymlinks(bareDir)
	if err != nil {
		t.Fatalf("EvalSymlinks(bareDir): %v", err)
	}
	if layout.RepositoryRoot != wantBareDir {
		t.Fatalf("expected bare repository root %q, got %q", wantBareDir, layout.RepositoryRoot)
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

func TestGlobalDaemonProcessesRegisteredRepos(t *testing.T) {
	registryPath := filepath.Join(t.TempDir(), "registry.json")
	t.Setenv("MAINLINE_REGISTRY_PATH", registryPath)
	t.Setenv("MAINLINE_DISABLE_SUBMIT_DRAIN", "1")

	repoOne, remoteOne := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoOne)
	updatePublishMode(t, repoOne, "auto")

	repoTwo, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoTwo)
	updatePublishMode(t, repoTwo, "auto")

	featurePath := filepath.Join(t.TempDir(), "feature-global-daemon")
	runTestCommand(t, repoOne, "git", "worktree", "add", "-b", "feature/global-daemon", featurePath)
	writeFileAndCommit(t, featurePath, "global.txt", "global\n", "feature global daemon")
	submitBranch(t, featurePath)

	var stdout bytes.Buffer
	opts := daemonOptions{
		allRepos:     true,
		registryPath: registryPath,
		interval:     time.Millisecond,
		maxCycles:    1,
		jsonLogs:     true,
	}
	if err := runDaemonLoop(context.Background(), opts, &stdout); err != nil {
		t.Fatalf("runDaemonLoop returned error: %v", err)
	}

	localHead := trimNewline(runTestCommand(t, repoOne, "git", "rev-parse", "HEAD"))
	remoteHead := trimNewline(runTestCommand(t, remoteOne, "git", "rev-parse", "refs/heads/main"))
	if remoteHead != localHead {
		t.Fatalf("expected remote head %q, got %q", localHead, remoteHead)
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) == 0 {
		t.Fatalf("expected daemon logs, got %q", stdout.String())
	}
	foundRepoOne := false
	for _, line := range lines {
		var record daemonLog
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if record.Repo == canonicalRegistryPath(repoOne) {
			foundRepoOne = true
			break
		}
	}
	if !foundRepoOne {
		t.Fatalf("expected global daemon logs to include repo %q, got %q", repoOne, stdout.String())
	}
}

func TestGlobalDaemonSkipsDirtyRootRepoAndDrainsHealthyRepo(t *testing.T) {
	registryPath := filepath.Join(t.TempDir(), "registry.json")
	t.Setenv("MAINLINE_REGISTRY_PATH", registryPath)
	t.Setenv("MAINLINE_DISABLE_SUBMIT_DRAIN", "1")

	dirtyRepo, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, dirtyRepo)
	updatePublishMode(t, dirtyRepo, "auto")
	if err := os.WriteFile(filepath.Join(dirtyRepo, "DIRTY.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(DIRTY.txt): %v", err)
	}

	healthyRepo, healthyRemote := createTestRepoWithRemote(t)
	initRepoForWorker(t, healthyRepo)
	updatePublishMode(t, healthyRepo, "auto")

	featurePath := filepath.Join(t.TempDir(), "feature-global-daemon-healthy")
	runTestCommand(t, healthyRepo, "git", "worktree", "add", "-b", "feature/global-daemon-healthy", featurePath)
	writeFileAndCommit(t, featurePath, "global-healthy.txt", "global healthy\n", "feature global daemon healthy")
	submitBranch(t, featurePath)

	var stdout bytes.Buffer
	opts := daemonOptions{
		allRepos:     true,
		registryPath: registryPath,
		interval:     time.Millisecond,
		maxCycles:    1,
		jsonLogs:     true,
	}
	if err := runDaemonLoop(context.Background(), opts, &stdout); err != nil {
		t.Fatalf("runDaemonLoop returned error: %v", err)
	}

	healthyLocalHead := trimNewline(runTestCommand(t, healthyRepo, "git", "rev-parse", "HEAD"))
	healthyRemoteHead := trimNewline(runTestCommand(t, healthyRemote, "git", "rev-parse", "refs/heads/main"))
	if healthyRemoteHead != healthyLocalHead {
		t.Fatalf("expected healthy remote head %q, got %q", healthyLocalHead, healthyRemoteHead)
	}

	dirtyRemoteHead := trimNewline(runTestCommand(t, dirtyRepo, "git", "rev-parse", "HEAD"))
	if strings.Contains(stdout.String(), dirtyRemoteHead) {
		t.Fatalf("unexpected dirty repo head leakage in daemon output: %q", stdout.String())
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	var sawDirtyFailure bool
	var sawHealthyWork bool
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record daemonLog
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if record.Repo == canonicalRegistryPath(dirtyRepo) && record.Event == "cycle.failed" && strings.Contains(record.Message, "dirty") {
			sawDirtyFailure = true
		}
		if record.Repo == canonicalRegistryPath(healthyRepo) && record.Event == "cycle.completed" && strings.Contains(record.Message, "Published request") {
			sawHealthyWork = true
		}
	}
	if !sawDirtyFailure {
		t.Fatalf("expected dirty repo failure log, got %q", stdout.String())
	}
	if !sawHealthyWork {
		t.Fatalf("expected healthy repo publish log, got %q", stdout.String())
	}
}

func TestGlobalDaemonIdleExitDoesNotTreatBusyRepoAsIdle(t *testing.T) {
	registryPath := filepath.Join(t.TempDir(), "registry.json")
	t.Setenv("MAINLINE_REGISTRY_PATH", registryPath)

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
		allRepos:     true,
		registryPath: registryPath,
		interval:     time.Millisecond,
		maxCycles:    1,
		jsonLogs:     true,
		idleExit:     true,
	}
	if err := runDaemonLoop(context.Background(), opts, &stdout); err != nil {
		t.Fatalf("runDaemonLoop returned error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected daemon logs, got %q", stdout.String())
	}

	var sawBusyCycle bool
	var last daemonLog
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if err := json.Unmarshal([]byte(line), &last); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if last.Repo == canonicalRegistryPath(repoRoot) && last.Event == "cycle.completed" && strings.Contains(last.Message, "Publish worker busy.") {
			sawBusyCycle = true
		}
	}
	if !sawBusyCycle {
		t.Fatalf("expected busy repo cycle log, got %q", stdout.String())
	}
	if last.Event != "daemon.max_cycles_reached" {
		t.Fatalf("expected daemon.max_cycles_reached, got %+v", last)
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

	if err := runCLI([]string{"completion", "bash"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "complete -F _mainline_completions mainline") {
		t.Fatalf("expected completion script output, got %q", output)
	}
	if !strings.Contains(output, "land submit status confidence run-once wait rebase blocked retry cancel publish") {
		t.Fatalf("expected completion script to include rebase and blocked workflow, got %q", output)
	}
	if !strings.Contains(output, "--repo --branch --sha --worktree --requested-by --priority --allow-newer-head --json --check --check-only --queue-only --wait --for --timeout --poll-interval") {
		t.Fatalf("expected submit completion flags, got %q", output)
	}
	if !strings.Contains(output, "retry cancel publish") {
		t.Fatalf("expected completion script to include retry and cancel, got %q", output)
	}
	if !strings.Contains(output, "publish logs watch events doctor completion version config") {
		t.Fatalf("expected completion script to include config surface, got %q", output)
	}
	if !strings.Contains(output, "compgen -W \"init show audit root\"") {
		t.Fatalf("expected repo root completion to be present, got %q", output)
	}
	if !strings.Contains(output, "compgen -W \"prune\"") {
		t.Fatalf("expected registry prune completion to be present, got %q", output)
	}
	if strings.Contains(output, "run-once|publish|doctor") {
		t.Fatalf("expected split completion cases for real command flags, got %q", output)
	}
	if strings.Contains(output, "run-once|publish)\n      COMPREPLY=( $(compgen -W \"--repo --json\" -- \"$cur\") )") {
		t.Fatalf("expected bash completion to avoid generic unsupported json flag suggestions, got %q", output)
	}
	if !strings.Contains(output, "doctor)\n      COMPREPLY=( $(compgen -W \"--repo --json --fix\" -- \"$cur\") )") {
		t.Fatalf("expected doctor completion to include --fix, got %q", output)
	}
	if !strings.Contains(output, "root)\n          COMPREPLY=( $(compgen -W \"--repo --json --adopt-root\" -- \"$cur\") )") {
		t.Fatalf("expected repo root completion to include --adopt-root, got %q", output)
	}
	if !strings.Contains(output, "wait)\n      COMPREPLY=( $(compgen -W \"--repo --submission --for --json --timeout --poll-interval\" -- \"$cur\") )") {
		t.Fatalf("expected wait completion to include submission-id flags, got %q", output)
	}

	stdout.Reset()
	stderr.Reset()
	if err := runCLI([]string{"completion", "fish"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	output = stdout.String()
	if !strings.Contains(output, "__fish_seen_subcommand_from logs events\" -l json") {
		t.Fatalf("expected fish completion to include logs/events --json, got %q", output)
	}
	if !strings.Contains(output, "__fish_seen_subcommand_from logs events\" -l lifecycle") {
		t.Fatalf("expected fish completion to include logs/events --lifecycle, got %q", output)
	}
	if !strings.Contains(output, "__fish_seen_subcommand_from watch\" -l interval") {
		t.Fatalf("expected fish completion to include watch flags, got %q", output)
	}
	if !strings.Contains(output, "__fish_seen_subcommand_from confidence\" -l cert-report") {
		t.Fatalf("expected fish completion to include confidence flags, got %q", output)
	}
	if !strings.Contains(output, "__fish_seen_subcommand_from doctor\" -l fix") {
		t.Fatalf("expected fish completion to include doctor --fix, got %q", output)
	}
	if !strings.Contains(output, "__fish_seen_subcommand_from land\" -l timeout") {
		t.Fatalf("expected fish completion to include land flags, got %q", output)
	}
	if !strings.Contains(output, "__fish_seen_subcommand_from land\" -l sha") {
		t.Fatalf("expected fish completion to include land sha flag, got %q", output)
	}
	if !strings.Contains(output, "__fish_seen_subcommand_from land\" -l allow-newer-head") {
		t.Fatalf("expected fish completion to include land allow-newer-head flag, got %q", output)
	}
	if !strings.Contains(output, "__fish_seen_subcommand_from wait\" -l submission") {
		t.Fatalf("expected fish completion to include wait submission flag, got %q", output)
	}
	if !strings.Contains(output, "__fish_seen_subcommand_from wait\" -l for") {
		t.Fatalf("expected fish completion to include wait target flag, got %q", output)
	}
	if !strings.Contains(output, "__fish_seen_subcommand_from wait\" -l timeout") {
		t.Fatalf("expected fish completion to include wait timeout flag, got %q", output)
	}
	if !strings.Contains(output, "__fish_seen_subcommand_from submit\" -l sha") {
		t.Fatalf("expected fish completion to include submit sha flag, got %q", output)
	}
	if !strings.Contains(output, "__fish_seen_subcommand_from submit\" -l allow-newer-head") {
		t.Fatalf("expected fish completion to include submit allow-newer-head flag, got %q", output)
	}
	if !strings.Contains(output, "__fish_seen_subcommand_from submit\" -l check") {
		t.Fatalf("expected fish completion to include submit check flag, got %q", output)
	}
	if !strings.Contains(output, "__fish_seen_subcommand_from submit\" -l check-only") {
		t.Fatalf("expected fish completion to include submit check-only flag, got %q", output)
	}
	if !strings.Contains(output, "__fish_seen_subcommand_from submit\" -l priority") {
		t.Fatalf("expected fish completion to include submit priority flag, got %q", output)
	}
	if !strings.Contains(output, "__fish_seen_subcommand_from submit\" -l wait") {
		t.Fatalf("expected fish completion to include submit wait flag, got %q", output)
	}
	if !strings.Contains(output, "__fish_seen_subcommand_from submit\" -l for") {
		t.Fatalf("expected fish completion to include submit for flag, got %q", output)
	}
	if !strings.Contains(output, "__fish_seen_subcommand_from submit\" -l timeout") {
		t.Fatalf("expected fish completion to include submit timeout flag, got %q", output)
	}
	if !strings.Contains(output, "__fish_seen_subcommand_from config edit\" -l editor") {
		t.Fatalf("expected fish completion to include config edit flags, got %q", output)
	}
	if !strings.Contains(output, "__fish_seen_subcommand_from repo; and not __fish_seen_subcommand_from init show audit root") {
		t.Fatalf("expected fish completion to include repo root subcommand, got %q", output)
	}
	if !strings.Contains(output, "__fish_seen_subcommand_from registry; and not __fish_seen_subcommand_from prune") {
		t.Fatalf("expected fish completion to include registry prune subcommand, got %q", output)
	}
	if !strings.Contains(output, "__fish_seen_subcommand_from repo root\" -l adopt-root") {
		t.Fatalf("expected fish completion to include repo root adopt flag, got %q", output)
	}
}

func TestConfidenceJSONReportsPromotionReadyForCurrentEvidence(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	runTestCommand(t, repoRoot, "git", "push", "origin", "main")

	head := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	soakPath := filepath.Join(t.TempDir(), "summary.json")
	certPath := filepath.Join(t.TempDir(), "latest-report.json")
	writeJSONFile(t, soakPath, map[string]any{
		"mainline_commit": head,
		"generated_at":    "2026-03-31T21:30:00Z",
		"runs":            5,
		"passed_runs":     5,
		"failed_runs":     0,
		"flake_rate":      0.0,
	})
	writeJSONFile(t, certPath, map[string]any{
		"mainline_commit": head,
		"generated_at":    "2026-03-31T21:30:00Z",
		"result":          "passed",
		"repos": []map[string]any{
			{"id": "dogfood", "result": "passed"},
			{"id": "bare", "result": "passed"},
		},
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLI([]string{"confidence", "--repo", repoRoot, "--json", "--soak-summary", soakPath, "--cert-report", certPath}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	var report confidenceResult
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !report.PromotionReady {
		t.Fatalf("expected promotion ready report, got %+v", report)
	}
	if report.CurrentCommit != head {
		t.Fatalf("expected current commit %q, got %q", head, report.CurrentCommit)
	}
	if len(report.Gates) == 0 {
		t.Fatalf("expected gates, got none")
	}
}

func TestConfidenceFailsOnMismatchedEvidenceCommit(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	runTestCommand(t, repoRoot, "git", "push", "origin", "main")

	soakPath := filepath.Join(t.TempDir(), "summary.json")
	certPath := filepath.Join(t.TempDir(), "latest-report.json")
	writeJSONFile(t, soakPath, map[string]any{
		"mainline_commit": "deadbeef",
		"generated_at":    "2026-03-31T21:30:00Z",
		"runs":            5,
		"passed_runs":     5,
		"failed_runs":     0,
		"flake_rate":      0.0,
	})
	writeJSONFile(t, certPath, map[string]any{
		"mainline_commit": "deadbeef",
		"generated_at":    "2026-03-31T21:30:00Z",
		"result":          "passed",
		"repos": []map[string]any{
			{"id": "dogfood", "result": "passed"},
		},
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLI([]string{"confidence", "--repo", repoRoot, "--json", "--soak-summary", soakPath, "--cert-report", certPath}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	var report confidenceResult
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if report.PromotionReady {
		t.Fatalf("expected promotion gate failure, got %+v", report)
	}
	foundMismatch := false
	for _, gate := range report.Gates {
		if (gate.Name == "soak_current" || gate.Name == "certification_current") && !gate.Passed {
			foundMismatch = true
		}
	}
	if !foundMismatch {
		t.Fatalf("expected evidence commit mismatch gates, got %+v", report.Gates)
	}
}

func TestConfidenceUsesCurrentWorktreeHeadForBuildIdentity(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	runTestCommand(t, repoRoot, "git", "push", "origin", "main")

	featurePath := filepath.Join(t.TempDir(), "feature-confidence")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/confidence", featurePath)
	writeFileAndCommit(t, featurePath, "confidence.txt", "confidence\n", "feature confidence")

	featureHead := trimNewline(runTestCommand(t, featurePath, "git", "rev-parse", "HEAD"))
	mainHead := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	if featureHead == mainHead {
		t.Fatalf("expected feature head to differ from main head")
	}

	soakPath := filepath.Join(t.TempDir(), "summary.json")
	certPath := filepath.Join(t.TempDir(), "latest-report.json")
	writeJSONFile(t, soakPath, map[string]any{
		"mainline_commit": featureHead,
		"generated_at":    "2026-03-31T21:30:00Z",
		"runs":            1,
		"passed_runs":     1,
		"failed_runs":     0,
		"flake_rate":      0.0,
	})
	writeJSONFile(t, certPath, map[string]any{
		"mainline_commit": featureHead,
		"generated_at":    "2026-03-31T21:30:00Z",
		"result":          "passed",
		"repos": []map[string]any{
			{"id": "dogfood", "result": "passed"},
		},
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLI([]string{"confidence", "--repo", featurePath, "--json", "--soak-summary", soakPath, "--cert-report", certPath}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	var report confidenceResult
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if report.CurrentCommit != featureHead {
		t.Fatalf("expected current commit %q, got %q", featureHead, report.CurrentCommit)
	}
}

func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
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
	if err := runCLI([]string{"status", "--repo", repoRoot, "--json", "--events", "2"}, newStepPrinter(&stdout), &stderr); err != nil {
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

func TestStatusUsesDurableRepoRecordWhenConfigDrifts(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	cfg, err := policy.LoadFile(repoRoot)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	cfg.Repo.ProtectedBranch = "stale-branch"
	cfg.Repo.RemoteName = "stale-remote"
	cfg.Repo.MainWorktree = filepath.Join(repoRoot, "stale-worktree")
	if err := policy.SaveFile(repoRoot, cfg); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLI([]string{"status", "--repo", repoRoot, "--json"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	var status statusResult
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if status.ProtectedBranch != "main" {
		t.Fatalf("expected durable protected branch from sqlite, got %+v", status)
	}
}

func TestStatusUpgradesExistingLegacyStateSchema(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	layout, err := git.DiscoverRepositoryLayout(repoRoot)
	if err != nil {
		t.Fatalf("DiscoverRepositoryLayout: %v", err)
	}
	statePath := state.DefaultPath(layout.GitDir)

	db, err := sql.Open("sqlite", statePath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 0;`); err != nil {
		t.Fatalf("reset user_version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLI([]string{"status", "--repo", repoRoot, "--json"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	db, err = sql.Open("sqlite", statePath)
	if err != nil {
		t.Fatalf("sql.Open(second): %v", err)
	}
	defer db.Close()

	var version int
	if err := db.QueryRow(`
		SELECT MAX(version_id)
		FROM goose_db_version
		WHERE is_applied = 1
	`).Scan(&version); err != nil {
		t.Fatalf("read goose version: %v", err)
	}
	if version != state.CurrentSchemaVersionForTest() {
		t.Fatalf("expected schema version %d after status upgrade, got %d", state.CurrentSchemaVersionForTest(), version)
	}
}

func TestStatusHumanOutputIncludesRecentSummary(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	queuePublish(t, repoRoot)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLI([]string{"status", "--repo", repoRoot}, newStepPrinter(&stdout), &stderr); err != nil {
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
	if err := runCLI([]string{"events", "--repo", repoRoot, "--json", "--limit", "3"}, newStepPrinter(&stdout), &stderr); err != nil {
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

func TestEventsLifecycleJSONProjectsBranchTransitions(t *testing.T) {
	repoRoot, remoteDir := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	featurePath := filepath.Join(t.TempDir(), "feature-lifecycle")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/lifecycle", featurePath)
	writeFileAndCommit(t, featurePath, "lifecycle.txt", "lifecycle\n", "feature lifecycle")
	submitBranch(t, featurePath)
	runOnce(t, repoRoot)
	queuePublish(t, repoRoot)
	runOnce(t, repoRoot)

	protectedSHA := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	remoteSHA := trimNewline(runTestCommand(t, remoteDir, "git", "rev-parse", "refs/heads/main"))
	if remoteSHA != protectedSHA {
		t.Fatalf("expected remote head %q, got %q", protectedSHA, remoteSHA)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLI([]string{"events", "--repo", repoRoot, "--json", "--lifecycle", "--limit", "20"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) == 0 {
		t.Fatalf("expected lifecycle events, got none")
	}

	foundSubmitted := false
	foundIntegrated := false
	foundPublished := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event lifecycleEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("Unmarshal lifecycle event: %v", err)
		}
		switch event.Event {
		case "submitted":
			if event.Branch == "feature/lifecycle" {
				foundSubmitted = true
			}
		case "integrated":
			if event.Branch == "feature/lifecycle" && event.SHA == protectedSHA {
				foundIntegrated = true
			}
		case "published":
			if event.Branch == "feature/lifecycle" && event.SHA == protectedSHA {
				foundPublished = true
			}
		}
	}
	if !foundSubmitted || !foundIntegrated || !foundPublished {
		t.Fatalf("expected submitted/integrated/published lifecycle events, got %s", stdout.String())
	}
}

func TestEventsLifecycleReplayKeepsBranchOnPublishWindow(t *testing.T) {
	repoRoot, remoteDir := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	updatePublishMode(t, repoRoot, "auto")

	featurePath := filepath.Join(t.TempDir(), "feature-lifecycle-window")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/lifecycle-window", featurePath)
	writeFileAndCommit(t, featurePath, "window.txt", "window\n", "feature lifecycle window")
	submitBranch(t, featurePath)
	runOnce(t, repoRoot)
	runOnce(t, repoRoot)

	protectedSHA := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	remoteSHA := trimNewline(runTestCommand(t, remoteDir, "git", "rev-parse", "refs/heads/main"))
	if remoteSHA != protectedSHA {
		t.Fatalf("expected remote head %q, got %q", protectedSHA, remoteSHA)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLI([]string{"events", "--repo", repoRoot, "--json", "--lifecycle", "--limit", "3"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lifecycle events, got %d: %q", len(lines), stdout.String())
	}

	foundPublished := false
	for _, line := range lines {
		var event lifecycleEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("Unmarshal lifecycle event: %v", err)
		}
		if event.Event == "published" {
			foundPublished = true
			if event.Branch != "feature/lifecycle-window" || event.SHA != protectedSHA {
				t.Fatalf("expected published branch + sha in narrow replay window, got %+v", event)
			}
		}
	}
	if !foundPublished {
		t.Fatalf("expected published lifecycle event in %q", stdout.String())
	}
}

func TestEventsLifecycleFailedDetachedSubmissionKeepsSourceRef(t *testing.T) {
	t.Setenv("MAINLINE_DISABLE_SUBMIT_DRAIN", "1")

	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	detachedPath := filepath.Join(t.TempDir(), "feature-detached-failure")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "--detach", detachedPath)
	writeFileAndCommit(t, detachedPath, "detached-failure.txt", "detached failure\n", "feature detached failure")
	runTestCommand(t, detachedPath, "git", "checkout", "--detach", "HEAD")
	detachedSHA := trimNewline(runTestCommand(t, detachedPath, "git", "rev-parse", "HEAD"))

	var submitStdout bytes.Buffer
	var submitStderr bytes.Buffer
	if err := runCLI([]string{"submit", "--repo", detachedPath, "--json"}, newStepPrinter(&submitStdout), &submitStderr); err != nil {
		t.Fatalf("submit runCLI returned error: %v", err)
	}

	restoreFaults := setAppTestFaultHooks(testFaultHooks{
		before: func(point string) error {
			if point == "integration.rebase" {
				return errors.New("synthetic rebase failure")
			}
			return nil
		},
	})
	defer restoreFaults()

	var runStdout bytes.Buffer
	var runStderr bytes.Buffer
	if err := runCLI([]string{"run-once", "--repo", repoRoot}, newStepPrinter(&runStdout), &runStderr); err != nil {
		t.Fatalf("run-once runCLI returned error: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLI([]string{"events", "--repo", repoRoot, "--json", "--lifecycle", "--limit", "20"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("events runCLI returned error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	foundFailed := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event lifecycleEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("Unmarshal lifecycle event: %v", err)
		}
		if event.Event == "failed" {
			foundFailed = true
			if event.Branch != detachedSHA {
				t.Fatalf("expected failed detached lifecycle branch %q, got %+v", detachedSHA, event)
			}
			if event.Error != "synthetic rebase failure" {
				t.Fatalf("expected failure reason in lifecycle event, got %+v", event)
			}
		}
	}
	if !foundFailed {
		t.Fatalf("expected failed lifecycle event in %q", stdout.String())
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
		}, newStepPrinter(&output))
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

func TestEventsFollowLifecycleStreamsIntegratedBranchEvent(t *testing.T) {
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
			asJSON:       true,
			lifecycle:    true,
			follow:       true,
			pollInterval: 20 * time.Millisecond,
		}, newStepPrinter(&output))
	}()

	featurePath := filepath.Join(t.TempDir(), "feature-follow-lifecycle")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/follow-lifecycle", featurePath)
	writeFileAndCommit(t, featurePath, "follow.txt", "follow\n", "feature follow")
	submitBranch(t, featurePath)
	runOnce(t, repoRoot)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		text := output.String()
		if strings.Contains(text, "\"event\":\"integrated\"") && strings.Contains(text, "\"branch\":\"feature/follow-lifecycle\"") {
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
	t.Fatalf("timed out waiting for integrated lifecycle event, got %q", output.String())
}

func TestLogsMatchesEventOutput(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)
	queuePublish(t, repoRoot)

	var eventStdout bytes.Buffer
	var logsStdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLI([]string{"events", "--repo", repoRoot, "--json", "--limit", "3"}, newStepPrinter(&eventStdout), &stderr); err != nil {
		t.Fatalf("events runCLI returned error: %v", err)
	}
	stderr.Reset()
	if err := runCLI([]string{"logs", "--repo", repoRoot, "--json", "--limit", "3"}, newStepPrinter(&logsStdout), &stderr); err != nil {
		t.Fatalf("logs runCLI returned error: %v", err)
	}
	if logsStdout.String() != eventStdout.String() {
		t.Fatalf("expected logs output to match events output\nlogs:\n%s\nevents:\n%s", logsStdout.String(), eventStdout.String())
	}
}

func TestLogsHelpUsesLogsCommandName(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runCLI([]string{"logs", "--help"}, newStepPrinter(&stdout), &stderr)
	if err != nil {
		t.Fatalf("expected help success for logs command, got %v", err)
	}
	output := stderr.String()
	if !strings.Contains(output, "mainline logs [flags]") {
		t.Fatalf("expected logs help usage, got %q", output)
	}
	if strings.Contains(output, "mainline events [flags]") {
		t.Fatalf("expected logs help to avoid events alias wording, got %q", output)
	}
}

func TestSubmitHelpMentionsAgentTurboPath(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runCLIWithName("mq", []string{"submit", "--help"}, newStepPrinter(&stdout), &stderr)
	if err != nil {
		t.Fatalf("expected help success for submit command, got %v", err)
	}
	output := stderr.String()
	if !strings.Contains(output, "mq submit --wait --timeout 15m --json") {
		t.Fatalf("expected submit help to mention wait json path, got %q", output)
	}
	if !strings.Contains(output, "Usage:\n  mq submit [flags]") {
		t.Fatalf("expected submit help to use mq identity, got %q", output)
	}
}

func TestRepoInitHelpExitsSuccessfully(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLIWithName("mq", []string{"repo", "init", "--help"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("expected help success for repo init, got %v", err)
	}
	output := stderr.String()
	if !strings.Contains(output, "mq repo init [flags]") {
		t.Fatalf("expected repo init help usage, got %q", output)
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
	}, newStepPrinter(&stdout)); err != nil {
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

	if err := runCLI([]string{"repo", "init", "--repo", repoRoot}, newStepPrinter(&stdout), &stderr); err != nil {
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
