package app

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"

	"github.com/recallnet/mainline/internal/state"
)

func runRetry(args []string, stdout io.Writer, stderr io.Writer) error {
	return runControlAction("retry", args, stdout, stderr)
}

func runCancel(args []string, stdout io.Writer, stderr io.Writer) error {
	return runControlAction("cancel", args, stdout, stderr)
}

func runControlAction(action string, args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet("mainline "+action, flag.ContinueOnError)
	fs.SetOutput(stderr)

	var repoPath string
	var submissionID int64
	var publishID int64

	fs.StringVar(&repoPath, "repo", ".", "repository path")
	fs.Int64Var(&submissionID, "submission", 0, "integration submission id")
	fs.Int64Var(&publishID, "publish", 0, "publish request id")

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
		return controlSubmission(ctx, action, store, repoRecord.ID, submissionID, stdout)
	}
	return controlPublish(ctx, action, store, repoRecord.ID, publishID, stdout)
}

func controlSubmission(ctx context.Context, action string, store state.Store, repoID int64, submissionID int64, stdout io.Writer) error {
	submission, err := store.GetIntegrationSubmission(ctx, submissionID)
	if err != nil {
		return err
	}
	if submission.RepoID != repoID {
		return fmt.Errorf("submission %d does not belong to this repository", submissionID)
	}

	switch action {
	case "retry":
		if submission.Status != "blocked" && submission.Status != "failed" && submission.Status != "cancelled" {
			return fmt.Errorf("submission %d is %q; only blocked, failed, or cancelled submissions can be retried", submissionID, submission.Status)
		}
		updated, err := store.UpdateIntegrationSubmissionStatus(ctx, submissionID, "queued", "")
		if err != nil {
			return err
		}
		if err := appendSubmissionEvent(ctx, store, repoID, submissionID, "submission.retried", map[string]string{
			"from_status": submission.Status,
			"branch":      submission.BranchName,
		}); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Retried submission %d\n", updated.ID)
		fmt.Fprintf(stdout, "Branch: %s\n", updated.BranchName)
		fmt.Fprintf(stdout, "Status: %s\n", updated.Status)
		return nil
	case "cancel":
		if submission.Status != "queued" && submission.Status != "blocked" && submission.Status != "failed" {
			return fmt.Errorf("submission %d is %q; only queued, blocked, or failed submissions can be cancelled", submissionID, submission.Status)
		}
		updated, err := store.UpdateIntegrationSubmissionStatus(ctx, submissionID, "cancelled", submission.LastError)
		if err != nil {
			return err
		}
		if err := appendSubmissionEvent(ctx, store, repoID, submissionID, "submission.cancelled", map[string]string{
			"from_status": submission.Status,
			"branch":      submission.BranchName,
		}); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Cancelled submission %d\n", updated.ID)
		fmt.Fprintf(stdout, "Branch: %s\n", updated.BranchName)
		fmt.Fprintf(stdout, "Status: %s\n", updated.Status)
		return nil
	default:
		return fmt.Errorf("unknown action %q", action)
	}
}

func controlPublish(ctx context.Context, action string, store state.Store, repoID int64, publishID int64, stdout io.Writer) error {
	request, err := store.GetPublishRequest(ctx, publishID)
	if err != nil {
		return err
	}
	if request.RepoID != repoID {
		return fmt.Errorf("publish request %d does not belong to this repository", publishID)
	}

	switch action {
	case "retry":
		if request.Status != "failed" && request.Status != "cancelled" {
			return fmt.Errorf("publish request %d is %q; only failed or cancelled publishes can be retried", publishID, request.Status)
		}
		updated, err := store.UpdatePublishRequestStatus(ctx, publishID, "queued", sql.NullInt64{})
		if err != nil {
			return err
		}
		if err := appendStateEvent(ctx, store, state.EventRecord{
			RepoID:    repoID,
			ItemType:  "publish_request",
			ItemID:    state.NullInt64(publishID),
			EventType: "publish.retried",
			Payload: mustJSON(map[string]string{
				"from_status": request.Status,
				"target_sha":  request.TargetSHA,
			}),
		}); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Retried publish request %d\n", updated.ID)
		fmt.Fprintf(stdout, "Target SHA: %s\n", updated.TargetSHA)
		fmt.Fprintf(stdout, "Status: %s\n", updated.Status)
		return nil
	case "cancel":
		if request.Status != "queued" && request.Status != "failed" {
			return fmt.Errorf("publish request %d is %q; only queued or failed publishes can be cancelled", publishID, request.Status)
		}
		updated, err := store.UpdatePublishRequestStatus(ctx, publishID, "cancelled", request.SupersededBy)
		if err != nil {
			return err
		}
		if err := appendStateEvent(ctx, store, state.EventRecord{
			RepoID:    repoID,
			ItemType:  "publish_request",
			ItemID:    state.NullInt64(publishID),
			EventType: "publish.cancelled",
			Payload: mustJSON(map[string]string{
				"from_status": request.Status,
				"target_sha":  request.TargetSHA,
			}),
		}); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Cancelled publish request %d\n", updated.ID)
		fmt.Fprintf(stdout, "Target SHA: %s\n", updated.TargetSHA)
		fmt.Fprintf(stdout, "Status: %s\n", updated.Status)
		return nil
	default:
		return fmt.Errorf("unknown action %q", action)
	}
}
