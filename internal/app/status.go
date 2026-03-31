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
	RepositoryRoot     string                       `json:"repository_root"`
	StatePath          string                       `json:"state_path"`
	CurrentWorktree    string                       `json:"current_worktree"`
	CurrentBranch      string                       `json:"current_branch"`
	ProtectedBranch    string                       `json:"protected_branch"`
	ProtectedBranchSHA string                       `json:"protected_branch_sha"`
	ProtectedUpstream  git.BranchStatus             `json:"protected_upstream"`
	Counts             statusCounts                 `json:"counts"`
	LatestSubmission   *state.IntegrationSubmission `json:"latest_submission,omitempty"`
	LatestPublish      *state.PublishRequest        `json:"latest_publish,omitempty"`
	RecentEvents       []state.EventRecord          `json:"recent_events"`
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

	layout, _, cfg, repoRecord, store, err := loadRepoContext(repoPath)
	if err != nil {
		return err
	}

	engine := git.NewEngine(layout.WorktreeRoot)
	worktree, err := engine.ResolveWorktree(layout.WorktreeRoot)
	if err != nil {
		return err
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
			return err
		}
	}

	protectedStatus, err := engine.BranchStatus(cfg.Repo.ProtectedBranch, cfg.Repo.ProtectedBranch)
	if err != nil && engine.BranchExists(cfg.Repo.ProtectedBranch) {
		return err
	}

	ctx := context.Background()
	submissions, err := store.ListIntegrationSubmissions(ctx, repoRecord.ID)
	if err != nil {
		return err
	}
	requests, err := store.ListPublishRequests(ctx, repoRecord.ID)
	if err != nil {
		return err
	}
	events, err := store.ListEvents(ctx, repoRecord.ID, limit)
	if err != nil {
		return err
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
		RecentEvents:       events,
	}
	if len(submissions) > 0 {
		result.LatestSubmission = &submissions[len(submissions)-1]
	}
	if len(requests) > 0 {
		result.LatestPublish = &requests[len(requests)-1]
	}

	if asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

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
		fmt.Fprintf(stdout, "Latest submission: #%d %s from %s (%s)\n",
			result.LatestSubmission.ID,
			result.LatestSubmission.BranchName,
			result.LatestSubmission.SourceWorktree,
			result.LatestSubmission.Status,
		)
		if result.LatestSubmission.LastError != "" {
			fmt.Fprintf(stdout, "  last error: %s\n", result.LatestSubmission.LastError)
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
