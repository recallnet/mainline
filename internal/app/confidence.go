package app

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/state"
)

type confidenceMetrics struct {
	SubmissionCount             int     `json:"submission_count"`
	PublishCount                int     `json:"publish_count"`
	BlockedRate                 float64 `json:"blocked_rate"`
	RetryRate                   float64 `json:"retry_rate"`
	SupersedeRate               float64 `json:"supersede_rate"`
	AverageIntegrationLatencyMS int64   `json:"average_integration_latency_ms"`
	AveragePublishLatencyMS     int64   `json:"average_publish_latency_ms"`
}

type confidenceEvidenceSummary struct {
	Path           string    `json:"path"`
	Exists         bool      `json:"exists"`
	MainlineCommit string    `json:"mainline_commit,omitempty"`
	GeneratedAt    time.Time `json:"generated_at,omitempty"`
	Passed         bool      `json:"passed"`
	Detail         string    `json:"detail,omitempty"`
}

type confidenceGate struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Reason  string `json:"reason,omitempty"`
	Current any    `json:"current,omitempty"`
}

type confidenceResult struct {
	RepositoryRoot string                    `json:"repository_root"`
	CurrentCommit  string                    `json:"current_commit"`
	PromotionReady bool                      `json:"promotion_ready"`
	Live           statusResult              `json:"live"`
	Metrics        confidenceMetrics         `json:"metrics"`
	Soak           confidenceEvidenceSummary `json:"soak"`
	Certification  confidenceEvidenceSummary `json:"certification"`
	Gates          []confidenceGate          `json:"gates"`
}

type soakSummaryFile struct {
	MainlineCommit       string    `json:"mainline_commit"`
	GeneratedAt          time.Time `json:"generated_at"`
	Runs                 int       `json:"runs"`
	PassedRuns           int       `json:"passed_runs"`
	FailedRuns           int       `json:"failed_runs"`
	FlakeRate            float64   `json:"flake_rate"`
	AverageDurationMS    *float64  `json:"avg_duration_ms"`
	MaxDurationMS        *int64    `json:"max_duration_ms"`
	MaxQueuedSubmissions *int      `json:"max_queued_submissions_seen"`
	MaxQueuedPublishes   *int      `json:"max_queued_publishes_seen"`
}

type certificationReportFile struct {
	GeneratedAt    time.Time                   `json:"generated_at"`
	MainlineCommit string                      `json:"mainline_commit"`
	Result         string                      `json:"result"`
	Repos          []certificationRepoEvidence `json:"repos"`
}

type certificationRepoEvidence struct {
	ID     string `json:"id"`
	Result string `json:"result"`
}

