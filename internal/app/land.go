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
	"github.com/recallnet/mainline/internal/state"
)

type landResult struct {
	SubmissionID     int64                   `json:"submission_id"`
	PublishRequestID int64                   `json:"publish_request_id,omitempty"`
	Branch           string                  `json:"branch"`
	SourceRef        string                  `json:"source_ref,omitempty"`
	RefKind          domain.RefKind          `json:"ref_kind,omitempty"`
	SourceWorktree   string                  `json:"source_worktree"`
	SourceSHA        string                  `json:"source_sha"`
	AllowNewerHead   bool                    `json:"allow_newer_head,omitempty"`
	Priority         string                  `json:"priority,omitempty"`
	RepositoryRoot   string                  `json:"repository_root"`
	MainWorktree     string                  `json:"main_worktree"`
	ProtectedBranch  string                  `json:"protected_branch"`
	ProtectedSHA     string                  `json:"protected_sha,omitempty"`
	SubmissionStatus domain.SubmissionStatus `json:"submission_status"`
	PublishStatus    domain.PublishStatus    `json:"publish_status,omitempty"`
	Published        bool                    `json:"published"`
	DurationMS       int64                   `json:"duration_ms"`
	LastWorkerResult string                  `json:"last_worker_result,omitempty"`
	Error            string                  `json:"error,omitempty"`
}

func runLand(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet(currentCLIProgramName()+" land", flag.ContinueOnError)
	fs.SetOutput(stderr)
	setFlagUsage(fs, fmt.Sprintf(`Usage:
  %s land [flags]

Submit a topic worktree and wait until it is integrated and published.

Best for controller agents and factory daemons that want one command in and one
final outcome out.

Examples:
  mq land --json --timeout 30m
  mq land --allow-newer-head --json --timeout 30m
  mq land --repo /path/to/topic-worktree --json

Flags:
`, currentCLIProgramName()))

	var repoPath string
	var branch string
	var sha string
	var worktreePath string
	var requestedBy string
	var priority string
	var asJSON bool
	var timeout time.Duration
	var pollInterval time.Duration
	var allowNewerHead bool

	fs.StringVar(&repoPath, "repo", ".", "source worktree path")
	fs.StringVar(&branch, "branch", "", "branch to submit")
	fs.StringVar(&sha, "sha", "", "detached commit to submit")
	fs.StringVar(&worktreePath, "worktree", "", "source worktree path override")
	fs.StringVar(&requestedBy, "requested-by", "", "submitter identity")
	fs.StringVar(&priority, "priority", submissionPriorityNormal, "submission priority: high, normal, or low")
	fs.BoolVar(&allowNewerHead, "allow-newer-head", false, "allow a queued branch head to advance before integration if it remains a descendant of the submitted sha")
	fs.BoolVar(&asJSON, "json", false, "output json")
	fs.DurationVar(&timeout, "timeout", 30*time.Minute, "maximum time to wait for integrate+publish")
	fs.DurationVar(&pollInterval, "poll-interval", 500*time.Millisecond, "wait interval between worker checks")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if timeout <= 0 {
		return fmt.Errorf("timeout must be greater than zero")
	}
	if pollInterval <= 0 {
		return fmt.Errorf("poll-interval must be greater than zero")
	}
	if !isValidSubmissionPriority(priority) {
		return fmt.Errorf("priority must be one of %q, %q, or %q", submissionPriorityHigh, submissionPriorityNormal, submissionPriorityLow)
	}
	if err := validateLandPreflight(repoPath); err != nil {
		return err
	}

	start := time.Now()
	queued, err := queueSubmission(submitOptions{
		repoPath:     repoPath,
		branch:       branch,
		sha:          sha,
		worktreePath: worktreePath,
		requestedBy:  requestedBy,
		priority:     priority,
		allowNewer:   allowNewerHead,
	})
	if err != nil {
		return err
	}

	result, waitErr := waitForLandedPublish(queued, timeout, pollInterval)
	result.DurationMS = time.Since(start).Milliseconds()

	if asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(stdout, "Queued submission %d\n", result.SubmissionID)
		fmt.Fprintf(stdout, "Branch: %s\n", result.Branch)
		fmt.Fprintf(stdout, "Worktree: %s\n", result.SourceWorktree)
		fmt.Fprintf(stdout, "Source SHA: %s\n", result.SourceSHA)
		fmt.Fprintf(stdout, "Priority: %s\n", result.Priority)
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
		fmt.Fprintf(stdout, "Published: %t\n", result.Published)
		fmt.Fprintf(stdout, "Duration: %s\n", time.Since(start).Round(time.Millisecond))
	}

	return waitErr
}

func validateLandPreflight(repoPath string) error {
	layout, repoRoot, cfg, repoRecord, store, err := loadRepoContext(repoPath)
	if err != nil {
		return err
	}

	mainLayout, err := git.DiscoverRepositoryLayout(cfg.Repo.MainWorktree)
	if err != nil {
		return err
	}
	if mainLayout.GitDir != layout.GitDir {
		return fmt.Errorf("main worktree %s does not belong to repository %s", cfg.Repo.MainWorktree, repoRoot)
	}

	if _, err := ensureProtectedRootHealthy(
		context.Background(),
		git.NewEngine(mainLayout.WorktreeRoot),
		cfg,
		store,
		repoRecord,
		protectedRootRecoveryAllowQueued,
	); err != nil {
		return err
	}

	return nil
}

