package app

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/recallnet/mainline/internal/domain"
	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/state"
)

type statusCounts struct {
	QueuedSubmissions    int `json:"queued_submissions"`
	RunningSubmissions   int `json:"running_submissions"`
	BlockSubmissions     int `json:"blocked_submissions"`
	FailedSubmissions    int `json:"failed_submissions"`
	CancelledSubmissions int `json:"cancelled_submissions"`
	QueuedPublishes      int `json:"queued_publishes"`
	RunningPublishes     int `json:"running_publishes"`
	FailedPublishes      int `json:"failed_publishes"`
	CancelledPublishes   int `json:"cancelled_publishes"`
	SucceededPublishes   int `json:"succeeded_publishes"`
}

type statusResult struct {
	RepositoryRoot     string               `json:"repository_root"`
	StatePath          string               `json:"state_path"`
	CurrentWorktree    string               `json:"current_worktree"`
	CurrentBranch      string               `json:"current_branch"`
	State              string               `json:"state"`
	QueueLength        int                  `json:"queue_length"`
	ProtectedBranch    string               `json:"protected_branch"`
	ProtectedBranchSHA string               `json:"protected_branch_sha"`
	ProtectedUpstream  git.BranchStatus     `json:"protected_upstream"`
	ExecutionEstimate  executionEstimate    `json:"execution_estimate"`
	Counts             statusCounts         `json:"counts"`
	LatestSubmission   *statusSubmission    `json:"latest_submission,omitempty"`
	LatestPublish      *statusPublish       `json:"latest_publish,omitempty"`
	ActiveSubmissions  []statusSubmission   `json:"active_submissions,omitempty"`
	ActivePublishes    []statusPublish      `json:"active_publishes,omitempty"`
	IntegrationWorker  *state.LeaseMetadata `json:"integration_worker,omitempty"`
	PublishWorker      *state.LeaseMetadata `json:"publish_worker,omitempty"`
	RecentEvents       []state.EventRecord  `json:"recent_events"`
}

type statusSubmission struct {
	ID                    int64                    `json:"id"`
	RepoID                int64                    `json:"repo_id"`
	BranchName            string                   `json:"branch_name"`
	SourceRef             string                   `json:"source_ref"`
	RefKind               domain.RefKind           `json:"ref_kind"`
	SourceWorktree        string                   `json:"source_worktree_path"`
	SourceSHA             string                   `json:"source_sha"`
	AllowNewerHead        bool                     `json:"allow_newer_head,omitempty"`
	RequestedBy           string                   `json:"requested_by"`
	Priority              string                   `json:"priority"`
	Status                domain.SubmissionStatus  `json:"status"`
	LastError             string                   `json:"last_error"`
	CreatedAt             time.Time                `json:"created_at"`
	UpdatedAt             time.Time                `json:"updated_at"`
	PublishRequestID      int64                    `json:"publish_request_id,omitempty"`
	PublishStatus         domain.PublishStatus     `json:"publish_status,omitempty"`
	Outcome               domain.SubmissionOutcome `json:"outcome,omitempty"`
	QueuePosition         int                      `json:"queue_position,omitempty"`
	EstimatedCompletionMS int64                    `json:"estimated_completion_ms,omitempty"`
	EstimateBasis         domain.SubmissionOutcome `json:"estimate_basis,omitempty"`
	BlockedReason         domain.BlockedReason     `json:"blocked_reason,omitempty"`
	ConflictFiles         []string                 `json:"conflict_files,omitempty"`
	ProtectedTipSHA       string                   `json:"protected_tip_sha,omitempty"`
	RetryHint             string                   `json:"retry_hint,omitempty"`
	PublishFailureCause   string                   `json:"publish_failure_cause,omitempty"`
	PublishFailureSummary string                   `json:"publish_failure_summary,omitempty"`
	PublishFailureError   string                   `json:"publish_failure_error,omitempty"`
}

type blockedSubmissionDetails struct {
	Error           string               `json:"error,omitempty"`
	BlockedReason   domain.BlockedReason `json:"blocked_reason,omitempty"`
	ConflictFiles   []string             `json:"conflict_files,omitempty"`
	ProtectedTipSHA string               `json:"protected_tip_sha,omitempty"`
	RetryHint       string               `json:"retry_hint,omitempty"`
}

