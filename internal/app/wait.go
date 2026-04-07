package app

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/recallnet/mainline/internal/domain"
	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/policy"
	"github.com/recallnet/mainline/internal/state"
)

type waitOutcome string

const (
	waitOutcomeSucceeded  waitOutcome = "succeeded"
	waitOutcomeBlocked    waitOutcome = "blocked"
	waitOutcomeFailed     waitOutcome = "failed"
	waitOutcomeCancelled  waitOutcome = "cancelled"
	waitOutcomeSuperseded waitOutcome = "superseded"
	waitOutcomeTimeout    waitOutcome = "timed_out"
)

type integrationWaitResult struct {
	SubmissionID     int64                   `json:"submission_id"`
	Branch           string                  `json:"branch"`
	SourceRef        string                  `json:"source_ref,omitempty"`
	RefKind          domain.RefKind          `json:"ref_kind,omitempty"`
	SourceWorktree   string                  `json:"source_worktree"`
	SourceSHA        string                  `json:"source_sha"`
	RepositoryRoot   string                  `json:"repository_root"`
	ProtectedBranch  string                  `json:"protected_branch"`
	SubmissionStatus domain.SubmissionStatus `json:"submission_status"`
	PublishRequestID int64                   `json:"publish_request_id,omitempty"`
	PublishStatus    domain.PublishStatus    `json:"publish_status,omitempty"`
	Outcome          waitOutcome             `json:"outcome"`
	DurationMS       int64                   `json:"duration_ms"`
	QueueSummary     queueSummary            `json:"queue_summary,omitempty"`
	LastWorkerResult string                  `json:"last_worker_result,omitempty"`
	Error            string                  `json:"error,omitempty"`
}

type waitTarget string

const (
	waitTargetIntegrated waitTarget = "integrated"
	waitTargetLanded     waitTarget = "landed"
)

type submissionWaitResult struct {
	SubmissionID          int64                   `json:"submission_id"`
	Branch                string                  `json:"branch"`
	SourceRef             string                  `json:"source_ref,omitempty"`
	RefKind               domain.RefKind          `json:"ref_kind,omitempty"`
	SourceWorktree        string                  `json:"source_worktree"`
	SourceSHA             string                  `json:"source_sha"`
	RepositoryRoot        string                  `json:"repository_root"`
	ProtectedBranch       string                  `json:"protected_branch"`
	ProtectedSHA          string                  `json:"protected_sha,omitempty"`
	SubmissionStatus      domain.SubmissionStatus `json:"submission_status"`
	PublishRequestID      int64                   `json:"publish_request_id,omitempty"`
	PublishStatus         domain.PublishStatus    `json:"publish_status,omitempty"`
	Outcome               waitOutcome             `json:"outcome"`
	DurationMS            int64                   `json:"duration_ms"`
	QueueSummary          queueSummary            `json:"queue_summary,omitempty"`
	LastWorkerResult      string                  `json:"last_worker_result,omitempty"`
	PublishFailureCause   string                  `json:"publish_failure_cause,omitempty"`
	PublishFailureSummary string                  `json:"publish_failure_summary,omitempty"`
	PublishFailureError   string                  `json:"publish_failure_error,omitempty"`
	RetryHint             string                  `json:"retry_hint,omitempty"`
	ResubmitRequired      bool                    `json:"resubmit_required,omitempty"`
	Error                 string                  `json:"error,omitempty"`
}

