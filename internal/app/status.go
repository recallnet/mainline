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
	"github.com/recallnet/mainline/internal/policy"
	"github.com/recallnet/mainline/internal/state"
)

type statusResult struct {
	RepositoryRoot            string                     `json:"repository_root"`
	StatePath                 string                     `json:"state_path"`
	CurrentWorktree           string                     `json:"current_worktree"`
	CurrentBranch             string                     `json:"current_branch"`
	CurrentBranchStatus       *git.BranchComparison      `json:"current_branch_status,omitempty"`
	RebaseGuidance            *statusRebaseGuidance      `json:"rebase_guidance,omitempty"`
	Alerts                    []string                   `json:"alerts,omitempty"`
	QueueSummary              queueSummary               `json:"queue_summary"`
	ProtectedBranch           string                     `json:"protected_branch"`
	ProtectedBranchSHA        string                     `json:"protected_branch_sha"`
	ProtectedUpstream         git.BranchStatus           `json:"protected_upstream"`
	ExecutionEstimate         executionEstimate          `json:"execution_estimate"`
	PublishExecution          publishExecutionPolicy     `json:"publish_execution"`
	Counts                    queueCounts                `json:"counts"`
	LatestSubmission          *statusSubmission          `json:"latest_submission,omitempty"`
	LatestPublish             *statusPublish             `json:"latest_publish,omitempty"`
	ActiveSubmissions         []statusSubmission         `json:"active_submissions,omitempty"`
	ActivePublishes           []statusPublish            `json:"active_publishes,omitempty"`
	IntegrationWorker         *state.LeaseMetadata       `json:"integration_worker,omitempty"`
	PublishWorker             *state.LeaseMetadata       `json:"publish_worker,omitempty"`
	ProtectedWorktreeActivity *protectedWorktreeActivity `json:"protected_worktree_activity,omitempty"`
	RecentEvents              []state.EventRecord        `json:"recent_events"`
}

type statusRebaseGuidance struct {
	NeedsRebase                 bool   `json:"needs_rebase"`
	BaseBranch                  string `json:"base_branch"`
	BaseSHA                     string `json:"base_sha,omitempty"`
	BehindProtectedCount        int    `json:"behind_protected_count,omitempty"`
	AheadProtectedCount         int    `json:"ahead_protected_count,omitempty"`
	Command                     string `json:"command,omitempty"`
	Message                     string `json:"message,omitempty"`
	ProtectedBehindUpstream     bool   `json:"protected_behind_upstream,omitempty"`
	ProtectedBehindUpstreamHint string `json:"protected_behind_upstream_hint,omitempty"`
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
	NextActions           []statusNextAction       `json:"next_actions,omitempty"`
}

type statusNextAction struct {
	Label   string `json:"label"`
	Command string `json:"command,omitempty"`
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
	ActiveStage    string               `json:"active_stage,omitempty"`
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

	enrichedSubmissions, err := enrichStatusSubmissions(ctx, store, repoRecord.ID, cfg.Repo.MainWorktree, cfg.Repo.ProtectedBranch, submissions)
	if err != nil {
		return statusResult{}, err
	}
	estimate, err := collectExecutionEstimate(ctx, store, repoRecord.ID, cfg, submissions)
	if err != nil {
		return statusResult{}, err
	}
	enrichedSubmissions = annotateQueueEstimates(enrichedSubmissions, estimate)
	queue, err := loadQueueSnapshot(store, repoRecord.ID)
	if err != nil {
		return statusResult{}, err
	}

	result := statusResult{
		RepositoryRoot:     repoRecord.CanonicalPath,
		StatePath:          store.Path,
		CurrentWorktree:    layout.WorktreeRoot,
		CurrentBranch:      currentBranch,
		ProtectedBranch:    cfg.Repo.ProtectedBranch,
		ProtectedBranchSHA: protectedSHA,
		ProtectedUpstream:  protectedStatus,
		ExecutionEstimate:  estimate,
		PublishExecution:   buildPublishExecutionPolicy(cfg),
		Counts:             queue.Counts,
		ActiveSubmissions:  activeSubmissions(enrichedSubmissions),
		ActivePublishes:    activePublishes(requests),
		RecentEvents:       events,
	}
	if currentBranch != "(detached)" && currentBranch != "" && currentBranch != cfg.Repo.ProtectedBranch {
		comparison, compareErr := engine.CompareBranches(cfg.Repo.ProtectedBranch, currentBranch)
		if compareErr == nil {
			result.CurrentBranchStatus = &comparison
			result.RebaseGuidance = buildStatusRebaseGuidance(cfg, comparison, protectedStatus, layout.WorktreeRoot, currentBranch)
		}
	}
	result.QueueSummary = queue.Summary
	result.Alerts = buildQueueAlerts(result.Counts)
	lockManager := state.NewLockManager(layout.RepositoryRoot, layout.GitDir)
	if metadata, ok := readActiveLease(lockManager, state.IntegrationLock); ok {
		result.IntegrationWorker = &metadata
	}
	if metadata, ok := readActiveLease(lockManager, state.PublishLock); ok {
		result.PublishWorker = &metadata
	}
	result.ProtectedWorktreeActivity = buildProtectedWorktreeActivity(cfg.Repo.MainWorktree, result.IntegrationWorker, result.PublishWorker)
	if len(enrichedSubmissions) > 0 {
		latest := enrichedSubmissions[len(enrichedSubmissions)-1]
		result.LatestSubmission = &latest
	}
	if len(requests) > 0 {
		latest := newStatusPublish(requests[len(requests)-1])
		if result.PublishWorker != nil && result.PublishWorker.RequestID == latest.ID {
			latest.ActiveStage = result.PublishWorker.Stage
		}
		result.LatestPublish = &latest
	}
	if result.PublishWorker != nil {
		for i := range result.ActivePublishes {
			if result.ActivePublishes[i].ID == result.PublishWorker.RequestID {
				result.ActivePublishes[i].ActiveStage = result.PublishWorker.Stage
			}
		}
	}

	return result, nil
}