func runConfidence(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet("mainline confidence", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var repoPath string
	var asJSON bool
	var events int
	var soakSummaryPath string
	var certReportPath string

	fs.StringVar(&repoPath, "repo", ".", "repository path")
	fs.BoolVar(&asJSON, "json", false, "output json")
	fs.IntVar(&events, "events", 5, "number of recent events to include")
	fs.StringVar(&soakSummaryPath, "soak-summary", "", "path to soak summary json")
	fs.StringVar(&certReportPath, "cert-report", "", "path to certification report json")

	if err := fs.Parse(args); err != nil {
		return err
	}

	result, err := collectConfidence(repoPath, events, soakSummaryPath, certReportPath)
	if err != nil {
		return err
	}

	if asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	return renderConfidence(stdout, result)
}

func collectConfidence(repoPath string, eventLimit int, soakSummaryPath string, certReportPath string) (confidenceResult, error) {
	live, submissions, requests, events, currentCommit, err := collectConfidenceInputs(repoPath, eventLimit)
	if err != nil {
		return confidenceResult{}, err
	}

	layout, err := git.DiscoverRepositoryLayout(repoPath)
	if err != nil {
		return confidenceResult{}, err
	}
	repoRoot := layout.RepositoryRoot
	if soakSummaryPath == "" {
		soakSummaryPath = filepath.Join(repoRoot, "artifacts", "soak", "latest", "summary.json")
	}
	if certReportPath == "" {
		certReportPath = filepath.Join(repoRoot, "docs", "certification", "latest-report.json")
	}

	metrics := summarizeConfidenceMetrics(submissions, requests, events)
	soak := loadSoakEvidence(soakSummaryPath)
	cert := loadCertificationEvidence(certReportPath)
	gates := evaluateConfidenceGates(live, metrics, currentCommit, soak, cert)

	result := confidenceResult{
		RepositoryRoot: repoRoot,
		CurrentCommit:  currentCommit,
		PromotionReady: true,
		Live:           live,
		Metrics:        metrics,
		Soak:           soak,
		Certification:  cert,
		Gates:          gates,
	}
	for _, gate := range gates {
		if !gate.Passed {
			result.PromotionReady = false
			break
		}
	}

	return result, nil
}

func collectConfidenceInputs(repoPath string, eventLimit int) (statusResult, []state.IntegrationSubmission, []state.PublishRequest, []state.EventRecord, string, error) {
	layout, _, _, repoRecord, store, err := loadRepoContext(repoPath)
	if err != nil {
		return statusResult{}, nil, nil, nil, "", err
	}

	live, err := collectStatus(repoPath, eventLimit)
	if err != nil {
		return statusResult{}, nil, nil, nil, "", err
	}

	engine := git.NewEngine(layout.WorktreeRoot)
	currentCommit, err := engine.BranchHeadSHA("HEAD")
	if err != nil {
		return statusResult{}, nil, nil, nil, "", err
	}

	ctx := context.Background()
	submissions, err := store.ListIntegrationSubmissions(ctx, repoRecord.ID)
	if err != nil {
		return statusResult{}, nil, nil, nil, "", err
	}
	requests, err := store.ListPublishRequests(ctx, repoRecord.ID)
	if err != nil {
		return statusResult{}, nil, nil, nil, "", err
	}
	events, err := store.ListEvents(ctx, repoRecord.ID, 500)
	if err != nil {
		return statusResult{}, nil, nil, nil, "", err
	}

	return live, submissions, requests, events, currentCommit, nil
}

func summarizeConfidenceMetrics(submissions []state.IntegrationSubmission, requests []state.PublishRequest, events []state.EventRecord) confidenceMetrics {
	metrics := confidenceMetrics{
		SubmissionCount: len(submissions),
		PublishCount:    len(requests),
	}

	if len(submissions) > 0 {
		blocked := 0
		var integrationLatency int64
		var integrationSamples int64
		for _, submission := range submissions {
			if submission.Status == "blocked" {
				blocked++
			}
			if submission.Status == "succeeded" {
				integrationLatency += submission.UpdatedAt.Sub(submission.CreatedAt).Milliseconds()
				integrationSamples++
			}
		}
		metrics.BlockedRate = float64(blocked) / float64(len(submissions))
		if integrationSamples > 0 {
			metrics.AverageIntegrationLatencyMS = integrationLatency / integrationSamples
		}
	}

	if len(requests) > 0 {
		superseded := 0
		var publishLatency int64
		var publishSamples int64
		for _, request := range requests {
			if request.Status == "superseded" {
				superseded++
			}
			if request.Status == "succeeded" {
				publishLatency += request.UpdatedAt.Sub(request.CreatedAt).Milliseconds()
				publishSamples++
			}
		}
		metrics.SupersedeRate = float64(superseded) / float64(len(requests))
		if publishSamples > 0 {
			metrics.AveragePublishLatencyMS = publishLatency / publishSamples
		}
	}

	retries := 0
	for _, event := range events {
		if event.EventType == "submission.retried" || event.EventType == "publish.retried" {
			retries++
		}
	}
	totalAttempts := len(submissions) + len(requests)
	if totalAttempts > 0 {
		metrics.RetryRate = float64(retries) / float64(totalAttempts)
	}

	return metrics
}

func loadSoakEvidence(path string) confidenceEvidenceSummary {
	evidence := confidenceEvidenceSummary{Path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		evidence.Detail = err.Error()
		return evidence
	}
	evidence.Exists = true

	var summary soakSummaryFile
	if err := json.Unmarshal(data, &summary); err != nil {
		evidence.Detail = err.Error()
		return evidence
	}

	evidence.MainlineCommit = summary.MainlineCommit
	evidence.GeneratedAt = summary.GeneratedAt
	evidence.Passed = summary.FailedRuns == 0 && summary.Runs > 0
	evidence.Detail = fmt.Sprintf("runs=%d failed=%d flake_rate=%.4f", summary.Runs, summary.FailedRuns, summary.FlakeRate)
	return evidence
}

func loadCertificationEvidence(path string) confidenceEvidenceSummary {
	evidence := confidenceEvidenceSummary{Path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		evidence.Detail = err.Error()
		return evidence
	}
	evidence.Exists = true

	var report certificationReportFile
	if err := json.Unmarshal(data, &report); err != nil {
		evidence.Detail = err.Error()
		return evidence
	}

	passed := 0
	for _, repo := range report.Repos {
		if repo.Result == "passed" {
			passed++
		}
	}
	evidence.MainlineCommit = report.MainlineCommit
	evidence.GeneratedAt = report.GeneratedAt
	evidence.Passed = report.Result == "passed"
	evidence.Detail = fmt.Sprintf("repos=%d passed=%d result=%s", len(report.Repos), passed, report.Result)
	return evidence
}

func evaluateConfidenceGates(live statusResult, metrics confidenceMetrics, currentCommit string, soak confidenceEvidenceSummary, cert confidenceEvidenceSummary) []confidenceGate {
	gates := []confidenceGate{
		{
			Name:   "live_queue_clear",
			Passed: live.Counts.BlockSubmissions == 0 && live.Counts.FailedSubmissions == 0 && live.Counts.FailedPublishes == 0 && live.Counts.RunningSubmissions == 0 && live.Counts.RunningPublishes == 0,
			Reason: fmt.Sprintf("running_submissions=%d running_publishes=%d blocked_submissions=%d failed_submissions=%d failed_publishes=%d",
				live.Counts.RunningSubmissions,
				live.Counts.RunningPublishes,
				live.Counts.BlockSubmissions,
				live.Counts.FailedSubmissions,
				live.Counts.FailedPublishes,
			),
		},
		{
			Name:   "protected_branch_synced",
			Passed: !live.ProtectedUpstream.HasUpstream || (live.ProtectedUpstream.AheadCount == 0 && live.ProtectedUpstream.BehindCount == 0),
			Reason: fmt.Sprintf("ahead=%d behind=%d upstream=%s", live.ProtectedUpstream.AheadCount, live.ProtectedUpstream.BehindCount, live.ProtectedUpstream.Upstream),
		},
		{
			Name:   "certification_current",
			Passed: cert.Exists && cert.Passed && cert.MainlineCommit == currentCommit,
			Reason: fmt.Sprintf("exists=%t passed=%t report_commit=%s current_commit=%s", cert.Exists, cert.Passed, cert.MainlineCommit, currentCommit),
		},
		{
			Name:   "soak_current",
			Passed: soak.Exists && soak.Passed && soak.MainlineCommit == currentCommit,
			Reason: fmt.Sprintf("exists=%t passed=%t report_commit=%s current_commit=%s", soak.Exists, soak.Passed, soak.MainlineCommit, currentCommit),
		},
		{
			Name:   "recent_behavior_stable",
			Passed: metrics.BlockedRate == 0 && metrics.RetryRate == 0,
			Reason: fmt.Sprintf("blocked_rate=%.4f retry_rate=%.4f supersede_rate=%.4f", metrics.BlockedRate, metrics.RetryRate, metrics.SupersedeRate),
		},
	}
	return gates
}

func renderConfidence(stdout io.Writer, result confidenceResult) error {
	stateLabel := "FAIL"
	if result.PromotionReady {
		stateLabel = "PASS"
	}
	fmt.Fprintf(stdout, "Confidence: %s\n", stateLabel)
	fmt.Fprintf(stdout, "Repository root: %s\n", result.RepositoryRoot)
	fmt.Fprintf(stdout, "Current commit: %s\n", result.CurrentCommit)
	fmt.Fprintf(stdout, "Live queue: submissions queued=%d running=%d blocked=%d failed=%d | publishes queued=%d running=%d failed=%d\n",
		result.Live.Counts.QueuedSubmissions,
		result.Live.Counts.RunningSubmissions,
		result.Live.Counts.BlockSubmissions,
		result.Live.Counts.FailedSubmissions,
		result.Live.Counts.QueuedPublishes,
		result.Live.Counts.RunningPublishes,
		result.Live.Counts.FailedPublishes,
	)
	fmt.Fprintf(stdout, "Recent metrics: blocked_rate=%.4f retry_rate=%.4f supersede_rate=%.4f avg_integration_ms=%d avg_publish_ms=%d\n",
		result.Metrics.BlockedRate,
		result.Metrics.RetryRate,
		result.Metrics.SupersedeRate,
		result.Metrics.AverageIntegrationLatencyMS,
		result.Metrics.AveragePublishLatencyMS,
	)
	fmt.Fprintf(stdout, "Soak evidence: %s (%s)\n", passFailLabel(result.Soak.Passed && result.Soak.Exists), result.Soak.Detail)
	fmt.Fprintf(stdout, "Certification evidence: %s (%s)\n", passFailLabel(result.Certification.Passed && result.Certification.Exists), result.Certification.Detail)
	fmt.Fprintln(stdout, "Gates:")
	for _, gate := range result.Gates {
		fmt.Fprintf(stdout, "  %s %s: %s\n", passFailLabel(gate.Passed), gate.Name, gate.Reason)
	}
	return nil
}

func passFailLabel(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}