func runWait(args []string, stdout *stepPrinter, stderr io.Writer) error {
	fs := flag.NewFlagSet(currentCLIProgramName()+" wait", flag.ContinueOnError)
	fs.SetOutput(stderr)
	setFlagUsage(fs, fmt.Sprintf(`Usage:
  %s wait [flags]

Wait for an existing submission by durable submission id.

Examples:
  mq wait --submission 42 --for integrated --json
  mq wait --submission 42 --for landed --json --timeout 30m

Flags:
`, currentCLIProgramName()))

	var repoPath string
	var submissionID int64
	var waitFor string
	var asJSON bool
	var timeout time.Duration
	var pollInterval time.Duration

	fs.StringVar(&repoPath, "repo", ".", "repository path")
	fs.Int64Var(&submissionID, "submission", 0, "submission id")
	fs.StringVar(&waitFor, "for", string(waitTargetIntegrated), "wait target: integrated or landed")
	fs.BoolVar(&asJSON, "json", false, "output json")
	fs.DurationVar(&timeout, "timeout", 10*time.Minute, "maximum wait time")
	fs.DurationVar(&pollInterval, "poll-interval", 500*time.Millisecond, "wait interval between worker checks")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if submissionID <= 0 {
		return fmt.Errorf("submission must be greater than zero")
	}
	if timeout <= 0 {
		return fmt.Errorf("timeout must be greater than zero")
	}
	if pollInterval <= 0 {
		return fmt.Errorf("poll-interval must be greater than zero")
	}

	target := waitTarget(waitFor)
	if target != waitTargetIntegrated && target != waitTargetLanded {
		return fmt.Errorf("--for must be %q or %q", waitTargetIntegrated, waitTargetLanded)
	}

	layout, repoRoot, cfg, repoRecord, store, err := loadRepoContext(repoPath)
	if err != nil {
		return err
	}
	submission, err := store.GetIntegrationSubmission(context.Background(), submissionID)
	if err != nil {
		return err
	}
	if submission.RepoID != repoRecord.ID {
		return fmt.Errorf("submission %d does not belong to repository %s", submissionID, repoRoot)
	}

	queued := queuedSubmission{
		Layout:     layout,
		RepoRoot:   repoRoot,
		Config:     cfg,
		Store:      store,
		RepoRecord: repoRecord,
		Submission: submission,
	}
	result, waitErr := waitForSubmissionTarget(queued, target, timeout, pollInterval)
	if asJSON {
		encoder := json.NewEncoder(stdout.Raw())
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			return err
		}
		return waitErr
	}

	printer := stdout
	printer.Section("Submission %d", result.SubmissionID)
	printer.Line("Branch: %s", result.Branch)
	printer.Line("Submission status: %s", result.SubmissionStatus)
	if result.ProtectedSHA != "" {
		printer.Line("Protected SHA: %s", result.ProtectedSHA)
	}
	if result.PublishRequestID != 0 {
		printer.Line("Publish request: %d", result.PublishRequestID)
	}
	if result.PublishStatus != "" {
		printer.Line("Publish status: %s", result.PublishStatus)
	}
	printer.Line("Outcome: %s", result.Outcome)
	printer.Line("Queue summary: state=%s length=%d blocked=%t running_publishes=%t running_submissions=%t queued_work=%t",
		result.QueueSummary.Headline,
		result.QueueSummary.QueueLength,
		result.QueueSummary.HasBlockedSubmissions,
		result.QueueSummary.HasRunningPublishes,
		result.QueueSummary.HasRunningSubmissions,
		result.QueueSummary.HasQueuedWork,
	)
	if result.LastWorkerResult != "" {
		printer.Line("Last worker result: %s", result.LastWorkerResult)
	}
	if result.PublishFailureSummary != "" {
		printer.Line("Publish failure: %s", result.PublishFailureSummary)
	}
	if result.RetryHint != "" {
		printer.Line("Retry hint: %s", result.RetryHint)
	}
	if result.Error != "" {
		printer.Warning("Error: %s", result.Error)
	}
	return waitErr
}

