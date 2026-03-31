package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/policy"
	"github.com/recallnet/mainline/internal/state"
)

func runRunOnce(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet("mainline run-once", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var repoPath string
	fs.StringVar(&repoPath, "repo", ".", "repository path")

	if err := fs.Parse(args); err != nil {
		return err
	}

	layout, repoRoot, cfg, repoRecord, store, err := loadRepoContext(repoPath)
	if err != nil {
		return err
	}

	mainLayout, err := git.DiscoverRepositoryLayout(cfg.Repo.MainWorktree)
	if err != nil {
		return err
	}
	if filepath.Clean(mainLayout.GitDir) != filepath.Clean(layout.GitDir) {
		return fmt.Errorf("main worktree %s does not belong to repository %s", cfg.Repo.MainWorktree, repoRoot)
	}

	report, err := git.NewEngine(mainLayout.WorktreeRoot).InspectHealth(cfg.Repo.ProtectedBranch, cfg.Repo.MainWorktree)
	if err != nil {
		return err
	}
	if !report.MainWorktreeExists {
		return fmt.Errorf("main worktree %s is missing", cfg.Repo.MainWorktree)
	}
	if !report.ProtectedBranchExists {
		return fmt.Errorf("protected branch %q does not exist", cfg.Repo.ProtectedBranch)
	}
	if !report.ProtectedBranchClean {
		return fmt.Errorf("protected branch worktree %s is dirty", cfg.Repo.MainWorktree)
	}
	if report.HasDivergedUpstream {
		return fmt.Errorf("protected branch %q has diverged from upstream %s", cfg.Repo.ProtectedBranch, report.UpstreamRef)
	}

	lockManager := state.NewLockManager(repoRoot, layout.GitDir)
	ctx := context.Background()

	lease, err := lockManager.Acquire(state.IntegrationLock, "run-once")
	if err != nil {
		return err
	}

	submission, err := store.NextQueuedIntegrationSubmission(ctx, repoRecord.ID)
	if err != nil {
		lease.Release()
		if !errors.Is(err, state.ErrNotFound) {
			return err
		}
	} else {
		defer lease.Release()

		if _, err := store.UpdateIntegrationSubmissionStatus(ctx, submission.ID, "running", ""); err != nil {
			return err
		}
		if err := appendSubmissionEvent(ctx, store, repoRecord.ID, submission.ID, "integration.started", map[string]string{
			"branch":           submission.BranchName,
			"source_worktree":  submission.SourceWorktree,
			"submitted_source": submission.SourceSHA,
		}); err != nil {
			return err
		}

		result, err := processIntegrationSubmission(ctx, store, repoRecord, cfg, layout.GitDir, submission)
		if err != nil {
			return err
		}

		fmt.Fprintln(stdout, result)
		return nil
	}

	publishLease, err := lockManager.Acquire(state.PublishLock, "run-once")
	if err != nil {
		if errors.Is(err, state.ErrLockHeld) {
			return err
		}
		return err
	}
	defer publishLease.Release()

	result, err := processPublishRequest(ctx, store, repoRecord, cfg)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, result)
	return nil
}

