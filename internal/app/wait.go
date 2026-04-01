package app

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/recallnet/mainline/internal/git"
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
	PublishRequestID int64       `json:"publish_request_id,omitempty"`
	PublishStatus    string      `json:"publish_status,omitempty"`
	Outcome          waitOutcome `json:"outcome"`
	DurationMS       int64       `json:"duration_ms"`
	LastWorkerResult string      `json:"last_worker_result,omitempty"`
	Error            string      `json:"error,omitempty"`
}

type waitTarget string

const (
	waitTargetIntegrated waitTarget = "integrated"
	waitTargetLanded     waitTarget = "landed"
)

type submissionWaitResult struct {
	SubmissionID     int64       `json:"submission_id"`
	Branch           string      `json:"branch"`
	SourceRef        string      `json:"source_ref,omitempty"`
	RefKind          string      `json:"ref_kind,omitempty"`
	SourceWorktree   string      `json:"source_worktree"`
	SourceSHA        string      `json:"source_sha"`
	RepositoryRoot   string      `json:"repository_root"`
	ProtectedBranch  string      `json:"protected_branch"`
	ProtectedSHA     string      `json:"protected_sha,omitempty"`
	SubmissionStatus string      `json:"submission_status"`
	PublishRequestID int64       `json:"publish_request_id,omitempty"`
	PublishStatus    string      `json:"publish_status,omitempty"`
	Outcome          waitOutcome `json:"outcome"`
	DurationMS       int64       `json:"duration_ms"`
	LastWorkerResult string      `json:"last_worker_result,omitempty"`
	Error            string      `json:"error,omitempty"`
}

func runWait(args []string, stdout io.Writer, stderr io.Writer) error {
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
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			return err
		}
		return waitErr
	}

	fmt.Fprintf(stdout, "Submission: %d\n", result.SubmissionID)
	fmt.Fprintf(stdout, "Branch: %s\n", result.Branch)
	fmt.Fprintf(stdout, "Submission status: %s\n", result.SubmissionStatus)
	if result.ProtectedSHA != "" {
		fmt.Fprintf(stdout, "Protected SHA: %s\n", result.ProtectedSHA)
	}
	if result.PublishRequestID != 0 {
		fmt.Fprintf(stdout, "Publish request: %d\n", result.PublishRequestID)
	}
	if result.PublishStatus != "" {
		fmt.Fprintf(stdout, "Publish status: %s\n", result.PublishStatus)
	}
	fmt.Fprintf(stdout, "Outcome: %s\n", result.Outcome)
	if result.LastWorkerResult != "" {
		fmt.Fprintf(stdout, "Last worker result: %s\n", result.LastWorkerResult)
	}
	if result.Error != "" {
		fmt.Fprintf(stdout, "Error: %s\n", result.Error)
	}
	return waitErr
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
			info, err := resolveSubmissionPublishInfo(ctx, queued.Store, queued.RepoRecord.ID, submission)
			if err != nil {
				result.Outcome = waitOutcomeFailed
				result.DurationMS = time.Since(start).Milliseconds()
				result.Error = err.Error()
				return result, err
			}
			result.PublishRequestID = info.PublishRequestID
			result.PublishStatus = info.PublishStatus
			if queued.Config.Publish.Mode == "auto" && info.PublishRequestID != 0 && info.PublishStatus == "queued" {
				cycleResult, cycleErr := runOneCycle(queued.Config.Repo.MainWorktree)
				if cycleResult != "" {
					result.LastWorkerResult = cycleResult
				}
				if cycleErr != nil {
					result.DurationMS = time.Since(start).Milliseconds()
					result.Error = cycleErr.Error()
					return result, cycleErr
				}
				info, err = resolveSubmissionPublishInfo(ctx, queued.Store, queued.RepoRecord.ID, submission)
				if err != nil {
					result.Outcome = waitOutcomeFailed
					result.DurationMS = time.Since(start).Milliseconds()
					result.Error = err.Error()
					return result, err
				}
				result.PublishRequestID = info.PublishRequestID
				result.PublishStatus = info.PublishStatus
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

func waitForSubmissionTarget(queued queuedSubmission, target waitTarget, timeout time.Duration, pollInterval time.Duration) (submissionWaitResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	result := submissionWaitResult{
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
		case "succeeded":
			protectedSHA, err := verifySubmissionReachable(ctx, queued.Store, queued.RepoRecord.ID, mainEngine, queued.Config, submission)
			if err != nil {
				result.Outcome = waitOutcomeFailed
				result.DurationMS = time.Since(start).Milliseconds()
				result.Error = err.Error()
				return result, exitWithCode(1, err)
			}
			result.ProtectedSHA = protectedSHA

			info, err := resolveSubmissionPublishInfo(ctx, queued.Store, queued.RepoRecord.ID, submission)
			if err != nil {
				result.DurationMS = time.Since(start).Milliseconds()
				result.Error = err.Error()
				return result, err
			}
			if info.ProtectedSHA != "" {
				result.ProtectedSHA = info.ProtectedSHA
			}
			result.PublishRequestID = info.PublishRequestID
			result.PublishStatus = info.PublishStatus

			if target == waitTargetIntegrated && queued.Config.Publish.Mode == "auto" && info.PublishRequestID != 0 && info.PublishStatus == "queued" {
				cycleResult, cycleErr := runOneCycle(queued.Config.Repo.MainWorktree)
				if cycleResult != "" {
					result.LastWorkerResult = cycleResult
				}
				if cycleErr != nil {
					result.DurationMS = time.Since(start).Milliseconds()
					result.Error = cycleErr.Error()
					return result, cycleErr
				}
				info, err = resolveSubmissionPublishInfo(ctx, queued.Store, queued.RepoRecord.ID, submission)
				if err != nil {
					result.DurationMS = time.Since(start).Milliseconds()
					result.Error = err.Error()
					return result, err
				}
				if info.ProtectedSHA != "" {
					result.ProtectedSHA = info.ProtectedSHA
				}
				result.PublishRequestID = info.PublishRequestID
				result.PublishStatus = info.PublishStatus
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
			if info.Outcome == submissionOutcomeLanded {
				result.Outcome = waitOutcome("landed")
				result.DurationMS = time.Since(start).Milliseconds()
				return result, nil
			}
			switch info.PublishStatus {
			case "failed":
				result.Outcome = waitOutcomeFailed
				result.DurationMS = time.Since(start).Milliseconds()
				result.Error = fmt.Sprintf("publish request %d failed", info.PublishRequestID)
				return result, exitWithCode(1, fmt.Errorf("publish request %d failed", info.PublishRequestID))
			case "cancelled":
				result.Outcome = waitOutcomeCancelled
				result.DurationMS = time.Since(start).Milliseconds()
				result.Error = fmt.Sprintf("publish request %d cancelled", info.PublishRequestID)
				return result, exitWithCode(1, fmt.Errorf("publish request %d cancelled", info.PublishRequestID))
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