func waitForIntegratedSubmission(queued queuedSubmission, timeout time.Duration, pollInterval time.Duration) (result integrationWaitResult, waitErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	result = integrationWaitResult{
		SubmissionID:     queued.Submission.ID,
		Branch:           submissionDisplayRef(queued.Submission),
		SourceRef:        queued.Submission.SourceRef,
		RefKind:          queued.Submission.RefKind,
		SourceWorktree:   queued.Submission.SourceWorktree,
		SourceSHA:        queued.Submission.SourceSHA,
		RepositoryRoot:   queued.RepoRoot,
		ProtectedBranch:  queued.Config.Repo.ProtectedBranch,
		SubmissionStatus: queued.Submission.Status,
	}
	defer populateIntegrationWaitQueueSummary(context.Background(), queued.Store, queued.RepoRecord, queued.Config, &result)
	mainEngine := git.NewEngine(queued.Config.Repo.MainWorktree)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	start := time.Now()

	for {
		submission, err := queued.Store.GetIntegrationSubmission(ctx, queued.Submission.ID)
		if err != nil {
			if ctx.Err() != nil {
				result.DurationMS = time.Since(start).Milliseconds()
				result.Outcome = waitOutcomeTimeout
				result.Error = fmt.Sprintf("timed out waiting for submission %d to integrate", queued.Submission.ID)
				return result, exitWithCode(2, fmt.Errorf("timed out waiting for submission %d to integrate", queued.Submission.ID))
			}
			result.DurationMS = time.Since(start).Milliseconds()
			result.Error = err.Error()
			return result, err
		}
		result.SubmissionStatus = submission.Status

		switch submission.Status {
		case "succeeded":
			protectedSHA, err := verifySubmissionReachable(ctx, queued.Store, queued.RepoRecord.ID, mainEngine, queued.Config, submission)
			if err != nil {
				result.Outcome = waitOutcomeFailed
				result.DurationMS = time.Since(start).Milliseconds()
				result.Error = err.Error()
				result.SubmissionStatus = "verification_failed"
				return result, exitWithCode(1, err)
			}
			_ = protectedSHA
			info, err := resolveSubmissionPublishInfo(ctx, queued.Store, queued.RepoRecord.ID, submission, mainEngine)
			if err != nil {
				result.Outcome = waitOutcomeFailed
				result.DurationMS = time.Since(start).Milliseconds()
				result.Error = err.Error()
				return result, err
			}
			result.PublishRequestID = info.PublishRequestID
			result.PublishStatus = domain.PublishStatus(info.PublishStatus)
			if queued.Config.Publish.Mode == "auto" && info.PublishRequestID != 0 && info.PublishStatus == string(domain.PublishStatusQueued) {
				cycleResult, cycleErr := runOneCycle(queued.Config.Repo.MainWorktree)
				if cycleResult != "" {
					result.LastWorkerResult = cycleResult
				}
				if cycleErr != nil {
					result.DurationMS = time.Since(start).Milliseconds()
					result.Error = cycleErr.Error()
					return result, cycleErr
				}
				info, err = resolveSubmissionPublishInfo(ctx, queued.Store, queued.RepoRecord.ID, submission, mainEngine)
				if err != nil {
					result.Outcome = waitOutcomeFailed
					result.DurationMS = time.Since(start).Milliseconds()
					result.Error = err.Error()
					return result, err
				}
				result.PublishRequestID = info.PublishRequestID
				result.PublishStatus = domain.PublishStatus(info.PublishStatus)
				if info.Outcome == submissionOutcomeLanded {
					result.Outcome = waitOutcome("landed")
					result.DurationMS = time.Since(start).Milliseconds()
					return result, nil
				}
			}
			result.Outcome = waitOutcome("integrated")
			result.DurationMS = time.Since(start).Milliseconds()
			return result, nil
		case "blocked":
			result.Outcome = waitOutcomeBlocked
			result.DurationMS = time.Since(start).Milliseconds()
			result.Error = submission.LastError
			return result, exitWithCode(1, fmt.Errorf("submission %d blocked: %s", submission.ID, submission.LastError))
		case "failed":
			result.Outcome = waitOutcomeFailed
			result.DurationMS = time.Since(start).Milliseconds()
			result.Error = submission.LastError
			return result, exitWithCode(1, fmt.Errorf("submission %d failed: %s", submission.ID, submission.LastError))
		case "cancelled":
			result.Outcome = waitOutcomeCancelled
			result.DurationMS = time.Since(start).Milliseconds()
			if submission.LastError != "" {
				result.Error = submission.LastError
			} else {
				result.Error = "submission cancelled"
			}
			return result, exitWithCode(1, fmt.Errorf("submission %d cancelled", submission.ID))
		case "superseded":
			result.Outcome = waitOutcomeSuperseded
			result.DurationMS = time.Since(start).Milliseconds()
			if submission.LastError != "" {
				result.Error = submission.LastError
			} else {
				result.Error = "submission superseded"
			}
			return result, exitWithCode(1, fmt.Errorf("submission %d superseded: %s", submission.ID, result.Error))
		}

		cycleResult, err := runOneCycle(queued.Config.Repo.MainWorktree)
		if cycleResult != "" {
			result.LastWorkerResult = cycleResult
		}
		if err != nil {
			result.DurationMS = time.Since(start).Milliseconds()
			result.Error = err.Error()
			return result, err
		}

		select {
		case <-ctx.Done():
			result.DurationMS = time.Since(start).Milliseconds()
			result.Outcome = waitOutcomeTimeout
			result.Error = fmt.Sprintf("timed out waiting for submission %d to integrate", queued.Submission.ID)
			return result, exitWithCode(2, fmt.Errorf("timed out waiting for submission %d to integrate", queued.Submission.ID))
		case <-ticker.C:
		}
	}
}