func processIntegrationSubmission(ctx context.Context, store state.Store, repoRecord state.RepositoryRecord, cfg policy.File, sharedGitDir string, submission state.IntegrationSubmission) (string, error) {
	mainLayout, err := git.DiscoverRepositoryLayout(cfg.Repo.MainWorktree)
	if err != nil {
		return "", err
	}
	mainEngine := git.NewEngine(mainLayout.WorktreeRoot)

	if err := syncProtectedBranch(mainEngine, cfg); err != nil {
		return failIntegrationSubmission(ctx, store, repoRecord.ID, submission.ID, err)
	}

	sourceLayout, err := git.DiscoverRepositoryLayout(submission.SourceWorktree)
	if err != nil {
		return failIntegrationSubmission(ctx, store, repoRecord.ID, submission.ID, fmt.Errorf("source worktree is unavailable: %w", err))
	}
	if filepath.Clean(sourceLayout.GitDir) != filepath.Clean(sharedGitDir) {
		return failIntegrationSubmission(ctx, store, repoRecord.ID, submission.ID, fmt.Errorf("source worktree %s no longer belongs to repository %s", submission.SourceWorktree, repoRecord.CanonicalPath))
	}

	sourceEngine := git.NewEngine(submission.SourceWorktree)
	worktree, err := sourceEngine.ResolveWorktree(submission.SourceWorktree)
	if err != nil {
		return failIntegrationSubmission(ctx, store, repoRecord.ID, submission.ID, err)
	}
	if worktree.IsDetached {
		return failIntegrationSubmission(ctx, store, repoRecord.ID, submission.ID, fmt.Errorf("source worktree %s is detached", submission.SourceWorktree))
	}
	if worktree.Branch != submission.BranchName {
		return failIntegrationSubmission(ctx, store, repoRecord.ID, submission.ID, fmt.Errorf("source worktree %s is on %q, expected %q", submission.SourceWorktree, worktree.Branch, submission.BranchName))
	}

	clean, err := sourceEngine.WorktreeIsClean(submission.SourceWorktree)
	if err != nil {
		return "", err
	}
	if !clean {
		return failIntegrationSubmission(ctx, store, repoRecord.ID, submission.ID, fmt.Errorf("source worktree %s is dirty; clean it and resubmit", submission.SourceWorktree))
	}

	headSHA, err := sourceEngine.BranchHeadSHA(submission.BranchName)
	if err != nil {
		return failIntegrationSubmission(ctx, store, repoRecord.ID, submission.ID, fmt.Errorf("resolve branch head for %q: %w", submission.BranchName, err))
	}
	if headSHA != submission.SourceSHA {
		return failIntegrationSubmission(ctx, store, repoRecord.ID, submission.ID, fmt.Errorf("branch %q moved from submitted SHA %s to %s; resubmit the branch", submission.BranchName, submission.SourceSHA, headSHA))
	}

	if err := sourceEngine.RebaseCurrentBranch(submission.SourceWorktree, cfg.Repo.ProtectedBranch); err != nil {
		if errors.Is(err, git.ErrRebaseConflict) {
			return blockIntegrationSubmission(ctx, store, repoRecord.ID, submission.ID, fmt.Errorf("rebase conflict in %s: resolve in the source worktree and resubmit", submission.SourceWorktree))
		}
		return failIntegrationSubmission(ctx, store, repoRecord.ID, submission.ID, err)
	}

	if err := mainEngine.FastForwardCurrentBranch(cfg.Repo.MainWorktree, submission.BranchName); err != nil {
		return failIntegrationSubmission(ctx, store, repoRecord.ID, submission.ID, err)
	}

	protectedHead, err := mainEngine.BranchHeadSHA(cfg.Repo.ProtectedBranch)
	if err != nil {
		return "", err
	}

	if _, err := store.UpdateIntegrationSubmissionStatus(ctx, submission.ID, "succeeded", ""); err != nil {
		return "", err
	}
	if err := appendSubmissionEvent(ctx, store, repoRecord.ID, submission.ID, "integration.succeeded", map[string]string{
		"branch":        submission.BranchName,
		"protected_sha": protectedHead,
	}); err != nil {
		return "", err
	}

	if cfg.Publish.Mode == "auto" {
		request, err := store.CreatePublishRequest(ctx, state.PublishRequest{
			RepoID:    repoRecord.ID,
			TargetSHA: protectedHead,
			Status:    "queued",
		})
		if err != nil {
			return "", err
		}
		if err := appendStateEvent(ctx, store, state.EventRecord{
			RepoID:    repoRecord.ID,
			ItemType:  "publish_request",
			ItemID:    state.NullInt64(request.ID),
			EventType: "publish.requested",
			Payload: mustJSON(map[string]string{
				"target_sha": protectedHead,
				"reason":     "integration_succeeded",
			}),
		}); err != nil {
			return "", err
		}
	}

	return fmt.Sprintf("Integrated submission %d from %s onto %s", submission.ID, submission.BranchName, cfg.Repo.ProtectedBranch), nil
}

