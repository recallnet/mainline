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
	"strings"
	"time"

	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/policy"
	"github.com/recallnet/mainline/internal/state"
)

func runRunOnce(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet("mainline run-once", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var repoPath string
	var asJSON bool
	fs.StringVar(&repoPath, "repo", ".", "repository path")
	fs.BoolVar(&asJSON, "json", false, "output json")

	if err := fs.Parse(args); err != nil {
		return err
	}

	result, err := runOneCycle(repoPath)
	if err != nil {
		return err
	}
	if asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(map[string]any{
			"ok":     true,
			"repo":   repoPath,
			"result": result,
		})
	}
	fmt.Fprintln(stdout, result)
	return nil
}

func runOneCycle(repoPath string) (string, error) {
	if err := applyAppTestFault("daemon.cycle"); err != nil {
		return "", err
	}

	layout, repoRoot, cfg, repoRecord, store, err := loadRepoContext(repoPath)
	if err != nil {
		return "", err
	}

	mainLayout, err := git.DiscoverRepositoryLayout(cfg.Repo.MainWorktree)
	if err != nil {
		return "", err
	}
	if filepath.Clean(mainLayout.GitDir) != filepath.Clean(layout.GitDir) {
		return "", fmt.Errorf("main worktree %s does not belong to repository %s", cfg.Repo.MainWorktree, repoRoot)
	}

	report, err := git.NewEngine(mainLayout.WorktreeRoot).InspectHealth(cfg.Repo.ProtectedBranch, cfg.Repo.MainWorktree)
	if err != nil {
		return "", err
	}
	if !report.MainWorktreeExists {
		return "", fmt.Errorf("main worktree %s is missing", cfg.Repo.MainWorktree)
	}
	if !report.ProtectedBranchExists {
		return "", fmt.Errorf("protected branch %q does not exist", cfg.Repo.ProtectedBranch)
	}
	if !report.ProtectedBranchClean {
		return "", fmt.Errorf("protected branch worktree %s is dirty", cfg.Repo.MainWorktree)
	}
	if report.HasDivergedUpstream {
		return "", fmt.Errorf("protected branch %q has diverged from upstream %s", cfg.Repo.ProtectedBranch, report.UpstreamRef)
	}

	lockManager := state.NewLockManager(repoRoot, layout.GitDir)
	ctx := context.Background()

	lease, err := lockManager.Acquire(state.IntegrationLock, "run-once")
	if err != nil {
		if errors.Is(err, state.ErrLockHeld) {
			return "Integration worker busy.", nil
		}
		return "", err
	}

	if err := recoverInterruptedIntegrationSubmissions(ctx, store, repoRecord.ID); err != nil {
		lease.Release()
		return "", err
	}

	submission, err := store.NextQueuedIntegrationSubmission(ctx, repoRecord.ID)
	if err != nil {
		lease.Release()
		if !errors.Is(err, state.ErrNotFound) {
			return "", err
		}
	} else {
		defer lease.Release()
		if _, err := store.UpdateIntegrationSubmissionStatus(ctx, submission.ID, "running", ""); err != nil {
			return "", err
		}
		if err := appendSubmissionEvent(ctx, store, repoRecord.ID, submission.ID, "integration.started", map[string]string{
			"branch":           submission.BranchName,
			"source_worktree":  submission.SourceWorktree,
			"submitted_source": submission.SourceSHA,
		}); err != nil {
			return "", err
		}

		result, err := processIntegrationSubmission(ctx, store, repoRecord, cfg, layout.GitDir, submission)
		if err != nil {
			return "", err
		}
		return result, nil
	}

	publishLease, err := lockManager.Acquire(state.PublishLock, "run-once")
	if err != nil {
		if errors.Is(err, state.ErrLockHeld) {
			if cfg.Publish.InterruptInflight {
				return maybeRequestPublishPreemption(ctx, store, repoRecord, cfg, lockManager)
			}
			return "Publish worker busy.", nil
		}
		return "", err
	}
	defer publishLease.Release()

	if err := recoverInterruptedPublishRequests(ctx, store, repoRecord.ID); err != nil {
		return "", err
	}

	result, err := processPublishRequest(ctx, store, repoRecord, cfg, publishLease)
	if err != nil {
		return "", err
	}
	return result, nil
}

