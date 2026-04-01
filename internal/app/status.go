package app

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

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
	RepositoryRoot     string                 `json:"repository_root"`
	StatePath          string                 `json:"state_path"`
	CurrentWorktree    string                 `json:"current_worktree"`
	CurrentBranch      string                 `json:"current_branch"`
	ProtectedBranch    string                 `json:"protected_branch"`
	ProtectedBranchSHA string                 `json:"protected_branch_sha"`
	ProtectedUpstream  git.BranchStatus       `json:"protected_upstream"`
	Counts             statusCounts           `json:"counts"`
	LatestSubmission   *statusSubmission      `json:"latest_submission,omitempty"`
	LatestPublish      *state.PublishRequest  `json:"latest_publish,omitempty"`
	ActiveSubmissions  []statusSubmission     `json:"active_submissions,omitempty"`
	ActivePublishes    []state.PublishRequest `json:"active_publishes,omitempty"`
	RecentEvents       []state.EventRecord    `json:"recent_events"`
}

type statusSubmission struct {
	state.IntegrationSubmission
	BlockedReason   string   `json:"blocked_reason,omitempty"`
	ConflictFiles   []string `json:"conflict_files,omitempty"`
	ProtectedTipSHA string   `json:"protected_tip_sha,omitempty"`
	RetryHint       string   `json:"retry_hint,omitempty"`
}

type blockedSubmissionDetails struct {
	Error           string   `json:"error,omitempty"`
	BlockedReason   string   `json:"blocked_reason,omitempty"`
	ConflictFiles   []string `json:"conflict_files,omitempty"`
	ProtectedTipSHA string   `json:"protected_tip_sha,omitempty"`
	RetryHint       string   `json:"retry_hint,omitempty"`
}

func runStatus(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet("mainline status", flag.ContinueOnError)
	fs.SetOutput(stderr)

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
		encoder := json.NewEncoder(stdout)
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

	enrichedSubmissions, err := enrichStatusSubmissions(ctx, store, repoRecord.ID, submissions)
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
		Counts:             summarizeCounts(submissions, requests),
		ActiveSubmissions:  activeSubmissions(enrichedSubmissions),
		ActivePublishes:    activePublishes(requests),
		RecentEvents:       events,
	}
	if len(enrichedSubmissions) > 0 {
		latest := enrichedSubmissions[len(enrichedSubmissions)-1]
		result.LatestSubmission = &latest
	}
	if len(requests) > 0 {
		result.LatestPublish = &requests[len(requests)-1]
	}

	return result, nil
}