func renderStatus(stdout *stepPrinter, result statusResult) error {
	printer := stdout
	printer.Section("Repository status")
	printer.Line("Repository root: %s", result.RepositoryRoot)
	printer.Line("Current worktree: %s", result.CurrentWorktree)
	printer.Line("Current branch: %s", result.CurrentBranch)
	printer.Line("State: %s", result.QueueSummary.Headline)
	printer.Line("Queue length: %d", result.QueueSummary.QueueLength)
	printer.Line("Queue summary: blocked=%t running_publishes=%t running_submissions=%t queued_work=%t",
		result.QueueSummary.HasBlockedSubmissions,
		result.QueueSummary.HasRunningPublishes,
		result.QueueSummary.HasRunningSubmissions,
		result.QueueSummary.HasQueuedWork,
	)
	printer.Line("Protected branch: %s", result.ProtectedBranch)
	printer.Line("Publish execution: configured_hook_policy=%s effective_hook_policy=%s hooks_bypassed_for_push=%t prepare=%t validate=%t",
		result.PublishExecution.ConfiguredHookPolicy,
		result.PublishExecution.EffectiveHookPolicy,
		result.PublishExecution.HooksBypassedForPush,
		result.PublishExecution.PreparePublishEnabled,
		result.PublishExecution.ValidatePublishEnabled,
	)
	if result.ProtectedBranchSHA != "" {
		printer.Line("Protected SHA: %s", result.ProtectedBranchSHA)
	}
	if result.ProtectedUpstream.HasUpstream {
		printer.Line("Protected upstream: %s (ahead %d, behind %d)", result.ProtectedUpstream.Upstream, result.ProtectedUpstream.AheadCount, result.ProtectedUpstream.BehindCount)
	} else {
		printer.Line("Protected upstream: none")
	}
	if result.CurrentBranchStatus != nil {
		printer.Line("Current branch vs protected: ahead %d, behind %d", result.CurrentBranchStatus.AheadCount, result.CurrentBranchStatus.BehindCount)
	}
	if result.RebaseGuidance != nil && result.RebaseGuidance.Message != "" {
		printer.Line("Rebase guidance: %s", result.RebaseGuidance.Message)
		if result.RebaseGuidance.Command != "" {
			printer.Line("Recommended command: %s", result.RebaseGuidance.Command)
		}
		if result.RebaseGuidance.ProtectedBehindUpstreamHint != "" {
			printer.Line("Protected sync hint: %s", result.RebaseGuidance.ProtectedBehindUpstreamHint)
		}
	}
	if len(result.Alerts) > 0 {
		printer.Section("Alerts:")
		for _, alert := range result.Alerts {
			printer.Line("%s", alert)
		}
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
		for _, action := range result.LatestSubmission.NextActions {
			if action.Command != "" {
				printer.Line("%s: %s", action.Label, action.Command)
				continue
			}
			printer.Line("%s", action.Label)
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
		if result.LatestPublish.ActiveStage != "" {
			printer.Line("latest publish stage: %s", result.LatestPublish.ActiveStage)
		}
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
		printer.Line("Publish worker: owner=%s request=%d pid=%d started=%s stage=%s",
			result.PublishWorker.Owner,
			result.PublishWorker.RequestID,
			result.PublishWorker.PID,
			result.PublishWorker.CreatedAt.UTC().Format(time.RFC3339),
			emptyDash(result.PublishWorker.Stage),
		)
	}
	if result.ProtectedWorktreeActivity != nil {
		printer.Line("Protected worktree activity: %s", result.ProtectedWorktreeActivity.Summary)
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

func buildStatusRebaseGuidance(cfg policy.File, comparison git.BranchComparison, protectedStatus git.BranchStatus, repoPath string, branch string) *statusRebaseGuidance {
	guidance := &statusRebaseGuidance{
		NeedsRebase:          comparison.BehindCount > 0,
		BaseBranch:           comparison.BaseBranch,
		BaseSHA:              comparison.BaseHeadSHA,
		BehindProtectedCount: comparison.BehindCount,
		AheadProtectedCount:  comparison.AheadCount,
	}
	if comparison.BehindCount > 0 {
		guidance.Command = fmt.Sprintf("mq rebase --repo %s --branch %s", repoPath, branch)
		upstreamLabel := protectedStatus.Upstream
		if !protectedStatus.HasUpstream || upstreamLabel == "" {
			upstreamLabel = comparison.BaseBranch
		}
		guidance.Message = fmt.Sprintf("current branch is behind local protected branch %q by %d commit(s); rebase onto local %s, not directly onto %s", comparison.BaseBranch, comparison.BehindCount, comparison.BaseBranch, upstreamLabel)
		if protectedStatus.HasUpstream && protectedStatus.BehindCount > 0 {
			guidance.ProtectedBehindUpstream = true
			guidance.ProtectedBehindUpstreamHint = fmt.Sprintf("local protected branch %q is behind %s by %d commit(s); sync protected %s first, then rebase this branch onto local %s", comparison.BaseBranch, protectedStatus.Upstream, protectedStatus.BehindCount, comparison.BaseBranch, comparison.BaseBranch)
		}
		return guidance
	}
	guidance.Message = fmt.Sprintf("current branch already includes local protected branch %q", comparison.BaseBranch)
	return guidance
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

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
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

func enrichStatusSubmissions(ctx context.Context, store state.Store, repoID int64, mainWorktree string, protectedBranch string, submissions []state.IntegrationSubmission) ([]statusSubmission, error) {
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
			item.NextActions = buildBlockedSubmissionActions(item)
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

func buildBlockedSubmissionActions(submission statusSubmission) []statusNextAction {
	if submission.SourceWorktree == "" {
		return nil
	}
	rebaseCommand := fmt.Sprintf("mq rebase --repo %s --submission %d", submission.SourceWorktree, submission.ID)
	retryCommand := fmt.Sprintf("mq retry --submission %d --repo %s", submission.ID, submission.SourceWorktree)
	cancelCommand := fmt.Sprintf("mq cancel --submission %d --repo %s", submission.ID, submission.SourceWorktree)

	switch submission.BlockedReason {
	case domain.BlockedReasonRebaseConflict:
		return []statusNextAction{
			{Label: "Resolve the branch against local protected main", Command: rebaseCommand},
			{Label: "Retry this blocked submission after the rebase resolves", Command: retryCommand},
			{Label: "Cancel this blocked submission if it is obsolete", Command: cancelCommand},
		}
	case domain.BlockedReasonCheckTimeout:
		return []statusNextAction{
			{Label: "Inspect and fix the hanging check in the source worktree", Command: fmt.Sprintf("cd %s", submission.SourceWorktree)},
			{Label: "Retry this blocked submission after fixing the check", Command: retryCommand},
			{Label: "Cancel this blocked submission if it is obsolete", Command: cancelCommand},
		}
	default:
		return []statusNextAction{
			{Label: "Inspect the blocked source worktree", Command: fmt.Sprintf("cd %s", submission.SourceWorktree)},
			{Label: "Retry this blocked submission when ready", Command: retryCommand},
			{Label: "Cancel this blocked submission if it is obsolete", Command: cancelCommand},
		}
	}
}