func processIntegrationSubmission(ctx context.Context, store state.Store, repoRecord state.RepositoryRecord, cfg policy.File, sharedGitDir string, submission state.IntegrationSubmission) (string, error) {
	mainLayout, err := git.DiscoverRepositoryLayout(cfg.Repo.MainWorktree)
	if err != nil {
		return "", err
	}
	mainEngine := git.NewEngine(mainLayout.WorktreeRoot)

	syncResult, err := syncProtectedBranch(mainEngine, cfg)
	if err != nil {
		return failIntegrationSubmission(ctx, store, repoRecord.ID, submission.ID, err)
	}
	if syncResult.Synced {
		if err := appendStateEvent(ctx, store, state.EventRecord{
			RepoID:    repoRecord.ID,
			ItemType:  "repository",
			EventType: "protected.synced_from_upstream",
			Payload: mustJSON(map[string]string{
				"protected_branch": cfg.Repo.ProtectedBranch,
				"upstream":         syncResult.Upstream,
				"before_sha":       syncResult.BeforeSHA,
				"after_sha":        syncResult.AfterSHA,
			}),
		}); err != nil {
			return "", err
		}
	}

	sourceLayout, err := git.DiscoverRepositoryLayout(submission.SourceWorktree)
	if err != nil {
		return failIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission.ID, syncResult, fmt.Errorf("source worktree is unavailable: %w", err))
	}
	if filepath.Clean(sourceLayout.GitDir) != filepath.Clean(sharedGitDir) {
		return failIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission.ID, syncResult, fmt.Errorf("source worktree %s no longer belongs to repository %s", submission.SourceWorktree, repoRecord.CanonicalPath))
	}

	sourceEngine := git.NewEngine(submission.SourceWorktree)
	worktree, err := sourceEngine.ResolveWorktree(submission.SourceWorktree)
	if err != nil {
		return failIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission.ID, syncResult, err)
	}
	if worktree.IsDetached {
		return failIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission.ID, syncResult, fmt.Errorf("source worktree %s is detached", submission.SourceWorktree))
	}
	if worktree.Branch != submission.BranchName {
		return failIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission.ID, syncResult, fmt.Errorf("source worktree %s is on %q, expected %q", submission.SourceWorktree, worktree.Branch, submission.BranchName))
	}

	clean, err := sourceEngine.WorktreeIsClean(submission.SourceWorktree)
	if err != nil {
		return "", err
	}
	if !clean {
		return failIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission.ID, syncResult, fmt.Errorf("source worktree %s is dirty; clean it and resubmit", submission.SourceWorktree))
	}

	headSHA, err := sourceEngine.BranchHeadSHA(submission.BranchName)
	if err != nil {
		return failIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission.ID, syncResult, fmt.Errorf("resolve branch head for %q: %w", submission.BranchName, err))
	}
	if headSHA != submission.SourceSHA {
		return failIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission.ID, syncResult, fmt.Errorf("branch %q moved from submitted SHA %s to %s; resubmit the branch", submission.BranchName, submission.SourceSHA, headSHA))
	}

	if err := runConfiguredChecks(cfg.Checks.PreIntegrate, submission.SourceWorktree, cfg.Checks.CommandTimeout); err != nil {
		return blockIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission.ID, syncResult, fmt.Errorf("pre-integrate checks failed: %w", err))
	}

	if err := applyAppTestFault("integration.rebase"); err != nil {
		return failIntegrationSubmission(ctx, store, repoRecord.ID, submission.ID, err)
	}
	if err := sourceEngine.RebaseCurrentBranch(submission.SourceWorktree, cfg.Repo.ProtectedBranch); err != nil {
		if errors.Is(err, git.ErrRebaseConflict) {
			protectedTipSHA, shaErr := mainEngine.BranchHeadSHA(cfg.Repo.ProtectedBranch)
			if shaErr != nil {
				return failIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission.ID, syncResult, shaErr)
			}
			conflictFiles, conflictErr := sourceEngine.ConflictedFiles(submission.SourceWorktree)
			if conflictErr != nil {
				return failIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission.ID, syncResult, conflictErr)
			}
			return blockIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission.ID, syncResult,
				fmt.Errorf("rebase conflict in %s: resolve in the source worktree and resubmit", submission.SourceWorktree),
				map[string]any{
					"blocked_reason":     "rebase_conflict",
					"conflict_files":     conflictFiles,
					"protected_tip_sha":  protectedTipSHA,
					"retry_hint":         "manual-rebase-from-tip",
					"retry_recommended":  false,
					"source_worktree":    submission.SourceWorktree,
					"protected_branch":   cfg.Repo.ProtectedBranch,
					"protected_upstream": syncResult.Upstream,
				},
			)
		}
		return failIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission.ID, syncResult, err)
	}

	if err := mainEngine.FastForwardCurrentBranch(cfg.Repo.MainWorktree, submission.BranchName); err != nil {
		return failIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission.ID, syncResult, err)
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

	if syncResult.Synced {
		return fmt.Sprintf("Synced %s from %s and integrated submission %d from %s onto %s", cfg.Repo.ProtectedBranch, syncResult.Upstream, submission.ID, submission.BranchName, cfg.Repo.ProtectedBranch), nil
	}

	return fmt.Sprintf("Integrated submission %d from %s onto %s", submission.ID, submission.BranchName, cfg.Repo.ProtectedBranch), nil
}