func renderStatus(stdout io.Writer, result statusResult) error {
	fmt.Fprintf(stdout, "Repository root: %s\n", result.RepositoryRoot)
	fmt.Fprintf(stdout, "Current worktree: %s\n", result.CurrentWorktree)
	fmt.Fprintf(stdout, "Current branch: %s\n", result.CurrentBranch)
	fmt.Fprintf(stdout, "Protected branch: %s\n", result.ProtectedBranch)
	if result.ProtectedBranchSHA != "" {
		fmt.Fprintf(stdout, "Protected SHA: %s\n", result.ProtectedBranchSHA)
	}
	if result.ProtectedUpstream.HasUpstream {
		fmt.Fprintf(stdout, "Protected upstream: %s (ahead %d, behind %d)\n", result.ProtectedUpstream.Upstream, result.ProtectedUpstream.AheadCount, result.ProtectedUpstream.BehindCount)
	} else {
		fmt.Fprintln(stdout, "Protected upstream: none")
	}
	fmt.Fprintf(stdout, "Queue: submissions queued=%d running=%d blocked=%d failed=%d cancelled=%d | publishes queued=%d running=%d failed=%d cancelled=%d succeeded=%d\n",
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
	if result.LatestSubmission != nil {
		fmt.Fprintf(stdout, "Latest submission: #%d %s from %s (%s, priority=%s)\n",
			result.LatestSubmission.ID,
			submissionDisplayRef(result.LatestSubmission.IntegrationSubmission),
			result.LatestSubmission.SourceWorktree,
			result.LatestSubmission.Status,
			result.LatestSubmission.Priority,
		)
		if result.LatestSubmission.LastError != "" {
			fmt.Fprintf(stdout, "  last error: %s\n", result.LatestSubmission.LastError)
		}
		if len(result.LatestSubmission.ConflictFiles) > 0 {
			fmt.Fprintf(stdout, "  conflict files: %s\n", strings.Join(result.LatestSubmission.ConflictFiles, ", "))
		}
		if result.LatestSubmission.ProtectedTipSHA != "" {
			fmt.Fprintf(stdout, "  protected tip: %s\n", result.LatestSubmission.ProtectedTipSHA)
		}
		if result.LatestSubmission.RetryHint != "" {
			fmt.Fprintf(stdout, "  retry hint: %s\n", result.LatestSubmission.RetryHint)
		}
	} else {
		fmt.Fprintln(stdout, "Latest submission: none")
	}
	if result.LatestPublish != nil {
		fmt.Fprintf(stdout, "Latest publish: #%d %s (%s)\n",
			result.LatestPublish.ID,
			result.LatestPublish.TargetSHA,
			result.LatestPublish.Status,
		)
	} else {
		fmt.Fprintln(stdout, "Latest publish: none")
	}
	if len(result.ActiveSubmissions) > 0 {
		fmt.Fprintln(stdout, "Active submissions:")
		for _, submission := range result.ActiveSubmissions {
			fmt.Fprintf(stdout, "  #%d %s (%s)\n", submission.ID, submissionDisplayRef(submission.IntegrationSubmission), submission.Status)
		}
	}
	if len(result.ActivePublishes) > 0 {
		fmt.Fprintln(stdout, "Active publishes:")
		for _, request := range result.ActivePublishes {
			fmt.Fprintf(stdout, "  #%d %s (%s)\n", request.ID, request.TargetSHA, request.Status)
		}
	}
	if len(result.RecentEvents) == 0 {
		fmt.Fprintln(stdout, "Recent events: none")
		return nil
	}
	fmt.Fprintln(stdout, "Recent events:")
	for _, event := range result.RecentEvents {
		fmt.Fprintf(stdout, "  %s  %s", event.CreatedAt.UTC().Format(time.RFC3339), event.EventType)
		payload := strings.TrimSpace(string(event.Payload))
		if payload != "" && payload != "{}" {
			fmt.Fprintf(stdout, "  %s", payload)
		}
		fmt.Fprintln(stdout)
	}

	return nil
}

func activeSubmissions(submissions []statusSubmission) []statusSubmission {
	var active []statusSubmission
	for _, submission := range submissions {
		switch submission.Status {
		case "queued", "running", "blocked":
			active = append(active, submission)
		}
	}
	return active
}

func enrichStatusSubmissions(ctx context.Context, store state.Store, repoID int64, submissions []state.IntegrationSubmission) ([]statusSubmission, error) {
	enriched := make([]statusSubmission, 0, len(submissions))
	for _, submission := range submissions {
		item := statusSubmission{IntegrationSubmission: submission}
		if submission.Status == "blocked" {
			details, err := latestBlockedSubmissionDetails(ctx, store, repoID, submission.ID)
			if err != nil {
				return nil, err
			}
			item.BlockedReason = details.BlockedReason
			item.ConflictFiles = details.ConflictFiles
			item.ProtectedTipSHA = details.ProtectedTipSHA
			item.RetryHint = details.RetryHint
		}
		enriched = append(enriched, item)
	}
	return enriched, nil
}

func latestBlockedSubmissionDetails(ctx context.Context, store state.Store, repoID int64, submissionID int64) (blockedSubmissionDetails, error) {
	events, err := store.ListEventsForItem(ctx, repoID, "integration_submission", submissionID, 10)
	if err != nil {
		return blockedSubmissionDetails{}, err
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].EventType != "integration.blocked" {
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

func activePublishes(requests []state.PublishRequest) []state.PublishRequest {
	var active []state.PublishRequest
	for _, request := range requests {
		switch request.Status {
		case "queued", "running":
			active = append(active, request)
		}
	}
	return active
}

func summarizeCounts(submissions []state.IntegrationSubmission, requests []state.PublishRequest) statusCounts {
	var counts statusCounts
	for _, submission := range submissions {
		switch submission.Status {
		case "queued":
			counts.QueuedSubmissions++
		case "running":
			counts.RunningSubmissions++
		case "blocked":
			counts.BlockSubmissions++
		case "failed":
			counts.FailedSubmissions++
		case "cancelled":
			counts.CancelledSubmissions++
		}
	}
	for _, request := range requests {
		switch request.Status {
		case "queued":
			counts.QueuedPublishes++
		case "running":
			counts.RunningPublishes++
		case "failed":
			counts.FailedPublishes++
		case "cancelled":
			counts.CancelledPublishes++
		case "succeeded":
			counts.SucceededPublishes++
		}
	}
	return counts
}