func waitForSubmissionTarget(queued queuedSubmission, target waitTarget, timeout time.Duration, pollInterval time.Duration) (result submissionWaitResult, waitErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	result = submissionWaitResult{
		SubmissionID:     queued.Submission.ID,
		Branch:           submissionDisplayRef(queued.Submission),
		SourceRef:        queued.Submission.SourceRef,
		RefKind:          queued.Submission.RefKind,
		SourceWorktree:   queued.Submission.SourceWorktree,
		SourceSHA:        queued.Submission.SourceSHA,
		RepositoryRoot:   queued.RepoRoot,
		ProtectedBranch:  queued.Config.Repo.ProtectedBranch,
		SubmissionStatus: queued.Submission.Status,
	}
	defer populateSubmissionWaitQueueSummary(context.Background(), queued.Store, queued.RepoRecord, queued.Config, &result)
	mainEngine := git.NewEngine(queued.Config.Repo.MainWorktree)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	start := time.Now()

	for {
		submission, err := queued.Store.GetIntegrationSubmission(ctx, queued.Submission.ID)
		if err != nil {
			if ctx.Err() != nil {
				result.DurationMS = time.Since(start).Milliseconds()
				result.Outcome = waitOutcomeTimeout
				result.Error = fmt.Sprintf("timed out waiting for submission %d", queued.Submission.ID)
				return result, exitWithCode(2, fmt.Errorf("timed out waiting for submission %d", queued.Submission.ID))
			}
			result.DurationMS = time.Since(start).Milliseconds()
			result.Error = err.Error()
			return result, err
		}
		result.SubmissionStatus = submission.Status

		switch submission.Status {
		case "blocked":
			result.Outcome = waitOutcomeBlocked
			result.DurationMS = time.Since(start).Milliseconds()
			result.Error = submission.LastError
			return result, exitWithCode(1, fmt.Errorf("submission %d blocked: %s", submission.ID, submission.LastError))
		case "failed":
			result.Outcome = waitOutcomeFailed
			result.DurationMS = time.Since(start).Milliseconds()
			result.Error = submission.LastError
			return result, exitWithCode(1, fmt.Errorf("submission %d failed: %s", submission.ID, submission.LastError))
		case "cancelled":
			result.Outcome = waitOutcomeCancelled
			result.DurationMS = time.Since(start).Milliseconds()
			if submission.LastError != "" {
				result.Error = submission.LastError
			} else {
				result.Error = "submission cancelled"
			}
			return result, exitWithCode(1, fmt.Errorf("submission %d cancelled", submission.ID))
		case "superseded":
			result.Outcome = waitOutcomeSuperseded
			result.DurationMS = time.Since(start).Milliseconds()
			if submission.LastError != "" {
				result.Error = submission.LastError
			} else {
				result.Error = "submission superseded"
			}
			return result, exitWithCode(1, fmt.Errorf("submission %d superseded: %s", submission.ID, result.Error))
		case "succeeded":
			protectedSHA, err := verifySubmissionReachable(ctx, queued.Store, queued.RepoRecord.ID, mainEngine, queued.Config, submission)
			if err != nil {
				result.Outcome = waitOutcomeFailed
				result.DurationMS = time.Since(start).Milliseconds()
				result.Error = err.Error()
				return result, exitWithCode(1, err)
			}
			result.ProtectedSHA = protectedSHA

			info, err := resolveSubmissionPublishInfo(ctx, queued.Store, queued.RepoRecord.ID, submission, mainEngine)
			if err != nil {
				result.DurationMS = time.Since(start).Milliseconds()
				result.Error = err.Error()
				return result, err
			}
			if info.ProtectedSHA != "" {
				result.ProtectedSHA = info.ProtectedSHA
			}
			result.PublishRequestID = info.PublishRequestID
			result.PublishStatus = domain.PublishStatus(info.PublishStatus)
			result.PublishFailureCause = info.Failure.Cause
			result.PublishFailureSummary = info.Failure.Summary
			result.PublishFailureError = info.Failure.Error
			result.RetryHint = info.Failure.RetryHint
			result.ResubmitRequired = info.Failure.ResubmitRequired

			if target == waitTargetIntegrated && queued.Config.Publish.Mode == "auto" && info.PublishRequestID != 0 && info.PublishStatus == string(domain.PublishStatusQueued) {
				cycleResult, cycleErr := runOneCycle(queued.Config.Repo.MainWorktree)
				if cycleResult != "" {
					result.LastWorkerResult = cycleResult
				}
				if cycleErr != nil {
					result.DurationMS = time.Since(start).Milliseconds()
					result.Error = cycleErr.Error()
					return result, cycleErr
				}
				info, err = resolveSubmissionPublishInfo(ctx, queued.Store, queued.RepoRecord.ID, submission, mainEngine)
				if err != nil {
					result.DurationMS = time.Since(start).Milliseconds()
					result.Error = err.Error()
					return result, err
				}
				if info.ProtectedSHA != "" {
					result.ProtectedSHA = info.ProtectedSHA
				}
				result.PublishRequestID = info.PublishRequestID
				result.PublishStatus = domain.PublishStatus(info.PublishStatus)
				result.PublishFailureCause = info.Failure.Cause
				result.PublishFailureSummary = info.Failure.Summary
				result.PublishFailureError = info.Failure.Error
				result.RetryHint = info.Failure.RetryHint
				result.ResubmitRequired = info.Failure.ResubmitRequired
				if info.Outcome == submissionOutcomeLanded {
					result.Outcome = waitOutcome("landed")
					result.DurationMS = time.Since(start).Milliseconds()
					return result, nil
				}
			}

			if target == waitTargetIntegrated {
				result.Outcome = waitOutcome("integrated")
				result.DurationMS = time.Since(start).Milliseconds()
				return result, nil
			}
			if queued.Config.Publish.Mode != "auto" && info.PublishRequestID == 0 {
				result.Outcome = waitOutcome("integrated")
				result.DurationMS = time.Since(start).Milliseconds()
				result.Error = fmt.Sprintf(
					"submission %d integrated, but repo publish mode is manual; run mq publish or mq land when remote landing is required",
					submission.ID,
				)
				return result, exitWithCode(1, fmt.Errorf("%s", result.Error))
			}
			if info.Outcome == submissionOutcomeLanded {
				result.Outcome = waitOutcome("landed")
				result.DurationMS = time.Since(start).Milliseconds()
				return result, nil
			}
			switch info.PublishStatus {
			case "failed":
				result.Outcome = waitOutcomeFailed
				result.DurationMS = time.Since(start).Milliseconds()
				if info.Failure.Summary != "" {
					result.Error = fmt.Sprintf("publish request %d failed: %s", info.PublishRequestID, info.Failure.Summary)
				} else {
					result.Error = fmt.Sprintf("publish request %d failed", info.PublishRequestID)
				}
				return result, exitWithCode(1, fmt.Errorf("%s", result.Error))
			case "cancelled":
				result.Outcome = waitOutcomeCancelled
				result.DurationMS = time.Since(start).Milliseconds()
				result.Error = fmt.Sprintf("publish request %d cancelled", info.PublishRequestID)
				return result, exitWithCode(1, fmt.Errorf("publish request %d cancelled", info.PublishRequestID))
			case "superseded":
				result.Outcome = waitOutcomeSuperseded
				result.DurationMS = time.Since(start).Milliseconds()
				result.Error = fmt.Sprintf("publish request %d superseded by newer protected tip", info.PublishRequestID)
				return result, exitWithCode(1, fmt.Errorf("publish request %d superseded by newer protected tip", info.PublishRequestID))
			}
		}

		cycleResult, err := runOneCycle(queued.Config.Repo.MainWorktree)
		if cycleResult != "" {
			result.LastWorkerResult = cycleResult
		}
		if err != nil {
			result.DurationMS = time.Since(start).Milliseconds()
			result.Error = err.Error()
			return result, err
		}

		select {
		case <-ctx.Done():
			result.DurationMS = time.Since(start).Milliseconds()
			result.Outcome = waitOutcomeTimeout
			result.Error = fmt.Sprintf("timed out waiting for submission %d", queued.Submission.ID)
			return result, exitWithCode(2, fmt.Errorf("timed out waiting for submission %d", queued.Submission.ID))
		case <-ticker.C:
		}
	}
}

func populateIntegrationWaitQueueSummary(ctx context.Context, store state.Store, repoRecord state.RepositoryRecord, cfg policy.File, result *integrationWaitResult) {
	if result == nil {
		return
	}
	snapshot, err := loadRepoStatusSnapshot(ctx, store, repoRecord, cfg, 0)
	if err != nil {
		return
	}
	result.QueueSummary = snapshot.QueueSummary
}

func populateSubmissionWaitQueueSummary(ctx context.Context, store state.Store, repoRecord state.RepositoryRecord, cfg policy.File, result *submissionWaitResult) {
	if result == nil {
		return
	}
	snapshot, err := loadRepoStatusSnapshot(ctx, store, repoRecord, cfg, 0)
	if err != nil {
		return
	}
	result.QueueSummary = snapshot.QueueSummary
}