func syncProtectedBranch(engine git.Engine, cfg policy.File) error {
	if cfg.Integration.SyncPolicy != "sync-before-integrate" {
		return nil
	}

	status, err := engine.BranchStatus(cfg.Repo.ProtectedBranch, cfg.Repo.ProtectedBranch)
	if err != nil {
		return err
	}
	if !status.HasUpstream {
		return nil
	}
	if status.AheadCount > 0 && status.BehindCount > 0 {
		return fmt.Errorf("protected branch %q has diverged from upstream %s", cfg.Repo.ProtectedBranch, status.Upstream)
	}

	if err := engine.FetchRemote(cfg.Repo.MainWorktree, cfg.Repo.RemoteName); err != nil {
		return err
	}

	status, err = engine.BranchStatus(cfg.Repo.ProtectedBranch, cfg.Repo.ProtectedBranch)
	if err != nil {
		return err
	}
	if status.AheadCount > 0 && status.BehindCount > 0 {
		return fmt.Errorf("protected branch %q has diverged from upstream %s", cfg.Repo.ProtectedBranch, status.Upstream)
	}
	if status.BehindCount == 0 {
		return nil
	}

	return engine.FastForwardCurrentBranch(cfg.Repo.MainWorktree, status.Upstream)
}

func processPublishRequest(ctx context.Context, store state.Store, repoRecord state.RepositoryRecord, cfg policy.File) (string, error) {
	mainEngine := git.NewEngine(cfg.Repo.MainWorktree)

	request, err := store.LatestQueuedPublishRequest(ctx, repoRecord.ID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return "No queued publish requests.", nil
		}
		return "", err
	}

	if err := store.SupersedeOlderQueuedPublishRequests(ctx, repoRecord.ID, request.ID); err != nil {
		return "", err
	}
	if _, err := store.UpdatePublishRequestStatus(ctx, request.ID, "running", sql.NullInt64{}); err != nil {
		return "", err
	}
	if err := appendStateEvent(ctx, store, state.EventRecord{
		RepoID:    repoRecord.ID,
		ItemType:  "publish_request",
		ItemID:    state.NullInt64(request.ID),
		EventType: "publish.started",
		Payload: mustJSON(map[string]string{
			"target_sha": request.TargetSHA,
		}),
	}); err != nil {
		return "", err
	}

	currentProtectedSHA, err := mainEngine.BranchHeadSHA(cfg.Repo.ProtectedBranch)
	if err != nil {
		return "", err
	}
	if currentProtectedSHA != request.TargetSHA {
		replacement, created, err := ensureLatestPublishRequestRecord(ctx, store, repoRecord.ID, currentProtectedSHA)
		if err != nil {
			return "", err
		}
		updated, err := store.UpdatePublishRequestStatus(ctx, request.ID, "superseded", state.NullInt64(replacement.ID))
		if err != nil {
			return "", err
		}
		if err := appendStateEvent(ctx, store, state.EventRecord{
			RepoID:    repoRecord.ID,
			ItemType:  "publish_request",
			ItemID:    state.NullInt64(updated.ID),
			EventType: "publish.superseded",
			Payload: mustJSON(map[string]string{
				"target_sha":        request.TargetSHA,
				"new_protected_sha": currentProtectedSHA,
				"reason":            "protected_branch_advanced_before_publish",
			}),
		}); err != nil {
			return "", err
		}
		if created {
			return fmt.Sprintf("Queued follow-up publish request %d for %s", replacement.ID, currentProtectedSHA), nil
		}
		return fmt.Sprintf("Superseded older publish requests; latest target is %s", currentProtectedSHA), nil
	}

	if err := mainEngine.PushBranch(cfg.Repo.MainWorktree, cfg.Repo.RemoteName, cfg.Repo.ProtectedBranch); err != nil {
		if _, updateErr := store.UpdatePublishRequestStatus(ctx, request.ID, "failed", sql.NullInt64{}); updateErr != nil {
			return "", updateErr
		}
		if eventErr := appendStateEvent(ctx, store, state.EventRecord{
			RepoID:    repoRecord.ID,
			ItemType:  "publish_request",
			ItemID:    state.NullInt64(request.ID),
			EventType: "publish.failed",
			Payload: mustJSON(map[string]string{
				"target_sha": request.TargetSHA,
				"error":      err.Error(),
			}),
		}); eventErr != nil {
			return "", eventErr
		}
		return fmt.Sprintf("Failed publish request %d: %s", request.ID, err.Error()), nil
	}

	latestProtectedSHA, err := mainEngine.BranchHeadSHA(cfg.Repo.ProtectedBranch)
	if err != nil {
		return "", err
	}
	if _, err := store.UpdatePublishRequestStatus(ctx, request.ID, "succeeded", sql.NullInt64{}); err != nil {
		return "", err
	}
	if err := appendStateEvent(ctx, store, state.EventRecord{
		RepoID:    repoRecord.ID,
		ItemType:  "publish_request",
		ItemID:    state.NullInt64(request.ID),
		EventType: "publish.completed",
		Payload: mustJSON(map[string]string{
			"target_sha": latestProtectedSHA,
		}),
	}); err != nil {
		return "", err
	}

	if latestProtectedSHA != request.TargetSHA {
		replacement, created, err := ensureLatestPublishRequestRecord(ctx, store, repoRecord.ID, latestProtectedSHA)
		if err != nil {
			return "", err
		}
		if created {
			return fmt.Sprintf("Queued follow-up publish request %d for %s", replacement.ID, latestProtectedSHA), nil
		}
		return fmt.Sprintf("Superseded older publish requests; latest target is %s", latestProtectedSHA), nil
	}

	return fmt.Sprintf("Published request %d for %s", request.ID, latestProtectedSHA), nil
}