func waitForLandedPublish(queued queuedSubmission, timeout time.Duration, pollInterval time.Duration) (landResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	result := landResult{
		SubmissionID:     queued.Submission.ID,
		Branch:           submissionDisplayRef(queued.Submission),
		SourceRef:        queued.Submission.SourceRef,
		RefKind:          queued.Submission.RefKind,
		SourceWorktree:   queued.Submission.SourceWorktree,
		SourceSHA:        queued.Submission.SourceSHA,
		AllowNewerHead:   queued.Submission.AllowNewerHead,
		Priority:         queued.Submission.Priority,
		RepositoryRoot:   queued.RepoRoot,
		MainWorktree:     queued.Config.Repo.MainWorktree,
		ProtectedBranch:  queued.Config.Repo.ProtectedBranch,
		SubmissionStatus: queued.Submission.Status,
	}

	mainEngine := git.NewEngine(queued.Config.Repo.MainWorktree)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		submission, err := queued.Store.GetIntegrationSubmission(context.Background(), queued.Submission.ID)
		if err != nil {
			result.Error = err.Error()
			return result, err
		}
		result.SubmissionStatus = submission.Status

		switch submission.Status {
		case "blocked", "failed", "cancelled":
			if submission.LastError != "" {
				result.Error = submission.LastError
			}
			return result, fmt.Errorf("submission %d %s: %s", submission.ID, submission.Status, submission.LastError)
		case "succeeded":
			protectedSHA, err := verifySubmissionReachable(ctx, queued.Store, queued.RepoRecord.ID, mainEngine, queued.Config, submission)
			if err != nil {
				result.Error = err.Error()
				return result, err
			}
			result.ProtectedSHA = protectedSHA

			request, err := ensureLandPublishRequest(context.Background(), queued.Store, queued.RepoRecord.ID, queued.RepoRoot, protectedSHA, queued.Submission.Priority)
			if err != nil {
				result.Error = err.Error()
				return result, err
			}
			result.PublishRequestID = request.ID
			result.PublishStatus = request.Status

			queuedPublishes, err := queued.Store.ListPublishRequestsByStatus(context.Background(), queued.RepoRecord.ID, "queued")
			if err != nil {
				result.Error = err.Error()
				return result, err
			}
			runningPublishes, err := queued.Store.ListPublishRequestsByStatus(context.Background(), queued.RepoRecord.ID, "running")
			if err != nil {
				result.Error = err.Error()
				return result, err
			}

			if len(queuedPublishes) == 0 && len(runningPublishes) == 0 {
				status, err := mainEngine.BranchStatus(queued.Config.Repo.ProtectedBranch, queued.Config.Repo.ProtectedBranch)
				if err != nil {
					result.Error = err.Error()
					return result, err
				}

				request, err = queued.Store.GetPublishRequest(context.Background(), result.PublishRequestID)
				if err == nil {
					result.PublishStatus = request.Status
				}

				if status.HasUpstream {
					if status.AheadCount == 0 {
						result.Published = true
						result.PublishStatus = "succeeded"
						return result, nil
					}
				} else if result.PublishStatus == "succeeded" {
					result.Published = true
					return result, nil
				} else if result.PublishStatus == "failed" || result.PublishStatus == "cancelled" || result.PublishStatus == "superseded" {
					result.Error = fmt.Sprintf("publish request %d %s", result.PublishRequestID, result.PublishStatus)
					return result, fmt.Errorf("publish request %d %s", result.PublishRequestID, result.PublishStatus)
				}
			}
		}

		cycleResult, err := runOneCycle(queued.Config.Repo.MainWorktree)
		if err != nil {
			result.LastWorkerResult = cycleResult
			result.Error = err.Error()
			return result, err
		}
		result.LastWorkerResult = cycleResult

		select {
		case <-ctx.Done():
			result.Error = ctx.Err().Error()
			return result, fmt.Errorf("timed out waiting for submission %d to land and publish", queued.Submission.ID)
		case <-ticker.C:
		}
	}
}

func ensureLandPublishRequest(ctx context.Context, store state.Store, repoID int64, repoRoot string, targetSHA string, priority string) (state.PublishRequest, error) {
	requests, err := store.ListPublishRequests(ctx, repoID)
	if err != nil {
		return state.PublishRequest{}, err
	}
	for i := len(requests) - 1; i >= 0; i-- {
		request := requests[i]
		if request.TargetSHA != targetSHA {
			continue
		}
		switch request.Status {
		case "queued", "running", "succeeded":
			return request, nil
		}
	}

	request, err := store.CreatePublishRequest(ctx, state.PublishRequest{
		RepoID:    repoID,
		TargetSHA: targetSHA,
		Priority:  priority,
		Status:    "queued",
	})
	if err != nil {
		return state.PublishRequest{}, err
	}
	if err := appendStateEvent(ctx, store, state.EventRecord{
		RepoID:    repoID,
		ItemType:  "publish_request",
		ItemID:    state.NullInt64(request.ID),
		EventType: "publish.requested",
		Payload: mustJSON(map[string]string{
			"target_sha": targetSHA,
			"reason":     "land_requested",
			"priority":   priority,
			"repo_root":  repoRoot,
		}),
	}); err != nil {
		return state.PublishRequest{}, err
	}

	return request, nil
}