type statusPublish struct {
	ID             int64                `json:"id"`
	RepoID         int64                `json:"repo_id"`
	TargetSHA      string               `json:"target_sha"`
	Status         domain.PublishStatus `json:"status"`
	AttemptCount   int                  `json:"attempt_count"`
	NextAttemptAt  time.Time            `json:"next_attempt_at,omitempty"`
	HasNextAttempt bool                 `json:"has_next_attempt,omitempty"`
	SupersededBy   int64                `json:"superseded_by,omitempty"`
	CreatedAt      time.Time            `json:"created_at"`
	UpdatedAt      time.Time            `json:"updated_at"`
}

func newStatusSubmission(submission state.IntegrationSubmission) statusSubmission {
	return statusSubmission{
		ID:             submission.ID,
		RepoID:         submission.RepoID,
		BranchName:     submission.BranchName,
		SourceRef:      submission.SourceRef,
		RefKind:        submission.RefKind,
		SourceWorktree: submission.SourceWorktree,
		SourceSHA:      submission.SourceSHA,
		AllowNewerHead: submission.AllowNewerHead,
		RequestedBy:    submission.RequestedBy,
		Priority:       submission.Priority,
		Status:         submission.Status,
		LastError:      submission.LastError,
		CreatedAt:      submission.CreatedAt,
		UpdatedAt:      submission.UpdatedAt,
	}
}

func (s statusSubmission) integrationSubmission() state.IntegrationSubmission {
	return state.IntegrationSubmission{
		ID:             s.ID,
		RepoID:         s.RepoID,
		BranchName:     s.BranchName,
		SourceRef:      s.SourceRef,
		RefKind:        s.RefKind,
		SourceWorktree: s.SourceWorktree,
		SourceSHA:      s.SourceSHA,
		AllowNewerHead: s.AllowNewerHead,
		RequestedBy:    s.RequestedBy,
		Priority:       s.Priority,
		Status:         s.Status,
		LastError:      s.LastError,
		CreatedAt:      s.CreatedAt,
		UpdatedAt:      s.UpdatedAt,
	}
}

func newStatusPublish(request state.PublishRequest) statusPublish {
	result := statusPublish{
		ID:           request.ID,
		RepoID:       request.RepoID,
		TargetSHA:    request.TargetSHA,
		Status:       request.Status,
		AttemptCount: request.AttemptCount,
		CreatedAt:    request.CreatedAt,
		UpdatedAt:    request.UpdatedAt,
	}
	if request.NextAttemptAt.Valid {
		result.HasNextAttempt = true
		result.NextAttemptAt = request.NextAttemptAt.Time
	}
	if request.SupersededBy.Valid {
		result.SupersededBy = request.SupersededBy.Int64
	}
	return result
}