func ensureLatestPublishRequestRecord(ctx context.Context, store state.Store, repoID int64, targetSHA string) (state.PublishRequest, bool, error) {
	requests, err := store.ListPublishRequests(ctx, repoID)
	if err != nil {
		return state.PublishRequest{}, false, err
	}
	for _, request := range requests {
		if request.TargetSHA == targetSHA && (request.Status == "queued" || request.Status == "running" || request.Status == "succeeded") {
			return request, false, nil
		}
	}

	request, err := store.CreatePublishRequest(ctx, state.PublishRequest{
		RepoID:    repoID,
		TargetSHA: targetSHA,
		Status:    "queued",
	})
	if err != nil {
		return state.PublishRequest{}, false, err
	}
	if err := appendStateEvent(ctx, store, state.EventRecord{
		RepoID:    repoID,
		ItemType:  "publish_request",
		ItemID:    state.NullInt64(request.ID),
		EventType: "publish.requested",
		Payload: mustJSON(map[string]string{
			"target_sha": targetSHA,
			"reason":     "protected_branch_advanced_after_publish",
		}),
	}); err != nil {
		return state.PublishRequest{}, false, err
	}

	return request, true, nil
}

func failIntegrationSubmission(ctx context.Context, store state.Store, repoID int64, submissionID int64, cause error) (string, error) {
	if _, err := store.UpdateIntegrationSubmissionStatus(ctx, submissionID, "failed", cause.Error()); err != nil {
		return "", err
	}
	if err := appendSubmissionEvent(ctx, store, repoID, submissionID, "integration.failed", map[string]string{
		"error": cause.Error(),
	}); err != nil {
		return "", err
	}
	return fmt.Sprintf("Failed submission %d: %s", submissionID, cause.Error()), nil
}

func blockIntegrationSubmission(ctx context.Context, store state.Store, repoID int64, submissionID int64, cause error) (string, error) {
	if _, err := store.UpdateIntegrationSubmissionStatus(ctx, submissionID, "blocked", cause.Error()); err != nil {
		return "", err
	}
	if err := appendSubmissionEvent(ctx, store, repoID, submissionID, "integration.blocked", map[string]string{
		"error": cause.Error(),
	}); err != nil {
		return "", err
	}
	return fmt.Sprintf("Blocked submission %d: %s", submissionID, cause.Error()), nil
}

func appendSubmissionEvent(ctx context.Context, store state.Store, repoID int64, submissionID int64, eventType string, payload map[string]string) error {
	return appendStateEvent(ctx, store, state.EventRecord{
		RepoID:    repoID,
		ItemType:  "integration_submission",
		ItemID:    state.NullInt64(submissionID),
		EventType: eventType,
		Payload:   mustJSON(payload),
	})
}

func appendStateEvent(ctx context.Context, store state.Store, event state.EventRecord) error {
	_, err := store.AppendEvent(ctx, event)
	return err
}

func mustJSON(payload map[string]string) json.RawMessage {
	data, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return data
}