type protectedSyncResult struct {
	Synced    bool
	Upstream  string
	BeforeSHA string
	AfterSHA  string
}

func syncProtectedBranch(engine git.Engine, cfg policy.File) (protectedSyncResult, error) {
	if cfg.Integration.SyncPolicy != "sync-before-integrate" {
		return protectedSyncResult{}, nil
	}

	status, err := engine.BranchStatus(cfg.Repo.ProtectedBranch, cfg.Repo.ProtectedBranch)
	if err != nil {
		return protectedSyncResult{}, err
	}
	if !status.HasUpstream {
		return protectedSyncResult{}, nil
	}
	if status.AheadCount > 0 && status.BehindCount > 0 {
		return protectedSyncResult{}, fmt.Errorf("protected branch %q has diverged from upstream %s", cfg.Repo.ProtectedBranch, status.Upstream)
	}
	beforeSHA, err := engine.BranchHeadSHA(cfg.Repo.ProtectedBranch)
	if err != nil {
		return protectedSyncResult{}, err
	}

	if err := applyAppTestFault("integration.fetch"); err != nil {
		return protectedSyncResult{}, err
	}
	if err := engine.FetchRemote(cfg.Repo.MainWorktree, cfg.Repo.RemoteName); err != nil {
		return protectedSyncResult{}, err
	}

	status, err = engine.BranchStatus(cfg.Repo.ProtectedBranch, cfg.Repo.ProtectedBranch)
	if err != nil {
		return protectedSyncResult{}, err
	}
	if status.AheadCount > 0 && status.BehindCount > 0 {
		return protectedSyncResult{}, fmt.Errorf("protected branch %q has diverged from upstream %s", cfg.Repo.ProtectedBranch, status.Upstream)
	}
	if status.BehindCount == 0 {
		return protectedSyncResult{}, nil
	}

	if err := engine.FastForwardCurrentBranch(cfg.Repo.MainWorktree, status.Upstream); err != nil {
		return protectedSyncResult{}, err
	}
	afterSHA, err := engine.BranchHeadSHA(cfg.Repo.ProtectedBranch)
	if err != nil {
		return protectedSyncResult{}, err
	}
	return protectedSyncResult{
		Synced:    true,
		Upstream:  status.Upstream,
		BeforeSHA: beforeSHA,
		AfterSHA:  afterSHA,
	}, nil
}

