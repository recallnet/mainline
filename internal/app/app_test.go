package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/state"
	_ "modernc.org/sqlite"
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
	if !strings.Contains(stdout.String(), "submit --check-only --json") {
		t.Fatalf("expected turbo submit guidance in help, got %q", stdout.String())
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
	if !strings.Contains(stdout.String(), "mq land --json --timeout 30m") {
		t.Fatalf("expected mq help to include controller path, got %q", stdout.String())
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

func TestGlobalJSONVersionReportsStructuredBuildMetadata(t *testing.T) {
	originalVersion, originalCommit, originalDate := Version, Commit, Date
	Version, Commit, Date = "v1.2.3", "abc1234", "2026-03-31T00:00:00Z"
	t.Cleanup(func() {
		Version, Commit, Date = originalVersion, originalCommit, originalDate
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLIWithName("mq", []string{"--json", "version"}, &stdout, &stderr); err != nil {
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
	if err := runCLI([]string{"--json", "completion", "bash"}, &stdout, &stderr); err != nil {
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
	if err := runCLI([]string{"--json", "publish", "--repo", repoRoot}, &stdout, &stderr); err != nil {
		t.Fatalf("publish returned error: %v", err)
	}
	var publish publishResult
	if err := json.Unmarshal(stdout.Bytes(), &publish); err != nil {
		t.Fatalf("Unmarshal publish: %v", err)
	}
	if !publish.OK || publish.PublishRequestID == 0 || publish.Status != "queued" {
		t.Fatalf("unexpected publish json: %+v", publish)
	}

	stdout.Reset()
	stderr.Reset()
	if err := runCLI([]string{"--json", "run-once", "--repo", repoRoot}, &stdout, &stderr); err != nil {
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
	if !strings.Contains(output, "land submit status confidence run-once retry cancel publish") {
		t.Fatalf("expected completion script to include land and confidence, got %q", output)
	}
	if !strings.Contains(output, "--repo --branch --sha --worktree --requested-by --priority --json --check --check-only --wait --timeout --poll-interval") {
		t.Fatalf("expected submit completion flags, got %q", output)
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
	if !strings.Contains(output, "doctor)\n      COMPREPLY=( $(compgen -W \"--repo --json --fix\" -- \"$cur\") )") {
		t.Fatalf("expected doctor completion to include --fix, got %q", output)
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
	if !strings.Contains(output, "__fish_seen_subcommand_from submit\" -l sha") {
		t.Fatalf("expected fish completion to include submit sha flag, got %q", output)
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
	if !strings.Contains(output, "__fish_seen_subcommand_from submit\" -l timeout") {
		t.Fatalf("expected fish completion to include submit timeout flag, got %q", output)
	}
	if !strings.Contains(output, "__fish_seen_subcommand_from config edit\" -l editor") {
		t.Fatalf("expected fish completion to include config edit flags, got %q", output)
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
	if err := runCLI([]string{"confidence", "--repo", repoRoot, "--json", "--soak-summary", soakPath, "--cert-report", certPath}, &stdout, &stderr); err != nil {
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
	if err := runCLI([]string{"confidence", "--repo", repoRoot, "--json", "--soak-summary", soakPath, "--cert-report", certPath}, &stdout, &stderr); err != nil {
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
	if err := runCLI([]string{"confidence", "--repo", featurePath, "--json", "--soak-summary", soakPath, "--cert-report", certPath}, &stdout, &stderr); err != nil {
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
	if err := runCLI([]string{"status", "--repo", repoRoot, "--json"}, &stdout, &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	db, err = sql.Open("sqlite", statePath)
	if err != nil {
		t.Fatalf("sql.Open(second): %v", err)
	}
	defer db.Close()

	var version int
	if err := db.QueryRow(`PRAGMA user_version;`).Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version != 4 {
		t.Fatalf("expected schema version 4 after status upgrade, got %d", version)
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
	if err := runCLI([]string{"events", "--repo", repoRoot, "--json", "--lifecycle", "--limit", "20"}, &stdout, &stderr); err != nil {
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
	if err := runCLI([]string{"events", "--repo", repoRoot, "--json", "--lifecycle", "--limit", "3"}, &stdout, &stderr); err != nil {
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
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	detachedPath := filepath.Join(t.TempDir(), "feature-detached-failure")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "--detach", detachedPath)
	writeFileAndCommit(t, detachedPath, "detached-failure.txt", "detached failure\n", "feature detached failure")
	runTestCommand(t, detachedPath, "git", "checkout", "--detach", "HEAD")
	detachedSHA := trimNewline(runTestCommand(t, detachedPath, "git", "rev-parse", "HEAD"))

	var submitStdout bytes.Buffer
	var submitStderr bytes.Buffer
	if err := runCLI([]string{"submit", "--repo", detachedPath, "--json"}, &submitStdout, &submitStderr); err != nil {
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
	if err := runCLI([]string{"run-once", "--repo", repoRoot}, &runStdout, &runStderr); err != nil {
		t.Fatalf("run-once runCLI returned error: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLI([]string{"events", "--repo", repoRoot, "--json", "--lifecycle", "--limit", "20"}, &stdout, &stderr); err != nil {
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
		}, &output)
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
	err := runCLIWithName("mq", []string{"submit", "--help"}, &stdout, &stderr)
	if err == nil {
		t.Fatalf("expected help error for submit command")
	}
	output := stderr.String()
	if !strings.Contains(output, "mq submit --wait --timeout 15m --json") {
		t.Fatalf("expected submit help to mention wait json path, got %q", output)
	}
	if !strings.Contains(output, "Usage:\n  mq submit [flags]") {
		t.Fatalf("expected submit help to use mq identity, got %q", output)
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