func runStatus(args []string, stdout *stepPrinter, stderr io.Writer) error {
	fs := flag.NewFlagSet(currentCLIProgramName()+" status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	setFlagUsage(fs, fmt.Sprintf(`Usage:
  %s status [flags]

Show protected-branch state, queue counts, active work, and recent durable
events.

Examples:
  mq status --json
  mq status --repo /path/to/repo-root --json --events 10

Flags:
`, currentCLIProgramName()))

	var repoPath string
	var asJSON bool
	var limit int

	fs.StringVar(&repoPath, "repo", ".", "repository path")
	fs.BoolVar(&asJSON, "json", false, "output json")
	fs.IntVar(&limit, "events", 5, "number of recent events to show")

	if err := fs.Parse(args); err != nil {
		return err
	}

	result, err := collectStatus(repoPath, limit)
	if err != nil {
		return err
	}

	if asJSON {
		encoder := json.NewEncoder(stdout.Raw())
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	return renderStatus(stdout, result)
}

func collectStatus(repoPath string, limit int) (statusResult, error) {
	layout, _, cfg, repoRecord, store, err := loadRepoContext(repoPath)
	if err != nil {
		return statusResult{}, err
	}

	engine := git.NewEngine(layout.WorktreeRoot)
	worktree, err := engine.ResolveWorktree(layout.WorktreeRoot)
	if err != nil {
		return statusResult{}, err
	}

	currentBranch := worktree.Branch
	if worktree.IsDetached || currentBranch == "" {
		currentBranch = "(detached)"
	}

	protectedSHA, err := engine.BranchHeadSHA(cfg.Repo.ProtectedBranch)
	if err != nil {
		if !engine.BranchExists(cfg.Repo.ProtectedBranch) {
			protectedSHA = ""
		} else {
			return statusResult{}, err
		}
	}

	protectedStatus, err := engine.BranchStatus(cfg.Repo.ProtectedBranch, cfg.Repo.ProtectedBranch)
	if err != nil && engine.BranchExists(cfg.Repo.ProtectedBranch) {
		return statusResult{}, err
	}

	ctx := context.Background()
	submissions, err := store.ListIntegrationSubmissions(ctx, repoRecord.ID)
	if err != nil {
		return statusResult{}, err
	}
	requests, err := store.ListPublishRequests(ctx, repoRecord.ID)
	if err != nil {
		return statusResult{}, err
	}
	events, err := store.ListEvents(ctx, repoRecord.ID, limit)
	if err != nil {
		return statusResult{}, err
	}

	enrichedSubmissions, err := enrichStatusSubmissions(ctx, store, repoRecord.ID, cfg.Repo.MainWorktree, submissions)
	if err != nil {
		return statusResult{}, err
	}
	estimate, err := collectExecutionEstimate(ctx, store, repoRecord.ID, cfg, submissions)
	if err != nil {
		return statusResult{}, err
	}
	enrichedSubmissions = annotateQueueEstimates(enrichedSubmissions, estimate)

	result := statusResult{
		RepositoryRoot:     repoRecord.CanonicalPath,
		StatePath:          store.Path,
		CurrentWorktree:    layout.WorktreeRoot,
		CurrentBranch:      currentBranch,
		ProtectedBranch:    cfg.Repo.ProtectedBranch,
		ProtectedBranchSHA: protectedSHA,
		ProtectedUpstream:  protectedStatus,
		ExecutionEstimate:  estimate,
		Counts:             summarizeCounts(submissions, requests),
		ActiveSubmissions:  activeSubmissions(enrichedSubmissions),
		ActivePublishes:    activePublishes(requests),
		RecentEvents:       events,
	}
	result.State, result.QueueLength = summarizeQueueState(result.Counts)
	lockManager := state.NewLockManager(layout.RepositoryRoot, layout.GitDir)
	if metadata, ok := readActiveLease(lockManager, state.IntegrationLock); ok {
		result.IntegrationWorker = &metadata
	}
	if metadata, ok := readActiveLease(lockManager, state.PublishLock); ok {
		result.PublishWorker = &metadata
	}
	if len(enrichedSubmissions) > 0 {
		latest := enrichedSubmissions[len(enrichedSubmissions)-1]
		result.LatestSubmission = &latest
	}
	if len(requests) > 0 {
		latest := newStatusPublish(requests[len(requests)-1])
		result.LatestPublish = &latest
	}

	return result, nil
}

func renderStatus(stdout *stepPrinter, result statusResult) error {
	printer := stdout
	printer.Section("Repository status")
	printer.Line("Repository root: %s", result.RepositoryRoot)
	printer.Line("Current worktree: %s", result.CurrentWorktree)
	printer.Line("Current branch: %s", result.CurrentBranch)
	printer.Line("State: %s", result.State)
	printer.Line("Queue length: %d", result.QueueLength)
	printer.Line("Protected branch: %s", result.ProtectedBranch)
	if result.ProtectedBranchSHA != "" {
		printer.Line("Protected SHA: %s", result.ProtectedBranchSHA)
	}
	if result.ProtectedUpstream.HasUpstream {
		printer.Line("Protected upstream: %s (ahead %d, behind %d)", result.ProtectedUpstream.Upstream, result.ProtectedUpstream.AheadCount, result.ProtectedUpstream.BehindCount)
	} else {
		printer.Line("Protected upstream: none")
	}
	printer.Line("Queue: submissions queued=%d running=%d blocked=%d failed=%d cancelled=%d | publishes queued=%d running=%d failed=%d cancelled=%d succeeded=%d",
		result.Counts.QueuedSubmissions,
		result.Counts.RunningSubmissions,
		result.Counts.BlockSubmissions,
		result.Counts.FailedSubmissions,
		result.Counts.CancelledSubmissions,
		result.Counts.QueuedPublishes,
		result.Counts.RunningPublishes,
		result.Counts.FailedPublishes,
		result.Counts.CancelledPublishes,
		result.Counts.SucceededPublishes,
	)
	if result.ExecutionEstimate.AvgExecutionMS > 0 {
		printer.Line("Execution estimate (24h rolling): basis=%s avg=%s samples=%d",
			result.ExecutionEstimate.Basis,
			(time.Duration(result.ExecutionEstimate.AvgExecutionMS) * time.Millisecond).Round(time.Millisecond),
			result.ExecutionEstimate.SampleCount,
		)
	}
	if result.LatestSubmission != nil {
		printer.Section("Latest submission:")
		printer.Line("#%d %s from %s (%s, priority=%s)",
			result.LatestSubmission.ID,
			submissionDisplayRef(result.LatestSubmission.integrationSubmission()),
			result.LatestSubmission.SourceWorktree,
			result.LatestSubmission.Status,
			result.LatestSubmission.Priority,
		)
		if result.LatestSubmission.LastError != "" {
			printer.Line("last error: %s", result.LatestSubmission.LastError)
		}
		if len(result.LatestSubmission.ConflictFiles) > 0 {
			printer.Line("conflict files: %s", strings.Join(result.LatestSubmission.ConflictFiles, ", "))
		}
		if result.LatestSubmission.ProtectedTipSHA != "" {
			printer.Line("protected tip: %s", result.LatestSubmission.ProtectedTipSHA)
		}
		if result.LatestSubmission.RetryHint != "" {
			printer.Line("retry hint: %s", result.LatestSubmission.RetryHint)
		}
		if result.LatestSubmission.PublishFailureSummary != "" {
			printer.Line("publish failure: %s", result.LatestSubmission.PublishFailureSummary)
		}
		if result.LatestSubmission.QueuePosition > 0 && result.LatestSubmission.EstimatedCompletionMS > 0 {
			printer.Line("queue position: %d", result.LatestSubmission.QueuePosition)
			printer.Line("estimated completion: %s (%s basis)",
				(time.Duration(result.LatestSubmission.EstimatedCompletionMS) * time.Millisecond).Round(time.Millisecond),
				result.LatestSubmission.EstimateBasis,
			)
		}
	} else {
		printer.Line("Latest submission: none")
	}
	if result.LatestPublish != nil {
		printer.Line("Latest publish: #%d %s (%s)",
			result.LatestPublish.ID,
			result.LatestPublish.TargetSHA,
			result.LatestPublish.Status,
		)
	} else {
		printer.Line("Latest publish: none")
	}
	if result.IntegrationWorker != nil {
		printer.Line("Integration worker: owner=%s request=%d pid=%d started=%s",
			result.IntegrationWorker.Owner,
			result.IntegrationWorker.RequestID,
			result.IntegrationWorker.PID,
			result.IntegrationWorker.CreatedAt.UTC().Format(time.RFC3339),
		)
	}
	if result.PublishWorker != nil {
		printer.Line("Publish worker: owner=%s request=%d pid=%d started=%s",
			result.PublishWorker.Owner,
			result.PublishWorker.RequestID,
			result.PublishWorker.PID,
			result.PublishWorker.CreatedAt.UTC().Format(time.RFC3339),
		)
	}
	if len(result.ActiveSubmissions) > 0 {
		printer.Section("Active submissions:")
		for _, submission := range result.ActiveSubmissions {
			printer.Line("#%d %s (%s)", submission.ID, submissionDisplayRef(submission.integrationSubmission()), submission.Status)
		}
	}
	if len(result.ActivePublishes) > 0 {
		printer.Section("Active publishes:")
		for _, request := range result.ActivePublishes {
			printer.Line("#%d %s (%s)", request.ID, request.TargetSHA, request.Status)
		}
	}
	if len(result.RecentEvents) == 0 {
		printer.Line("Recent events: none")
		return nil
	}
	printer.Section("Recent events:")
	for _, event := range result.RecentEvents {
		line := fmt.Sprintf("%s  %s", event.CreatedAt.UTC().Format(time.RFC3339), event.EventType)
		payload := strings.TrimSpace(string(event.Payload))
		if payload != "" && payload != "{}" {
			line = fmt.Sprintf("%s  %s", line, payload)
		}
		printer.Line("%s", line)
	}

	return nil
}

func readActiveLease(lockManager state.LockManager, domain string) (state.LeaseMetadata, bool) {
	metadata, err := lockManager.Metadata(domain)
	if err != nil {
		if os.IsNotExist(err) {
			return state.LeaseMetadata{}, false
		}
		return state.LeaseMetadata{}, false
	}
	return metadata, true
}

func activeSubmissions(submissions []statusSubmission) []statusSubmission {
	var active []statusSubmission
	for _, submission := range submissions {
		switch submission.Status {
		case domain.SubmissionStatusQueued, domain.SubmissionStatusRunning, domain.SubmissionStatusBlocked:
			active = append(active, submission)
		}
	}
	return active
}

func enrichStatusSubmissions(ctx context.Context, store state.Store, repoID int64, mainWorktree string, submissions []state.IntegrationSubmission) ([]statusSubmission, error) {
	mainEngine := git.NewEngine(mainWorktree)
	enriched := make([]statusSubmission, 0, len(submissions))
	for _, submission := range submissions {
		item := newStatusSubmission(submission)
		if submission.Status == domain.SubmissionStatusBlocked {
			details, err := latestBlockedSubmissionDetails(ctx, store, repoID, submission.ID)
			if err != nil {
				return nil, err
			}
			item.BlockedReason = details.BlockedReason
			item.ConflictFiles = details.ConflictFiles
			item.ProtectedTipSHA = details.ProtectedTipSHA
			item.RetryHint = details.RetryHint
		}
		if submission.Status == domain.SubmissionStatusSucceeded {
			info, err := resolveSubmissionPublishInfo(ctx, store, repoID, submission, mainEngine)
			if err != nil {
				return nil, err
			}
			item.ProtectedTipSHA = info.ProtectedSHA
			item.PublishRequestID = info.PublishRequestID
			item.PublishStatus = domain.PublishStatus(info.PublishStatus)
			item.Outcome = info.Outcome
			item.PublishFailureCause = info.Failure.Cause
			item.PublishFailureSummary = info.Failure.Summary
			item.PublishFailureError = info.Failure.Error
			if item.RetryHint == "" {
				item.RetryHint = info.Failure.RetryHint
			}
		}
		enriched = append(enriched, item)
	}
	return enriched, nil
}

func latestBlockedSubmissionDetails(ctx context.Context, store state.Store, repoID int64, submissionID int64) (blockedSubmissionDetails, error) {
	events, err := store.ListEventsForItem(ctx, repoID, string(domain.ItemTypeIntegrationSubmission), submissionID, 10)
	if err != nil {
		return blockedSubmissionDetails{}, err
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].EventType != domain.EventTypeIntegrationBlocked {
			continue
		}
		var details blockedSubmissionDetails
		if len(events[i].Payload) == 0 {
			return details, nil
		}
		if err := json.Unmarshal(events[i].Payload, &details); err != nil {
			return blockedSubmissionDetails{}, err
		}
		return details, nil
	}
	return blockedSubmissionDetails{}, nil
}

func activePublishes(requests []state.PublishRequest) []statusPublish {
	var active []statusPublish
	for _, request := range requests {
		switch request.Status {
		case domain.PublishStatusQueued, domain.PublishStatusRunning:
			active = append(active, newStatusPublish(request))
		}
	}
	return active
}

func summarizeCounts(submissions []state.IntegrationSubmission, requests []state.PublishRequest) statusCounts {
	var counts statusCounts
	for _, submission := range submissions {
		switch submission.Status {
		case domain.SubmissionStatusQueued:
			counts.QueuedSubmissions++
		case domain.SubmissionStatusRunning:
			counts.RunningSubmissions++
		case domain.SubmissionStatusBlocked:
			counts.BlockSubmissions++
		case domain.SubmissionStatusFailed:
			counts.FailedSubmissions++
		case domain.SubmissionStatusCancelled:
			counts.CancelledSubmissions++
		case domain.SubmissionStatusSuperseded:
			// terminal and intentionally omitted from active queue counts
		}
	}
	for _, request := range requests {
		switch request.Status {
		case domain.PublishStatusQueued:
			counts.QueuedPublishes++
		case domain.PublishStatusRunning:
			counts.RunningPublishes++
		case domain.PublishStatusFailed:
			counts.FailedPublishes++
		case domain.PublishStatusCancelled:
			counts.CancelledPublishes++
		case domain.PublishStatusSucceeded:
			counts.SucceededPublishes++
		}
	}
	return counts
}

func summarizeQueueState(counts statusCounts) (string, int) {
	queueLength := counts.QueuedSubmissions +
		counts.RunningSubmissions +
		counts.BlockSubmissions +
		counts.QueuedPublishes +
		counts.RunningPublishes
	switch {
	case counts.BlockSubmissions > 0:
		return "blocked", queueLength
	case counts.RunningPublishes > 0:
		return "publishing", queueLength
	case counts.RunningSubmissions > 0:
		return "integrating", queueLength
	case counts.QueuedSubmissions > 0 || counts.QueuedPublishes > 0:
		return "queued", queueLength
	default:
		return "idle", 0
	}
}
