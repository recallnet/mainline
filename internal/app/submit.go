package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"time"

	"github.com/recallnet/mainline/internal/domain"
	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/policy"
	"github.com/recallnet/mainline/internal/state"
)

const (
	submissionPriorityHigh   = "high"
	submissionPriorityNormal = "normal"
	submissionPriorityLow    = "low"
)

type submitValidationError struct {
	Code    string
	Message string
}

func (e *submitValidationError) Error() string {
	return e.Message
}

type submitOptions struct {
	repoPath     string
	branch       string
	sha          string
	worktreePath string
	requestedBy  string
	priority     string
	checkOnly    bool
	queueOnly    bool
	allowNewer   bool
	waitTarget   string
}

type preparedSubmission struct {
	Layout       git.RepositoryLayout
	RepoRoot     string
	Config       policy.File
	Store        state.Store
	Branch       string
	SourceRef    string
	RefKind      domain.RefKind
	WorktreePath string
	SourceSHA    string
	AllowNewer   bool
	RequestedBy  string
	Priority     string
	Duplicate    *state.IntegrationSubmission
}

type queuedSubmission struct {
	Layout     git.RepositoryLayout
	RepoRoot   string
	Config     policy.File
	Store      state.Store
	RepoRecord state.RepositoryRecord
	Submission state.IntegrationSubmission
}

type submitResult struct {
	OK                    bool                     `json:"ok"`
	Checked               bool                     `json:"checked"`
	Queued                bool                     `json:"queued"`
	Waited                bool                     `json:"waited"`
	DrainAttempted        bool                     `json:"drain_attempted,omitempty"`
	SubmissionID          int64                    `json:"submission_id,omitempty"`
	Branch                string                   `json:"branch,omitempty"`
	SourceRef             string                   `json:"source_ref,omitempty"`
	RefKind               domain.RefKind           `json:"ref_kind,omitempty"`
	SourceWorktree        string                   `json:"source_worktree,omitempty"`
	SourceSHA             string                   `json:"source_sha,omitempty"`
	AllowNewerHead        bool                     `json:"allow_newer_head,omitempty"`
	RepositoryRoot        string                   `json:"repository_root,omitempty"`
	RequestedBy           string                   `json:"requested_by,omitempty"`
	Priority              string                   `json:"priority,omitempty"`
	SubmissionStatus      domain.SubmissionStatus  `json:"submission_status,omitempty"`
	Outcome               domain.SubmissionOutcome `json:"outcome,omitempty"`
	PublishRequestID      int64                    `json:"publish_request_id,omitempty"`
	PublishStatus         domain.PublishStatus     `json:"publish_status,omitempty"`
	QueuePosition         int                      `json:"queue_position,omitempty"`
	EstimatedCompletionMS int64                    `json:"estimated_completion_ms,omitempty"`
	EstimateBasis         string                   `json:"estimate_basis,omitempty"`
	DurationMS            int64                    `json:"duration_ms,omitempty"`
	DrainResult           string                   `json:"drain_result,omitempty"`
	LastWorkerResult      string                   `json:"last_worker_result,omitempty"`
	PublishFailureCause   string                   `json:"publish_failure_cause,omitempty"`
	PublishFailureSummary string                   `json:"publish_failure_summary,omitempty"`
	PublishFailureError   string                   `json:"publish_failure_error,omitempty"`
	RetryHint             string                   `json:"retry_hint,omitempty"`
	ResubmitRequired      bool                     `json:"resubmit_required,omitempty"`
	ErrorCode             string                   `json:"error_code,omitempty"`
	Error                 string                   `json:"error,omitempty"`
}