func processPublishRequest(ctx context.Context, store state.Store, repoRecord state.RepositoryRecord, cfg policy.File, publishLease *state.Lease) (string, error) {
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

	if err := runConfiguredChecks(cfg.Checks.PrePublish, cfg.Repo.MainWorktree, cfg.Checks.CommandTimeout); err != nil {
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
				"error":      fmt.Sprintf("pre-publish checks failed: %s", err.Error()),
			}),
		}); eventErr != nil {
			return "", eventErr
		}
		return fmt.Sprintf("Failed publish request %d: pre-publish checks failed: %s", request.ID, err.Error()), nil
	}

	if err := applyAppTestFault("publish.push"); err != nil {
		return markPublishFailed(ctx, store, repoRecord.ID, request.ID, request.TargetSHA, err)
	}
	handle, err := mainEngine.StartPushBranch(cfg.Repo.MainWorktree, cfg.Repo.RemoteName, cfg.Repo.ProtectedBranch, shouldBypassGitHooks(cfg))
	if err != nil {
		return markPublishFailed(ctx, store, repoRecord.ID, request.ID, request.TargetSHA, err)
	}
	if publishLease != nil {
		_ = publishLease.UpdateMetadata(state.LeaseMetadata{
			Domain:    state.PublishLock,
			RepoRoot:  repoRecord.CanonicalPath,
			Owner:     "publish-worker",
			RequestID: request.ID,
			PID:       handle.PID(),
			CreatedAt: time.Now().UTC(),
		})
	}

	if _, err := handle.Wait(); err != nil {
		if errors.Is(err, git.ErrPushInterrupted) {
			replacement, ensureErr := latestReplacementPublishRequest(ctx, store, repoRecord.ID, request)
			if ensureErr != nil {
				return "", ensureErr
			}
			updated, updateErr := store.UpdatePublishRequestStatus(ctx, request.ID, "superseded", state.NullInt64(replacement.ID))
			if updateErr != nil {
				return "", updateErr
			}
			if eventErr := appendStateEvent(ctx, store, state.EventRecord{
				RepoID:    repoRecord.ID,
				ItemType:  "publish_request",
				ItemID:    state.NullInt64(updated.ID),
				EventType: "publish.superseded",
				Payload: mustJSON(map[string]string{
					"target_sha": request.TargetSHA,
					"reason":     "interrupted_for_newer_target",
				}),
			}); eventErr != nil {
				return "", eventErr
			}
			return fmt.Sprintf("Preempted publish request %d for newer target %s", request.ID, replacement.TargetSHA), nil
		}
		return markPublishFailed(ctx, store, repoRecord.ID, request.ID, request.TargetSHA, err)
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

func maybeRequestPublishPreemption(ctx context.Context, store state.Store, repoRecord state.RepositoryRecord, cfg policy.File, lockManager state.LockManager) (string, error) {
	metadata, err := lockManager.Metadata(state.PublishLock)
	if err != nil {
		return "Publish worker busy.", nil
	}
	if metadata.PID == 0 || metadata.RequestID == 0 {
		return "Publish worker busy.", nil
	}

	running, err := store.GetPublishRequest(ctx, metadata.RequestID)
	if err != nil {
		return "Publish worker busy.", nil
	}
	latest, err := store.LatestQueuedPublishRequest(ctx, repoRecord.ID)
	if err != nil {
		return "Publish worker busy.", nil
	}
	if latest.ID == running.ID || latest.TargetSHA == running.TargetSHA {
		return "Publish worker busy.", nil
	}

	if err := git.InterruptProcess(metadata.PID); err != nil {
		return "", err
	}
	return fmt.Sprintf("Requested publish preemption for newer target %s", latest.TargetSHA), nil
}

func markPublishFailed(ctx context.Context, store state.Store, repoID int64, requestID int64, targetSHA string, cause error) (string, error) {
	if _, updateErr := store.UpdatePublishRequestStatus(ctx, requestID, "failed", sql.NullInt64{}); updateErr != nil {
		return "", updateErr
	}
	if eventErr := appendStateEvent(ctx, store, state.EventRecord{
		RepoID:    repoID,
		ItemType:  "publish_request",
		ItemID:    state.NullInt64(requestID),
		EventType: "publish.failed",
		Payload: mustJSON(map[string]string{
			"target_sha": targetSHA,
			"error":      cause.Error(),
		}),
	}); eventErr != nil {
		return "", eventErr
	}
	return fmt.Sprintf("Failed publish request %d: %s", requestID, cause.Error()), nil
}

func latestReplacementPublishRequest(ctx context.Context, store state.Store, repoID int64, running state.PublishRequest) (state.PublishRequest, error) {
	requests, err := store.ListPublishRequests(ctx, repoID)
	if err != nil {
		return state.PublishRequest{}, err
	}
	var replacement state.PublishRequest
	for _, request := range requests {
		if request.Status != "queued" {
			continue
		}
		if request.ID == running.ID || request.TargetSHA == running.TargetSHA {
			continue
		}
		if replacement.ID == 0 || request.ID > replacement.ID {
			replacement = request
		}
	}
	if replacement.ID == 0 {
		return state.PublishRequest{}, fmt.Errorf("publish request %d was interrupted but no replacement request exists", running.ID)
	}
	return replacement, nil
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

func blockIntegrationSubmission(ctx context.Context, store state.Store, repoID int64, submissionID int64, cause error, details map[string]any) (string, error) {
	if _, err := store.UpdateIntegrationSubmissionStatus(ctx, submissionID, "blocked", cause.Error()); err != nil {
		return "", err
	}
	payload := map[string]any{
		"error": cause.Error(),
	}
	for key, value := range details {
		payload[key] = value
	}
	if err := appendSubmissionEvent(ctx, store, repoID, submissionID, "integration.blocked", payload); err != nil {
		return "", err
	}
	return fmt.Sprintf("Blocked submission %d: %s", submissionID, cause.Error()), nil
}

func failIntegrationSubmissionWithSync(ctx context.Context, store state.Store, repoID int64, submissionID int64, syncResult protectedSyncResult, cause error) (string, error) {
	result, err := failIntegrationSubmission(ctx, store, repoID, submissionID, cause)
	if err != nil {
		return "", err
	}
	return withProtectedSyncMessage(syncResult, result), nil
}

func blockIntegrationSubmissionWithSync(ctx context.Context, store state.Store, repoID int64, submissionID int64, syncResult protectedSyncResult, cause error, details ...map[string]any) (string, error) {
	var payload map[string]any
	if len(details) > 0 {
		payload = details[0]
	}
	result, err := blockIntegrationSubmission(ctx, store, repoID, submissionID, cause, payload)
	if err != nil {
		return "", err
	}
	return withProtectedSyncMessage(syncResult, result), nil
}

func appendSubmissionEvent(ctx context.Context, store state.Store, repoID int64, submissionID int64, eventType string, payload any) error {
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

func withProtectedSyncMessage(syncResult protectedSyncResult, result string) string {
	if !syncResult.Synced {
		return result
	}
	return fmt.Sprintf("Synced protected branch from %s before %s", syncResult.Upstream, lowerFirst(result))
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func recoverInterruptedIntegrationSubmissions(ctx context.Context, store state.Store, repoID int64) error {
	running, err := store.ListIntegrationSubmissionsByStatus(ctx, repoID, "running")
	if err != nil {
		return err
	}
	for _, submission := range running {
		if _, err := store.UpdateIntegrationSubmissionStatus(ctx, submission.ID, "queued", ""); err != nil {
			return err
		}
		if err := appendSubmissionEvent(ctx, store, repoID, submission.ID, "integration.recovered", map[string]string{
			"branch": submission.BranchName,
			"reason": "worker_restarted_without_active_lock",
		}); err != nil {
			return err
		}
	}
	return nil
}

func recoverInterruptedPublishRequests(ctx context.Context, store state.Store, repoID int64) error {
	running, err := store.ListPublishRequestsByStatus(ctx, repoID, "running")
	if err != nil {
		return err
	}
	for _, request := range running {
		if _, err := store.UpdatePublishRequestStatus(ctx, request.ID, "queued", request.SupersededBy); err != nil {
			return err
		}
		if err := appendStateEvent(ctx, store, state.EventRecord{
			RepoID:    repoID,
			ItemType:  "publish_request",
			ItemID:    state.NullInt64(request.ID),
			EventType: "publish.recovered",
			Payload: mustJSON(map[string]string{
				"target_sha": request.TargetSHA,
				"reason":     "worker_restarted_without_active_lock",
			}),
		}); err != nil {
			return err
		}
	}
	return nil
}

func mustJSON(payload any) json.RawMessage {
	data, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return data
}
