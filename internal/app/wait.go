package app

import (
	"context"
	"fmt"
	"time"
)

type waitOutcome string

const (
	waitOutcomeSucceeded waitOutcome = "succeeded"
	waitOutcomeBlocked   waitOutcome = "blocked"
	waitOutcomeFailed    waitOutcome = "failed"
	waitOutcomeCancelled waitOutcome = "cancelled"
	waitOutcomeTimeout   waitOutcome = "timed_out"
)

type integrationWaitResult struct {
	SubmissionID     int64       `json:"submission_id"`
	Branch           string      `json:"branch"`
	SourceRef        string      `json:"source_ref,omitempty"`
	RefKind          string      `json:"ref_kind,omitempty"`
	SourceWorktree   string      `json:"source_worktree"`
	SourceSHA        string      `json:"source_sha"`
	RepositoryRoot   string      `json:"repository_root"`
	ProtectedBranch  string      `json:"protected_branch"`
	SubmissionStatus string      `json:"submission_status"`
	Outcome          waitOutcome `json:"outcome"`
	DurationMS       int64       `json:"duration_ms"`
	LastWorkerResult string      `json:"last_worker_result,omitempty"`
	Error            string      `json:"error,omitempty"`
}

func waitForIntegratedSubmission(queued queuedSubmission, timeout time.Duration, pollInterval time.Duration) (integrationWaitResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	result := integrationWaitResult{
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
			result.Outcome = waitOutcomeSucceeded
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