func runSubmit(args []string, stdout *stepPrinter, stderr io.Writer) error {
	if err := applyAppTestFault("submit.start"); err != nil {
		return err
	}

	fs := flag.NewFlagSet(currentCLIProgramName()+" submit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	setFlagUsage(fs, fmt.Sprintf(`Usage:
  %s submit [flags]

Queue a topic worktree or detached sha for serialized integration.
By default, --wait stops at integration. If publish mode is manual, that means
the branch is on local protected main but not yet pushed to remote.

Turbo agent flow:
  mq submit --check-only --json
  mq submit --wait --timeout 15m --json
  mq submit --wait --for landed --timeout 30m --json
  mq wait --submission <id> --for landed --json --timeout 30m
  mq land --json --timeout 30m

Plain mq submit now queues first, then opportunistically tries to drain.
If another worker already holds the integration lock, submit still succeeds and
the active worker keeps draining.
Use --queue-only when you want to prove daemon-only handling for a specific
submission.

Examples:
  mq submit
  mq submit --queue-only --json
  mq submit --wait --for landed --timeout 30m --json
  mq submit --allow-newer-head --wait --timeout 15m --json
  mq submit --wait --timeout 15m --json
  mq submit --sha <commit> --json

Flags:
`, currentCLIProgramName()))

	var repoPath string
	var branch string
	var sha string
	var worktreePath string
	var requestedBy string
	var priority string
	var asJSON bool
	var checkOnly bool
	var checkOnlyAlias bool
	var waitForResult bool
	var waitFor string
	var queueOnly bool
	var timeout time.Duration
	var pollInterval time.Duration
	var allowNewerHead bool

	fs.StringVar(&repoPath, "repo", ".", "repository path")
	fs.StringVar(&branch, "branch", "", "branch to submit")
	fs.StringVar(&sha, "sha", "", "detached commit to submit")
	fs.StringVar(&worktreePath, "worktree", "", "source worktree path")
	fs.StringVar(&requestedBy, "requested-by", "", "submitter identity")
	fs.StringVar(&priority, "priority", submissionPriorityNormal, "submission priority: high, normal, or low")
	fs.BoolVar(&asJSON, "json", false, "output json")
	fs.BoolVar(&checkOnly, "check", false, "validate submission without queueing it")
	fs.BoolVar(&checkOnlyAlias, "check-only", false, "validate submission without queueing it")
	fs.BoolVar(&queueOnly, "queue-only", false, "queue the submission without opportunistically draining it")
	fs.BoolVar(&allowNewerHead, "allow-newer-head", false, "allow a queued branch head to advance before integration if it remains a descendant of the submitted sha")
	fs.BoolVar(&waitForResult, "wait", false, "wait for the submission result")
	fs.StringVar(&waitFor, "for", string(waitTargetIntegrated), "wait target when used with --wait: integrated or landed")
	fs.DurationVar(&timeout, "timeout", 10*time.Minute, "maximum time to wait for integration")
	fs.DurationVar(&pollInterval, "poll-interval", 500*time.Millisecond, "wait interval between worker checks")

	if err := fs.Parse(args); err != nil {
		return err
	}
	checkOnly = checkOnly || checkOnlyAlias
	if checkOnly && waitForResult {
		return fmt.Errorf("--check/--check-only and --wait cannot be used together")
	}
	if timeout <= 0 {
		return fmt.Errorf("timeout must be greater than zero")
	}
	if pollInterval <= 0 {
		return fmt.Errorf("poll-interval must be greater than zero")
	}
	if waitTarget(waitFor) != waitTargetIntegrated && waitTarget(waitFor) != waitTargetLanded {
		return fmt.Errorf("--for must be %q or %q", waitTargetIntegrated, waitTargetLanded)
	}
	if waitFor != string(waitTargetIntegrated) && !waitForResult {
		return fmt.Errorf("--for requires --wait")
	}
	opts := submitOptions{
		repoPath:     repoPath,
		branch:       branch,
		sha:          sha,
		worktreePath: worktreePath,
		requestedBy:  requestedBy,
		priority:     priority,
		checkOnly:    checkOnly,
		queueOnly:    queueOnly,
		allowNewer:   allowNewerHead,
		waitTarget:   waitFor,
	}
	prepared, err := prepareSubmission(opts)
	if err != nil {
		if asJSON {
			return writeSubmitJSON(stdout, submitResult{
				OK:             false,
				Checked:        true,
				Queued:         false,
				Branch:         preparedSubmissionDisplayRef(prepared),
				SourceRef:      prepared.SourceRef,
				RefKind:        prepared.RefKind,
				AllowNewerHead: prepared.AllowNewer,
				ErrorCode:      submitErrorCode(err),
				Error:          err.Error(),
			}, err)
		}
		return err
	}

	if checkOnly {
		result := submitResult{
			OK:             true,
			Checked:        true,
			Queued:         false,
			Branch:         preparedSubmissionDisplayRef(prepared),
			SourceRef:      prepared.SourceRef,
			RefKind:        prepared.RefKind,
			SourceWorktree: prepared.WorktreePath,
			SourceSHA:      prepared.SourceSHA,
			AllowNewerHead: prepared.AllowNewer,
			RepositoryRoot: prepared.RepoRoot,
			RequestedBy:    prepared.RequestedBy,
			Priority:       prepared.Priority,
		}
		if asJSON {
			return writeSubmitJSON(stdout, result, nil)
		}
		printer := stdout
		printer.Success("Submission check passed")
		printer.Line("Branch: %s", result.Branch)
		printer.Line("Worktree: %s", result.SourceWorktree)
		printer.Line("Source SHA: %s", result.SourceSHA)
		return nil
	}

	queued, err := queuePreparedSubmission(prepared)
	if err != nil {
		if asJSON {
			return writeSubmitJSON(stdout, submitResult{
				OK:             false,
				Checked:        true,
				Queued:         false,
				Branch:         preparedSubmissionDisplayRef(prepared),
				SourceRef:      prepared.SourceRef,
				RefKind:        prepared.RefKind,
				SourceWorktree: prepared.WorktreePath,
				SourceSHA:      prepared.SourceSHA,
				AllowNewerHead: prepared.AllowNewer,
				RepositoryRoot: prepared.RepoRoot,
				RequestedBy:    prepared.RequestedBy,
				Priority:       prepared.Priority,
				ErrorCode:      submitErrorCode(err),
				Error:          err.Error(),
			}, err)
		}
		return err
	}

	result := submitResult{
		OK:               true,
		Checked:          true,
		Queued:           true,
		Waited:           false,
		SubmissionID:     queued.Submission.ID,
		Branch:           submissionDisplayRef(queued.Submission),
		SourceRef:        queued.Submission.SourceRef,
		RefKind:          queued.Submission.RefKind,
		SourceWorktree:   queued.Submission.SourceWorktree,
		SourceSHA:        queued.Submission.SourceSHA,
		AllowNewerHead:   queued.Submission.AllowNewerHead,
		RepositoryRoot:   queued.RepoRoot,
		RequestedBy:      queued.Submission.RequestedBy,
		Priority:         queued.Submission.Priority,
		SubmissionStatus: queued.Submission.Status,
	}
	if estimate, queuePosition, estimateErr := submissionQueueEstimate(context.Background(), queued); estimateErr == nil {
		result.QueuePosition = queuePosition
		result.EstimatedCompletionMS = estimate.AvgExecutionMS * int64(queuePosition)
		result.EstimateBasis = string(estimate.Basis)
	}
	if waitForResult {
		target := waitTarget(opts.waitTarget)
		if target == "" {
			target = waitTargetIntegrated
		}
		waitResult, waitErr := waitForSubmissionTarget(queued, target, timeout, pollInterval)
		result.OK = waitErr == nil
		result.Waited = true
		result.SubmissionStatus = waitResult.SubmissionStatus
		result.Outcome = domain.SubmissionOutcome(waitResult.Outcome)
		result.PublishRequestID = waitResult.PublishRequestID
		result.PublishStatus = waitResult.PublishStatus
		result.DurationMS = waitResult.DurationMS
		result.LastWorkerResult = waitResult.LastWorkerResult
		if waitResult.Error != "" {
			result.Error = waitResult.Error
		}
		if waitErr != nil {
			if result.ErrorCode == "" {
				result.ErrorCode = submitWaitOutcomeCode(waitResult.Outcome)
			}
			if asJSON {
				return writeSubmitJSON(stdout, result, waitErr)
			}
			printer := stdout
			printer.Section("Queued submission %d", queued.Submission.ID)
			printer.Line("Branch: %s", submissionDisplayRef(queued.Submission))
			printer.Line("Worktree: %s", queued.Submission.SourceWorktree)
			printer.Line("Source SHA: %s", queued.Submission.SourceSHA)
			printer.Line("Submission status: %s", result.SubmissionStatus)
			if result.Outcome != "" {
				printer.Line("Outcome: %s", result.Outcome)
			}
			if result.PublishRequestID != 0 {
				printer.Line("Publish request: %d", result.PublishRequestID)
			}
			if result.PublishStatus != "" {
				printer.Line("Publish status: %s", result.PublishStatus)
			}
			if result.LastWorkerResult != "" {
				printer.Line("Last worker result: %s", result.LastWorkerResult)
			}
			if result.DurationMS > 0 {
				printer.Line("Duration: %s", (time.Duration(result.DurationMS) * time.Millisecond).Round(time.Millisecond))
			}
			if result.Outcome == submissionOutcomeIntegrated && queued.Config.Publish.Mode == "manual" {
				printer.Warning("Publish mode is manual: run mq publish or use mq land / mq wait --for landed when remote landing is required.")
			}
			return waitErr
		}
	}
	if !waitForResult && shouldTryDrainAfterSubmit(opts) {
		result.DrainAttempted = true
		drainResult, drainErr := drainRepoUntilSettled(queued.Config.Repo.MainWorktree)
		if drainResult != "" {
			result.DrainResult = drainResult
			result.LastWorkerResult = drainResult
		}
		if drainErr != nil {
			result.Error = drainErr.Error()
		} else {
			submission, loadErr := queued.Store.GetIntegrationSubmission(context.Background(), queued.Submission.ID)
			if loadErr == nil {
				result.SubmissionStatus = submission.Status
				if submission.Status == domain.SubmissionStatusSucceeded {
					info, infoErr := resolveSubmissionPublishInfo(context.Background(), queued.Store, queued.RepoRecord.ID, submission, git.NewEngine(queued.Config.Repo.MainWorktree))
					if infoErr == nil {
						result.PublishRequestID = info.PublishRequestID
						result.PublishStatus = domain.PublishStatus(info.PublishStatus)
						result.Outcome = info.Outcome
						result.PublishFailureCause = info.Failure.Cause
						result.PublishFailureSummary = info.Failure.Summary
						result.PublishFailureError = info.Failure.Error
						result.RetryHint = info.Failure.RetryHint
						result.ResubmitRequired = info.Failure.ResubmitRequired
					}
					if result.Outcome == "" {
						result.Outcome = submissionOutcomeIntegrated
					}
				}
			}
		}
	}
	if asJSON {
		return writeSubmitJSON(stdout, result, nil)
	}

	printer := stdout
	printer.Section("Queued submission %d", queued.Submission.ID)
	printer.Line("Branch: %s", submissionDisplayRef(queued.Submission))
	printer.Line("Worktree: %s", queued.Submission.SourceWorktree)
	printer.Line("Source SHA: %s", queued.Submission.SourceSHA)
	if result.QueuePosition > 0 {
		printer.Line("Queue position: %d", result.QueuePosition)
	}
	if eta := humanEstimatedCompletion(result.EstimatedCompletionMS); eta != "" {
		printer.Line("Estimated completion: %s (%s)", eta, result.EstimateBasis)
	}
	if result.DrainResult != "" {
		printer.Line("Drain result: %s", result.DrainResult)
	}
	if result.Error != "" && !waitForResult {
		printer.Warning("Drain error: %s", result.Error)
	}
	if !waitForResult {
		printer.Info("Follow: mq wait --submission %d --for landed --json --timeout 30m", queued.Submission.ID)
	}
	if waitForResult {
		printer.Line("Submission status: %s", result.SubmissionStatus)
		if result.Outcome != "" {
			printer.Line("Outcome: %s", result.Outcome)
		}
		if result.PublishRequestID != 0 {
			printer.Line("Publish request: %d", result.PublishRequestID)
		}
		if result.PublishStatus != "" {
			printer.Line("Publish status: %s", result.PublishStatus)
		}
		if result.LastWorkerResult != "" {
			printer.Line("Last worker result: %s", result.LastWorkerResult)
		}
		if result.DurationMS > 0 {
			printer.Line("Duration: %s", (time.Duration(result.DurationMS) * time.Millisecond).Round(time.Millisecond))
		}
		if result.Outcome == submissionOutcomeIntegrated && queued.Config.Publish.Mode == "manual" {
			printer.Warning("Publish mode is manual: run mq publish or use mq land / mq wait --for landed when remote landing is required.")
		}
	}
	return nil
}

func shouldTryDrainAfterSubmit(opts submitOptions) bool {
	return !opts.queueOnly && os.Getenv("MAINLINE_DISABLE_SUBMIT_DRAIN") == ""
}

func queueSubmission(opts submitOptions) (queuedSubmission, error) {
	prepared, err := prepareSubmission(opts)
	if err != nil {
		return queuedSubmission{}, err
	}
	return queuePreparedSubmission(prepared)
}

func prepareSubmission(opts submitOptions) (preparedSubmission, error) {
	if opts.priority == "" {
		opts.priority = submissionPriorityNormal
	}
	if !isValidSubmissionPriority(opts.priority) {
		return preparedSubmission{}, &submitValidationError{
			Code:    "invalid_priority",
			Message: fmt.Sprintf("priority must be one of %q, %q, or %q", submissionPriorityHigh, submissionPriorityNormal, submissionPriorityLow),
		}
	}
	layout, err := git.DiscoverRepositoryLayout(opts.repoPath)
	if err != nil {
		return preparedSubmission{}, &submitValidationError{
			Code:    "not_git_repository",
			Message: err.Error(),
		}
	}
	repoRoot := layout.RepositoryRoot

	cfg, _, err := policy.LoadOrDefault(repoRoot)
	if err != nil {
		return preparedSubmission{}, err
	}
	if cfg.Repo.MainWorktree == "" {
		cfg.Repo.MainWorktree = layout.WorktreeRoot
	}

	worktreePath := opts.worktreePath
	if worktreePath == "" {
		worktreePath = layout.WorktreeRoot
	}
	worktreePath = filepath.Clean(worktreePath)

	worktreeLayout, err := git.DiscoverRepositoryLayout(worktreePath)
	if err != nil {
		return preparedSubmission{}, &submitValidationError{
			Code:    "invalid_worktree",
			Message: err.Error(),
		}
	}
	if filepath.Clean(worktreeLayout.GitDir) != filepath.Clean(layout.GitDir) {
		return preparedSubmission{}, &submitValidationError{
			Code:    "foreign_worktree",
			Message: fmt.Sprintf("worktree %s does not belong to repository %s", worktreePath, repoRoot),
		}
	}

	engine := git.NewEngine(worktreePath)
	worktree, err := engine.ResolveWorktree(worktreePath)
	if err != nil {
		return preparedSubmission{}, &submitValidationError{
			Code:    "invalid_worktree",
			Message: err.Error(),
		}
	}

	if opts.branch != "" && opts.sha != "" {
		return preparedSubmission{}, &submitValidationError{
			Code:    "ref_conflict",
			Message: "--branch and --sha cannot be used together",
		}
	}

	branch := opts.branch
	if !worktree.IsDetached && worktree.Branch == cfg.Repo.ProtectedBranch {
		return preparedSubmission{}, &submitValidationError{
			Code:    "protected_branch",
			Message: fmt.Sprintf("cannot submit protected branch %q", cfg.Repo.ProtectedBranch),
		}
	}

	clean, err := engine.WorktreeIsClean(worktreePath)
	if err != nil {
		return preparedSubmission{}, err
	}
	if !clean {
		return preparedSubmission{}, &submitValidationError{
			Code:    "dirty_worktree",
			Message: fmt.Sprintf("source worktree %s is dirty; clean it before submission", worktreePath),
		}
	}

	headSHA, err := engine.WorktreeHeadSHA(worktreePath)
	if err != nil {
		return preparedSubmission{}, fmt.Errorf("resolve worktree head for %s: %w", worktreePath, err)
	}

	sourceRef := ""
	refKind := submissionRefKindBranch
	if opts.sha != "" {
		resolvedSHA, err := engine.BranchHeadSHA(opts.sha)
		if err != nil {
			return preparedSubmission{}, &submitValidationError{
				Code:    "sha_missing",
				Message: fmt.Sprintf("commit %q does not exist", opts.sha),
			}
		}
		if resolvedSHA != headSHA {
			return preparedSubmission{}, &submitValidationError{
				Code:    "sha_not_checked_out",
				Message: fmt.Sprintf("worktree %s is at %s, expected %s to be checked out", worktreePath, headSHA, resolvedSHA),
			}
		}
		sourceRef = resolvedSHA
		refKind = submissionRefKindSHA
	} else if branch != "" {
		if worktree.IsDetached {
			return preparedSubmission{}, &submitValidationError{
				Code:    "detached_head",
				Message: fmt.Sprintf("worktree %s is detached; pass --sha %s or check out branch %q", worktreePath, headSHA, branch),
			}
		}
		currentBranch, err := engine.CurrentBranchAtPath(worktreePath)
		if err != nil {
			return preparedSubmission{}, err
		}
		if currentBranch != branch {
			return preparedSubmission{}, &submitValidationError{
				Code:    "branch_not_checked_out",
				Message: fmt.Sprintf("branch %q is not checked out in worktree %s", branch, worktreePath),
			}
		}
		if branch == cfg.Repo.ProtectedBranch {
			return preparedSubmission{}, &submitValidationError{
				Code:    "protected_branch",
				Message: fmt.Sprintf("cannot submit protected branch %q", branch),
			}
		}
		if !engine.BranchExists(branch) {
			return preparedSubmission{}, &submitValidationError{
				Code:    "branch_missing",
				Message: fmt.Sprintf("branch %q does not exist", branch),
			}
		}
		sourceRef = branch
		refKind = submissionRefKindBranch
		headSHA, err = engine.BranchHeadSHA(branch)
		if err != nil {
			return preparedSubmission{}, fmt.Errorf("resolve branch head for %q: %w", branch, err)
		}
	} else if worktree.IsDetached {
		sourceRef = headSHA
		refKind = submissionRefKindSHA
	} else {
		branch = worktree.Branch
		if branch == cfg.Repo.ProtectedBranch {
			return preparedSubmission{}, &submitValidationError{
				Code:    "protected_branch",
				Message: fmt.Sprintf("cannot submit protected branch %q", branch),
			}
		}
		if !engine.BranchExists(branch) {
			return preparedSubmission{}, &submitValidationError{
				Code:    "branch_missing",
				Message: fmt.Sprintf("branch %q does not exist", branch),
			}
		}
		sourceRef = branch
		refKind = submissionRefKindBranch
		headSHA, err = engine.BranchHeadSHA(branch)
		if err != nil {
			return preparedSubmission{}, fmt.Errorf("resolve branch head for %q: %w", branch, err)
		}
	}

	commitCount, err := engine.CommitCount(sourceRef)
	if err != nil {
		return preparedSubmission{}, err
	}
	if commitCount == 0 {
		target := sourceRef
		if target == "" {
			target = headSHA
		}
		return preparedSubmission{}, &submitValidationError{
			Code:    "ref_has_no_commits",
			Message: fmt.Sprintf("reference %q has no commits", target),
		}
	}

	requestedBy := opts.requestedBy
	if requestedBy == "" {
		currentUser, err := user.Current()
		if err == nil {
			requestedBy = currentUser.Username
		} else {
			requestedBy = "unknown"
		}
	}

	store := state.NewStore(state.DefaultPath(layout.GitDir))
	if !store.Exists() {
		return preparedSubmission{}, &submitValidationError{
			Code:    "repository_not_initialized",
			Message: "repository is not initialized; run `mainline repo init` first",
		}
	}
	if err := store.EnsureSchema(context.Background()); err != nil {
		return preparedSubmission{}, err
	}

	ctx := context.Background()
	repoRecord, err := store.GetRepositoryByPath(ctx, repoRoot)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return preparedSubmission{}, err
	}
	if err == nil {
		duplicate, found, err := findActiveDuplicateSubmission(ctx, store, repoRecord.ID, sourceRef, headSHA)
		if err != nil {
			return preparedSubmission{}, err
		}
		if found {
			if duplicate.Status == "queued" && duplicate.Priority != opts.priority {
				dup := duplicate
				return preparedSubmission{
					Layout:       layout,
					RepoRoot:     repoRoot,
					Config:       cfg,
					Store:        store,
					Branch:       branch,
					SourceRef:    sourceRef,
					RefKind:      refKind,
					WorktreePath: worktree.Path,
					SourceSHA:    headSHA,
					AllowNewer:   opts.allowNewer,
					RequestedBy:  requestedBy,
					Priority:     opts.priority,
					Duplicate:    &dup,
				}, nil
			}
			return preparedSubmission{}, &submitValidationError{
				Code:    "already_queued",
				Message: fmt.Sprintf("submission %d for %q at %s is already %s", duplicate.ID, sourceRef, headSHA, duplicate.Status),
			}
		}
		if !opts.checkOnly && cfg.Integration.MaxQueueDepth > 0 {
			queuedCount, err := store.CountQueuedIntegrationSubmissions(ctx, repoRecord.ID)
			if err != nil {
				return preparedSubmission{}, err
			}
			if queuedCount >= cfg.Integration.MaxQueueDepth {
				return preparedSubmission{}, &submitValidationError{
					Code:    "integration_queue_full",
					Message: fmt.Sprintf("integration queue depth %d reached MaxQueueDepth=%d; wait for active mq work to drain the queue or raise [integration].MaxQueueDepth", queuedCount, cfg.Integration.MaxQueueDepth),
				}
			}
		}
	}

	if opts.checkOnly {
		protectedHeadSHA, err := engine.BranchHeadSHA(cfg.Repo.ProtectedBranch)
		if err != nil {
			return preparedSubmission{}, fmt.Errorf("resolve protected branch head for %q: %w", cfg.Repo.ProtectedBranch, err)
		}
		descended, err := engine.IsAncestor(cfg.Repo.ProtectedBranch, sourceRef)
		if err != nil {
			return preparedSubmission{}, err
		}
		if !descended {
			target := sourceRef
			if branch != "" {
				target = fmt.Sprintf("branch %q", branch)
			} else {
				target = fmt.Sprintf("commit %s", sourceRef)
			}
			return preparedSubmission{}, &submitValidationError{
				Code:    "branch_needs_rebase",
				Message: fmt.Sprintf("%s does not include protected branch %q at %s; rebase before submission", target, cfg.Repo.ProtectedBranch, protectedHeadSHA),
			}
		}
	}

	return preparedSubmission{
		Layout:       layout,
		RepoRoot:     repoRoot,
		Config:       cfg,
		Store:        store,
		Branch:       branch,
		SourceRef:    sourceRef,
		RefKind:      refKind,
		WorktreePath: worktree.Path,
		SourceSHA:    headSHA,
		AllowNewer:   opts.allowNewer,
		RequestedBy:  requestedBy,
		Priority:     opts.priority,
	}, nil
}

func isValidSubmissionPriority(priority string) bool {
	switch priority {
	case submissionPriorityHigh, submissionPriorityNormal, submissionPriorityLow:
		return true
	default:
		return false
	}
}

func findActiveDuplicateSubmission(ctx context.Context, store state.Store, repoID int64, sourceRef string, sourceSHA string) (state.IntegrationSubmission, bool, error) {
	submissions, err := store.ListIntegrationSubmissions(ctx, repoID)
	if err != nil {
		return state.IntegrationSubmission{}, false, err
	}
	for _, submission := range submissions {
		if submission.SourceRef != sourceRef || submission.SourceSHA != sourceSHA {
			continue
		}
		switch submission.Status {
		case "queued", "running", "blocked":
			return submission, true, nil
		}
	}
	return state.IntegrationSubmission{}, false, nil
}

func queuePreparedSubmission(prepared preparedSubmission) (queuedSubmission, error) {
	ctx := context.Background()
	repoRecord, err := prepared.Store.GetRepositoryByPath(ctx, prepared.RepoRoot)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			return queuedSubmission{}, err
		}

		repoRecord, err = prepared.Store.UpsertRepository(ctx, state.RepositoryRecord{
			CanonicalPath:   prepared.RepoRoot,
			ProtectedBranch: prepared.Config.Repo.ProtectedBranch,
			RemoteName:      prepared.Config.Repo.RemoteName,
			MainWorktree:    prepared.Config.Repo.MainWorktree,
			PolicyVersion:   "v1",
		})
		if err != nil {
			return queuedSubmission{}, err
		}
	}

	if prepared.Duplicate != nil {
		submission, err := prepared.Store.UpdateIntegrationSubmissionPriority(ctx, prepared.Duplicate.ID, prepared.Priority)
		if err != nil {
			return queuedSubmission{}, err
		}
		payload, err := json.Marshal(map[string]string{
			"branch":            prepared.Branch,
			"source_ref":        prepared.SourceRef,
			"ref_kind":          string(prepared.RefKind),
			"source_worktree":   prepared.WorktreePath,
			"source_sha":        prepared.SourceSHA,
			"allow_newer_head":  fmt.Sprintf("%t", prepared.AllowNewer),
			"requested_by":      prepared.RequestedBy,
			"previous_priority": prepared.Duplicate.Priority,
			"updated_priority":  prepared.Priority,
		})
		if err != nil {
			return queuedSubmission{}, err
		}
		if _, err := prepared.Store.AppendEvent(ctx, state.EventRecord{
			RepoID:    repoRecord.ID,
			ItemType:  domain.ItemTypeIntegrationSubmission,
			ItemID:    state.NullInt64(submission.ID),
			EventType: domain.EventTypeSubmissionReprioritized,
			Payload:   payload,
		}); err != nil {
			return queuedSubmission{}, err
		}

		return queuedSubmission{
			Layout:     prepared.Layout,
			RepoRoot:   prepared.RepoRoot,
			Config:     prepared.Config,
			Store:      prepared.Store,
			RepoRecord: repoRecord,
			Submission: submission,
		}, nil
	}

	submission, err := prepared.Store.CreateIntegrationSubmission(ctx, state.IntegrationSubmission{
		RepoID:         repoRecord.ID,
		BranchName:     prepared.Branch,
		SourceRef:      prepared.SourceRef,
		RefKind:        prepared.RefKind,
		SourceWorktree: prepared.WorktreePath,
		SourceSHA:      prepared.SourceSHA,
		AllowNewerHead: prepared.AllowNewer,
		RequestedBy:    prepared.RequestedBy,
		Priority:       prepared.Priority,
		Status:         domain.SubmissionStatusQueued,
	})
	if err != nil {
		return queuedSubmission{}, err
	}

	payload, err := json.Marshal(map[string]string{
		"branch":           prepared.Branch,
		"source_ref":       prepared.SourceRef,
		"ref_kind":         string(prepared.RefKind),
		"source_worktree":  prepared.WorktreePath,
		"source_sha":       prepared.SourceSHA,
		"allow_newer_head": fmt.Sprintf("%t", prepared.AllowNewer),
		"requested_by":     prepared.RequestedBy,
		"priority":         prepared.Priority,
	})
	if err != nil {
		return queuedSubmission{}, err
	}
	if _, err := prepared.Store.AppendEvent(ctx, state.EventRecord{
		RepoID:    repoRecord.ID,
		ItemType:  domain.ItemTypeIntegrationSubmission,
		ItemID:    state.NullInt64(submission.ID),
		EventType: domain.EventTypeSubmissionCreated,
		Payload:   payload,
	}); err != nil {
		return queuedSubmission{}, err
	}

	return queuedSubmission{
		Layout:     prepared.Layout,
		RepoRoot:   prepared.RepoRoot,
		Config:     prepared.Config,
		Store:      prepared.Store,
		RepoRecord: repoRecord,
		Submission: submission,
	}, nil
}

func writeSubmitJSON(stdout *stepPrinter, result submitResult, cmdErr error) error {
	encoder := json.NewEncoder(stdout.Raw())
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return err
	}
	return cmdErr
}

func humanEstimatedCompletion(ms int64) string {
	if ms <= 0 {
		return ""
	}
	duration := time.Duration(ms) * time.Millisecond
	minutes := int((duration + time.Minute - 1) / time.Minute)
	if minutes < 1 {
		minutes = 1
	}
	return fmt.Sprintf("~%dm", minutes)
}

func submitErrorCode(err error) string {
	var validationErr *submitValidationError
	if errors.As(err, &validationErr) {
		return validationErr.Code
	}
	return "submit_failed"
}

func submitWaitOutcomeCode(outcome waitOutcome) string {
	switch outcome {
	case waitOutcomeTimeout:
		return "timeout"
	case waitOutcomeBlocked:
		return "blocked"
	case waitOutcomeCancelled:
		return "cancelled"
	case waitOutcomeFailed:
		return "failed"
	default:
		return "submit_wait_failed"
	}
}
