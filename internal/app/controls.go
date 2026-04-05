package app

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/recallnet/mainline/internal/domain"
	"github.com/recallnet/mainline/internal/state"
)

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
	var asJSON bool

	fs.StringVar(&repoPath, "repo", ".", "repository path")
	fs.Int64Var(&submissionID, "submission", 0, "integration submission id")
	fs.Int64Var(&publishID, "publish", 0, "publish request id")
	fs.BoolVar(&asJSON, "json", false, "output json")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if (submissionID == 0 && publishID == 0) || (submissionID != 0 && publishID != 0) {
		return fmt.Errorf("exactly one of --submission or --publish is required")
	}

	_, _, _, repoRecord, store, err := loadRepoContext(repoPath)
	if err != nil {
		return err
	}
	ctx := context.Background()

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
