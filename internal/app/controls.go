package app

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/recallnet/mainline/internal/domain"
	"github.com/recallnet/mainline/internal/state"
)

func runBlocked(args []string, stdout *stepPrinter, stderr io.Writer) error {
	fs := flag.NewFlagSet(currentCLIProgramName()+" blocked", flag.ContinueOnError)
	fs.SetOutput(stderr)
	setFlagUsage(fs, fmt.Sprintf(`Usage:
  %s blocked [flags]

List blocked submissions with their exact recovery commands.

Examples:
  mq blocked --repo /path/to/repo-root
  mq blocked --repo /path/to/repo-root --json

Flags:
`, currentCLIProgramName()))

	var repoPath string
	var asJSON bool
	fs.StringVar(&repoPath, "repo", ".", "repository path")
	fs.BoolVar(&asJSON, "json", false, "output json")
	if err := fs.Parse(args); err != nil {
		return err
	}

	result, err := collectBlocked(repoPath)
	if err != nil {
		return err
	}
	if asJSON {
		encoder := json.NewEncoder(stdout.Raw())
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	printer := stdout
	printer.Section("Blocked submissions")
	printer.Line("Count: %d", result.Count)
	printer.Line("Retry all safe: %s", result.SafeRetryCommand)
	printer.Line("Cancel all blocked: %s", result.CancelAllCommand)
	if len(result.Submissions) == 0 {
		printer.Line("No blocked submissions.")
		return nil
	}
	for _, submission := range result.Submissions {
		printer.Section("Submission %d", submission.ID)
		printer.Line("Branch: %s", submission.BranchName)
		printer.Line("Source worktree: %s", submission.SourceWorktree)
		printer.Line("Blocked reason: %s", submission.BlockedReason)
		if submission.LastError != "" {
			printer.Line("Error: %s", submission.LastError)
		}
		if len(submission.ConflictFiles) > 0 {
			printer.Line("Conflict files: %s", strings.Join(submission.ConflictFiles, ", "))
		}
		for _, action := range submission.NextActions {
			if action.Command != "" {
				printer.Line("%s: %s", action.Label, action.Command)
				continue
			}
			printer.Line("%s", action.Label)
		}
	}
	return nil
}

func runRetry(args []string, stdout *stepPrinter, stderr io.Writer) error {
	return runControlAction("retry", args, stdout, stderr)
}

func runCancel(args []string, stdout *stepPrinter, stderr io.Writer) error {
	return runControlAction("cancel", args, stdout, stderr)
}

func runControlAction(action string, args []string, stdout *stepPrinter, stderr io.Writer) error {
	fs := flag.NewFlagSet(currentCLIProgramName()+" "+action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	setFlagUsage(fs, fmt.Sprintf(`Usage:
  %s %s [flags]

Operate on exactly one queue item.

Examples:
  mq %s --repo /path/to/repo-root --submission 17
  mq %s --repo /path/to/repo-root --publish 4 --json

Flags:
`, currentCLIProgramName(), action, action, action))

	var repoPath string
	var submissionID int64
	var publishID int64
	var allSafe bool
	var blockedOnly bool
	var asJSON bool

	fs.StringVar(&repoPath, "repo", ".", "repository path")
	fs.Int64Var(&submissionID, "submission", 0, "integration submission id")
	fs.Int64Var(&publishID, "publish", 0, "publish request id")
	fs.BoolVar(&allSafe, "all-safe", false, "operate on all safe blocked submissions")
	fs.BoolVar(&blockedOnly, "blocked", false, "operate on all blocked submissions")
	fs.BoolVar(&asJSON, "json", false, "output json")

	if err := fs.Parse(args); err != nil {
		return err
	}
	targetCount := 0
	if submissionID != 0 {
		targetCount++
	}
	if publishID != 0 {
		targetCount++
	}
	if allSafe {
		targetCount++
	}
	if blockedOnly {
		targetCount++
	}
	if targetCount != 1 {
		return fmt.Errorf("exactly one target is required: --submission, --publish, --all-safe, or --blocked")
	}
	if action == "retry" && blockedOnly {
		return fmt.Errorf("--blocked is only supported with cancel; use --all-safe for bulk safe retries")
	}
	if action == "cancel" && allSafe {
		return fmt.Errorf("--all-safe is only supported with retry; use --blocked for bulk blocked cancellation")
	}

	_, _, _, repoRecord, store, err := loadRepoContext(repoPath)
	if err != nil {
		return err
	}
	ctx := context.Background()

	if allSafe {
		return controlBlockedBatch(ctx, action, repoRecord.MainWorktree, store, repoRecord.ID, true, stdout, asJSON)
	}
	if blockedOnly {
		return controlBlockedBatch(ctx, action, repoRecord.MainWorktree, store, repoRecord.ID, false, stdout, asJSON)
	}
	if submissionID != 0 {
		return controlSubmission(ctx, action, repoRecord.MainWorktree, store, repoRecord.ID, submissionID, stdout, asJSON)
	}
	return controlPublish(ctx, action, repoRecord.MainWorktree, store, repoRecord.ID, publishID, stdout, asJSON)
}

type controlResult struct {
	OK             bool   `json:"ok"`
	Action         string `json:"action"`
	ItemType       string `json:"item_type"`
	ID             int64  `json:"id"`
	Branch         string `json:"branch,omitempty"`
	TargetSHA      string `json:"target_sha,omitempty"`
	Status         string `json:"status"`
	DrainAttempted bool   `json:"drain_attempted,omitempty"`
	DrainResult    string `json:"drain_result,omitempty"`
}

type batchControlResult struct {
	OK             bool            `json:"ok"`
	Action         string          `json:"action"`
	Selector       string          `json:"selector"`
	Count          int             `json:"count"`
	Results        []controlResult `json:"results"`
	DrainAttempted bool            `json:"drain_attempted,omitempty"`
	DrainResult    string          `json:"drain_result,omitempty"`
}

type blockedResult struct {
	RepositoryRoot   string             `json:"repository_root"`
	Count            int                `json:"count"`
	SafeRetryCommand string             `json:"safe_retry_command"`
	CancelAllCommand string             `json:"cancel_all_command"`
	Submissions      []statusSubmission `json:"submissions"`
}

func controlSubmission(ctx context.Context, action string, repoPath string, store state.Store, repoID int64, submissionID int64, stdout *stepPrinter, asJSON bool) error {
	submission, err := store.GetIntegrationSubmission(ctx, submissionID)
	if err != nil {
		return err
	}
	if submission.RepoID != repoID {
		return fmt.Errorf("submission %d does not belong to this repository", submissionID)
	}

	switch action {
	case "retry":
		if submission.Status != domain.SubmissionStatusBlocked && submission.Status != domain.SubmissionStatusFailed && submission.Status != domain.SubmissionStatusCancelled {
			return fmt.Errorf("submission %d is %q; only blocked, failed, or cancelled submissions can be retried", submissionID, submission.Status)
		}
		updated, err := store.UpdateIntegrationSubmissionStatus(ctx, submissionID, domain.SubmissionStatusQueued, "")
		if err != nil {
			return err
		}
		if err := appendSubmissionEvent(ctx, store, repoID, submissionID, domain.EventType("submission.retried"), map[string]string{
			"from_status": string(submission.Status),
			"branch":      submissionDisplayRef(submission),
			"source_ref":  submission.SourceRef,
			"ref_kind":    string(submission.RefKind),
		}); err != nil {
			return err
		}
		result := controlResult{
			OK:       true,
			Action:   action,
			ItemType: "submission",
			ID:       updated.ID,
			Branch:   submissionDisplayRef(updated),
			Status:   string(updated.Status),
		}
		if shouldTryDrainAfterMutation() {
			result.DrainAttempted = true
			drainResult, drainErr := drainRepoUntilSettled(repoPath)
			if drainResult != "" {
				result.DrainResult = drainResult
			}
			if drainErr != nil {
				return drainErr
			}
			if refreshed, loadErr := store.GetIntegrationSubmission(ctx, submissionID); loadErr == nil {
				result.Status = string(refreshed.Status)
			}
		}
		if asJSON {
			return json.NewEncoder(stdout.Raw()).Encode(result)
		}
		printer := stdout
		printer.Section("Retried submission %d", updated.ID)
		printer.Line("Branch: %s", submissionDisplayRef(updated))
		printer.Line("Status: %s", result.Status)
		if result.DrainResult != "" {
			printer.Line("Drain result: %s", result.DrainResult)
		}
		return nil
	case "cancel":
		if submission.Status != domain.SubmissionStatusQueued && submission.Status != domain.SubmissionStatusBlocked && submission.Status != domain.SubmissionStatusFailed {
			return fmt.Errorf("submission %d is %q; only queued, blocked, or failed submissions can be cancelled", submissionID, submission.Status)
		}
		updated, err := store.UpdateIntegrationSubmissionStatus(ctx, submissionID, domain.SubmissionStatusCancelled, submission.LastError)
		if err != nil {
			return err
		}
		if err := appendSubmissionEvent(ctx, store, repoID, submissionID, domain.EventType("submission.cancelled"), map[string]string{
			"from_status": string(submission.Status),
			"branch":      submissionDisplayRef(submission),
			"source_ref":  submission.SourceRef,
			"ref_kind":    string(submission.RefKind),
		}); err != nil {
			return err
		}
		result := controlResult{
			OK:       true,
			Action:   action,
			ItemType: "submission",
			ID:       updated.ID,
			Branch:   submissionDisplayRef(updated),
			Status:   string(updated.Status),
		}
		if shouldTryDrainAfterMutation() {
			result.DrainAttempted = true
			drainResult, drainErr := drainRepoUntilSettled(repoPath)
			if drainResult != "" {
				result.DrainResult = drainResult
			}
			if drainErr != nil {
				return drainErr
			}
			if refreshed, loadErr := store.GetIntegrationSubmission(ctx, submissionID); loadErr == nil {
				result.Status = string(refreshed.Status)
			}
		}
		if asJSON {
			return json.NewEncoder(stdout.Raw()).Encode(result)
		}
		printer := stdout
		printer.Section("Cancelled submission %d", updated.ID)
		printer.Line("Branch: %s", submissionDisplayRef(updated))
		printer.Line("Status: %s", result.Status)
		if result.DrainResult != "" {
			printer.Line("Drain result: %s", result.DrainResult)
		}
		return nil
	default:
		return fmt.Errorf("unknown action %q", action)
	}
}

func controlPublish(ctx context.Context, action string, repoPath string, store state.Store, repoID int64, publishID int64, stdout *stepPrinter, asJSON bool) error {
	request, err := store.GetPublishRequest(ctx, publishID)
	if err != nil {
		return err
	}
	if request.RepoID != repoID {
		return fmt.Errorf("publish request %d does not belong to this repository", publishID)
	}

	switch action {
	case "retry":
		if request.Status != domain.PublishStatusFailed && request.Status != domain.PublishStatusCancelled {
			return fmt.Errorf("publish request %d is %q; only failed or cancelled publishes can be retried", publishID, request.Status)
		}
		updated, err := store.ResetPublishRequestForRetry(ctx, publishID)
		if err != nil {
			return err
		}
		if err := appendStateEvent(ctx, store, state.EventRecord{
			RepoID:    repoID,
			ItemType:  domain.ItemTypePublishRequest,
			ItemID:    state.NullInt64(publishID),
			EventType: domain.EventTypePublishRetried,
			Payload: mustJSON(map[string]string{
				"from_status": string(request.Status),
				"target_sha":  request.TargetSHA,
			}),
		}); err != nil {
			return err
		}
		result := controlResult{
			OK:        true,
			Action:    action,
			ItemType:  "publish_request",
			ID:        updated.ID,
			TargetSHA: updated.TargetSHA,
			Status:    string(updated.Status),
		}
		if shouldTryDrainAfterMutation() {
			result.DrainAttempted = true
			drainResult, drainErr := drainRepoUntilSettled(repoPath)
			if drainResult != "" {
				result.DrainResult = drainResult
			}
			if drainErr != nil {
				return drainErr
			}
			if refreshed, loadErr := store.GetPublishRequest(ctx, publishID); loadErr == nil {
				result.Status = string(refreshed.Status)
			}
		}
		if asJSON {
			return json.NewEncoder(stdout.Raw()).Encode(result)
		}
		printer := stdout
		printer.Section("Retried publish request %d", updated.ID)
		printer.Line("Target SHA: %s", updated.TargetSHA)
		printer.Line("Status: %s", result.Status)
		if result.DrainResult != "" {
			printer.Line("Drain result: %s", result.DrainResult)
		}
		return nil
	case "cancel":
		if request.Status != domain.PublishStatusQueued && request.Status != domain.PublishStatusFailed {
			return fmt.Errorf("publish request %d is %q; only queued or failed publishes can be cancelled", publishID, request.Status)
		}
		updated, err := store.UpdatePublishRequestStatus(ctx, publishID, domain.PublishStatusCancelled, request.SupersededBy)
		if err != nil {
			return err
		}
		if err := appendStateEvent(ctx, store, state.EventRecord{
			RepoID:    repoID,
			ItemType:  domain.ItemTypePublishRequest,
			ItemID:    state.NullInt64(publishID),
			EventType: domain.EventTypePublishCancelled,
			Payload: mustJSON(map[string]string{
				"from_status": string(request.Status),
				"target_sha":  request.TargetSHA,
			}),
		}); err != nil {
			return err
		}
		result := controlResult{
			OK:        true,
			Action:    action,
			ItemType:  "publish_request",
			ID:        updated.ID,
			TargetSHA: updated.TargetSHA,
			Status:    string(updated.Status),
		}
		if shouldTryDrainAfterMutation() {
			result.DrainAttempted = true
			drainResult, drainErr := drainRepoUntilSettled(repoPath)
			if drainResult != "" {
				result.DrainResult = drainResult
			}
			if drainErr != nil {
				return drainErr
			}
			if refreshed, loadErr := store.GetPublishRequest(ctx, publishID); loadErr == nil {
				result.Status = string(refreshed.Status)
			}
		}
		if asJSON {
			return json.NewEncoder(stdout.Raw()).Encode(result)
		}
		printer := stdout
		printer.Section("Cancelled publish request %d", updated.ID)
		printer.Line("Target SHA: %s", updated.TargetSHA)
		printer.Line("Status: %s", result.Status)
		if result.DrainResult != "" {
			printer.Line("Drain result: %s", result.DrainResult)
		}
		return nil
	default:
		return fmt.Errorf("unknown action %q", action)
	}
}

func collectBlocked(repoPath string) (blockedResult, error) {
	_, _, cfg, repoRecord, store, err := loadRepoContext(repoPath)
	if err != nil {
		return blockedResult{}, err
	}
	ctx := context.Background()
	submissions, err := store.ListIntegrationSubmissions(ctx, repoRecord.ID)
	if err != nil {
		return blockedResult{}, err
	}
	enriched, err := enrichStatusSubmissions(ctx, store, repoRecord.ID, cfg.Repo.MainWorktree, cfg.Repo.ProtectedBranch, submissions)
	if err != nil {
		return blockedResult{}, err
	}
	var blocked []statusSubmission
	for _, submission := range enriched {
		if submission.Status == domain.SubmissionStatusBlocked {
			blocked = append(blocked, submission)
		}
	}
	return blockedResult{
		RepositoryRoot:   repoRecord.CanonicalPath,
		Count:            len(blocked),
		SafeRetryCommand: fmt.Sprintf("mq retry --repo %s --all-safe", cfg.Repo.MainWorktree),
		CancelAllCommand: fmt.Sprintf("mq cancel --repo %s --blocked", cfg.Repo.MainWorktree),
		Submissions:      blocked,
	}, nil
}

func controlBlockedBatch(ctx context.Context, action string, repoPath string, store state.Store, repoID int64, safeOnly bool, stdout *stepPrinter, asJSON bool) error {
	submissions, err := store.ListIntegrationSubmissions(ctx, repoID)
	if err != nil {
		return err
	}
	var targets []state.IntegrationSubmission
	for _, submission := range submissions {
		if submission.Status != domain.SubmissionStatusBlocked {
			continue
		}
		if safeOnly && !isSafeBlockedRetry(ctx, store, repoID, submission.ID) {
			continue
		}
		targets = append(targets, submission)
	}

	result := batchControlResult{
		OK:       true,
		Action:   action,
		Selector: "blocked",
		Count:    len(targets),
	}
	if safeOnly {
		result.Selector = "all_safe"
	}

	for _, submission := range targets {
		buffer := newStepPrinter(io.Discard)
		switch action {
		case "retry":
			if err := controlSubmission(ctx, action, repoPath, store, repoID, submission.ID, buffer, false); err != nil {
				return err
			}
			refreshed, err := store.GetIntegrationSubmission(ctx, submission.ID)
			if err != nil {
				return err
			}
			result.Results = append(result.Results, controlResult{
				OK:       true,
				Action:   action,
				ItemType: "submission",
				ID:       refreshed.ID,
				Branch:   submissionDisplayRef(refreshed),
				Status:   string(refreshed.Status),
			})
		case "cancel":
			if err := controlSubmission(ctx, action, repoPath, store, repoID, submission.ID, buffer, false); err != nil {
				return err
			}
			refreshed, err := store.GetIntegrationSubmission(ctx, submission.ID)
			if err != nil {
				return err
			}
			result.Results = append(result.Results, controlResult{
				OK:       true,
				Action:   action,
				ItemType: "submission",
				ID:       refreshed.ID,
				Branch:   submissionDisplayRef(refreshed),
				Status:   string(refreshed.Status),
			})
		default:
			return fmt.Errorf("unknown action %q", action)
		}
	}

	if shouldTryDrainAfterMutation() && len(targets) > 0 {
		result.DrainAttempted = true
		drainResult, drainErr := drainRepoUntilSettled(repoPath)
		if drainResult != "" {
			result.DrainResult = drainResult
		}
		if drainErr != nil {
			return drainErr
		}
	}

	if asJSON {
		return json.NewEncoder(stdout.Raw()).Encode(result)
	}
	printer := stdout
	switch action {
	case "retry":
		printer.Section("Retried blocked submissions")
	case "cancel":
		printer.Section("Cancelled blocked submissions")
	}
	printer.Line("Selector: %s", result.Selector)
	printer.Line("Count: %d", result.Count)
	for _, item := range result.Results {
		printer.Line("#%d %s (%s)", item.ID, item.Branch, item.Status)
	}
	if result.DrainResult != "" {
		printer.Line("Drain result: %s", result.DrainResult)
	}
	return nil
}

func isSafeBlockedRetry(ctx context.Context, store state.Store, repoID int64, submissionID int64) bool {
	details, err := latestBlockedSubmissionDetails(ctx, store, repoID, submissionID)
	if err != nil {
		return false
	}
	return details.BlockedReason == domain.BlockedReasonCheckTimeout
}
