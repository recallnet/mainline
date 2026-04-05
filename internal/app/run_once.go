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

	"github.com/recallnet/mainline/internal/domain"
	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/policy"
	"github.com/recallnet/mainline/internal/state"
)

func runRunOnce(args []string, stdout *stepPrinter, stderr io.Writer) error {
	fs := flag.NewFlagSet(currentCLIProgramName()+" run-once", flag.ContinueOnError)
	fs.SetOutput(stderr)
	setFlagUsage(fs, fmt.Sprintf(`Usage:
  %s run-once [flags]

Run one serialized integration or publish cycle from the protected worktree.

Examples:
  mq run-once --repo /path/to/repo-root
  mq run-once --repo /path/to/repo-root --json

Flags:
`, currentCLIProgramName()))

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
		encoder := json.NewEncoder(stdout.Raw())
		encoder.SetIndent("", "  ")
		return encoder.Encode(map[string]any{
			"ok":     true,
			"repo":   repoPath,
			"result": result,
		})
	}
	stdout.Line("%s", result)
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

	if _, err := ensureProtectedRootHealthy(
		context.Background(),
		git.NewEngine(mainLayout.WorktreeRoot),
		cfg,
		store,
		repoRecord,
		protectedRootRecoveryAllowQueued,
	); err != nil {
		return "", err
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

	if _, err := recoverInterruptedIntegrationSubmissions(ctx, store, repoRecord.ID); err != nil {
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
		if _, err := store.UpdateIntegrationSubmissionStatus(ctx, submission.ID, domain.SubmissionStatusRunning, ""); err != nil {
			return "", err
		}
		if err := appendSubmissionLifecycleEvent(ctx, store, repoRecord.ID, submission.ID, domain.EventTypeIntegrationStarted, domain.SubmissionLifecyclePayload{
			Branch:         submissionDisplayRef(submission),
			SourceRef:      submission.SourceRef,
			RefKind:        submission.RefKind,
			SourceWorktree: submission.SourceWorktree,
			SourceSHA:      submission.SourceSHA,
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

	if _, err := recoverInterruptedPublishRequests(ctx, store, repoRecord.ID); err != nil {
		return "", err
	}

	result, err := processPublishRequest(ctx, store, repoRecord, cfg, publishLease)
	if err != nil {
		return "", err
	}
	return result, nil
}

var publishRetryBackoffs = []time.Duration{
	time.Second,
	5 * time.Second,
	30 * time.Second,
}

func processPublishRequest(ctx context.Context, store state.Store, repoRecord state.RepositoryRecord, cfg policy.File, publishLease *state.Lease) (string, error) {
	mainEngine := git.NewEngine(cfg.Repo.MainWorktree)
	now := time.Now().UTC()

	request, err := store.LatestReadyQueuedPublishRequest(ctx, repoRecord.ID, now)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			delayed, delayedErr := store.NextDelayedQueuedPublishRequest(ctx, repoRecord.ID, now)
			if delayedErr == nil && delayed.NextAttemptAt.Valid {
				return fmt.Sprintf("No ready publish requests. Next retry for request %d at %s", delayed.ID, delayed.NextAttemptAt.Time.UTC().Format(time.RFC3339)), nil
			}
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
		replacement, created, err := ensureLatestPublishRequestRecord(ctx, store, repoRecord.ID, currentProtectedSHA, request.Priority)
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
		return handlePublishFailure(ctx, store, repoRecord.ID, request, err)
	}
	handle, err := mainEngine.StartPushBranch(cfg.Repo.MainWorktree, cfg.Repo.RemoteName, cfg.Repo.ProtectedBranch, shouldBypassGitHooks(cfg))
	if err != nil {
		return handlePublishFailure(ctx, store, repoRecord.ID, request, err)
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
		return handlePublishFailure(ctx, store, repoRecord.ID, request, err)
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
		replacement, created, err := ensureLatestPublishRequestRecord(ctx, store, repoRecord.ID, latestProtectedSHA, request.Priority)
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

func handlePublishFailure(ctx context.Context, store state.Store, repoID int64, request state.PublishRequest, cause error) (string, error) {
	if delay, ok := publishRetryDelay(request.AttemptCount, cause); ok {
		retryAt := time.Now().UTC().Add(delay)
		updated, err := store.SchedulePublishRetry(ctx, request.ID, request.AttemptCount+1, retryAt)
		if err != nil {
			return "", err
		}
		if eventErr := appendStateEvent(ctx, store, state.EventRecord{
			RepoID:    repoID,
			ItemType:  "publish_request",
			ItemID:    state.NullInt64(request.ID),
			EventType: "publish.retry_scheduled",
			Payload: mustJSON(map[string]any{
				"target_sha":        request.TargetSHA,
				"error":             cause.Error(),
				"attempt_count":     updated.AttemptCount,
				"next_attempt_at":   retryAt.Format(time.RFC3339),
				"backoff_seconds":   int(delay / time.Second),
				"retry_recommended": true,
			}),
		}); eventErr != nil {
			return "", eventErr
		}
		return fmt.Sprintf("Scheduled publish retry %d for request %d at %s", updated.AttemptCount, updated.ID, retryAt.Format(time.RFC3339)), nil
	}
	return markPublishFailed(ctx, store, repoID, request.ID, request.TargetSHA, cause)
}

func publishRetryDelay(attemptCount int, cause error) (time.Duration, bool) {
	if !isTransientPublishError(cause) {
		return 0, false
	}
	if attemptCount < 0 || attemptCount >= len(publishRetryBackoffs) {
		return 0, false
	}
	return publishRetryBackoffs[attemptCount], true
}

func isTransientPublishError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	nonTransientIndicators := []string{
		"gate failed",
		"pre-push hook",
		"hook declined",
		"protected branch worktree",
		"rejected by hook",
	}
	for _, indicator := range nonTransientIndicators {
		if strings.Contains(text, indicator) {
			return false
		}
	}
	transientIndicators := []string{
		"connection reset",
		"connection timed out",
		"connection timeout",
		"timed out",
		"timeout",
		"temporary failure",
		"try again",
		"rate limit",
		"429",
		"http 500",
		"http 502",
		"http 503",
		"http 504",
		"returned error: 429",
		"returned error: 500",
		"returned error: 502",
		"returned error: 503",
		"returned error: 504",
		"internal server error",
		"remote end hung up unexpectedly",
		"the remote end hung up unexpectedly",
		"unexpected disconnect",
		"connection closed by remote host",
		"tls handshake timeout",
		"network is unreachable",
		"could not resolve host",
		"transport endpoint is not connected",
	}
	for _, indicator := range transientIndicators {
		if strings.Contains(text, indicator) {
			return true
		}
	}
	return false
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
	if !shouldPreemptPublish(running, latest) {
		return "Publish worker busy.", nil
	}

	if err := git.InterruptProcess(metadata.PID); err != nil {
		return "", err
	}
	return fmt.Sprintf("Requested publish preemption for %s priority target %s over running %s priority target %s", latest.Priority, latest.TargetSHA, running.Priority, running.TargetSHA), nil
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
		if !shouldPreemptPublish(running, request) {
			continue
		}
		if replacement.ID == 0 || publishPriorityRank(request.Priority) < publishPriorityRank(replacement.Priority) || (request.Priority == replacement.Priority && request.ID > replacement.ID) {
			replacement = request
		}
	}
	if replacement.ID == 0 {
		return state.PublishRequest{}, fmt.Errorf("publish request %d was interrupted but no replacement request exists", running.ID)
	}
	return replacement, nil
}

func ensureLatestPublishRequestRecord(ctx context.Context, store state.Store, repoID int64, targetSHA string, priority string) (state.PublishRequest, bool, error) {
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
		Priority:  priority,
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
			"priority":   priority,
		}),
	}); err != nil {
		return state.PublishRequest{}, false, err
	}

	return request, true, nil
}

func failIntegrationSubmission(ctx context.Context, store state.Store, repoID int64, submission state.IntegrationSubmission, cause error) (string, error) {
	if _, err := store.UpdateIntegrationSubmissionStatus(ctx, submission.ID, domain.SubmissionStatusFailed, cause.Error()); err != nil {
		return "", err
	}
	if err := appendSubmissionLifecycleEvent(ctx, store, repoID, submission.ID, domain.EventTypeIntegrationFailed, domain.SubmissionLifecyclePayload{
		Branch:         submissionDisplayRef(submission),
		SourceRef:      submission.SourceRef,
		RefKind:        submission.RefKind,
		SourceWorktree: submission.SourceWorktree,
		SourceSHA:      submission.SourceSHA,
		Error:          cause.Error(),
	}); err != nil {
		return "", err
	}
	return fmt.Sprintf("Failed submission %d: %s", submission.ID, cause.Error()), nil
}

func blockIntegrationSubmission(ctx context.Context, store state.Store, repoID int64, submissionID int64, cause error, details map[string]any) (string, error) {
	if _, err := store.UpdateIntegrationSubmissionStatus(ctx, submissionID, domain.SubmissionStatusBlocked, cause.Error()); err != nil {
		return "", err
	}
	payload := domain.IntegrationBlockedPayload{Error: cause.Error()}
	raw := mustJSON(details)
	if len(details) > 0 {
		if err := json.Unmarshal(raw, &payload); err != nil {
			return "", err
		}
		payload.Error = cause.Error()
	}
	if err := appendIntegrationBlockedEvent(ctx, store, repoID, submissionID, payload); err != nil {
		return "", err
	}
	return fmt.Sprintf("Blocked submission %d: %s", submissionID, cause.Error()), nil
}

func failIntegrationSubmissionWithSync(ctx context.Context, store state.Store, repoID int64, submission state.IntegrationSubmission, syncResult protectedSyncResult, cause error) (string, error) {
	result, err := failIntegrationSubmission(ctx, store, repoID, submission, cause)
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

func appendSubmissionEvent(ctx context.Context, store state.Store, repoID int64, submissionID int64, eventType domain.EventType, payload any) error {
	return appendStateEvent(ctx, store, state.EventRecord{
		RepoID:    repoID,
		ItemType:  domain.ItemTypeIntegrationSubmission,
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

func recoverInterruptedIntegrationSubmissions(ctx context.Context, store state.Store, repoID int64) (int, error) {
	running, err := store.ListIntegrationSubmissionsByStatus(ctx, repoID, domain.SubmissionStatusRunning)
	if err != nil {
		return 0, err
	}
	recovered := 0
	for _, submission := range running {
		if _, err := store.UpdateIntegrationSubmissionStatus(ctx, submission.ID, domain.SubmissionStatusQueued, ""); err != nil {
			return recovered, err
		}
		if err := appendSubmissionLifecycleEvent(ctx, store, repoID, submission.ID, domain.EventTypeIntegrationRecovered, domain.SubmissionLifecyclePayload{
			Branch:    submissionDisplayRef(submission),
			SourceRef: submission.SourceRef,
			RefKind:   submission.RefKind,
			Error:     "worker_restarted_without_active_lock",
		}); err != nil {
			return recovered, err
		}
		recovered++
	}
	return recovered, nil
}

func supersedeObsoleteSubmissions(ctx context.Context, store state.Store, repoID int64, engine git.Engine, landed state.IntegrationSubmission, protectedSHA string) error {
	if landed.SourceRef == "" {
		return nil
	}
	submissions, err := store.ListIntegrationSubmissions(ctx, repoID)
	if err != nil {
		return err
	}
	for _, submission := range submissions {
		if submission.ID == landed.ID {
			continue
		}
		if submission.SourceRef != landed.SourceRef || submission.RefKind != landed.RefKind {
			continue
		}
		if submission.Status != domain.SubmissionStatusQueued && submission.Status != domain.SubmissionStatusBlocked {
			continue
		}
		descends, err := engine.IsAncestor(submission.SourceSHA, landed.SourceSHA)
		if err != nil || !descends {
			continue
		}
		reason := fmt.Sprintf("superseded by submission %d landing newer branch tip %s", landed.ID, landed.SourceSHA)
		if protectedSHA != "" {
			reason = fmt.Sprintf("%s at %s", reason, protectedSHA)
		}
		if _, err := store.UpdateIntegrationSubmissionStatus(ctx, submission.ID, domain.SubmissionStatusSuperseded, reason); err != nil {
			return err
		}
		if err := appendSubmissionEvent(ctx, store, repoID, submission.ID, domain.EventType("submission.superseded"), map[string]string{
			"branch":               submissionDisplayRef(submission),
			"source_ref":           submission.SourceRef,
			"ref_kind":             string(submission.RefKind),
			"source_sha":           submission.SourceSHA,
			"superseded_by_id":     fmt.Sprintf("%d", landed.ID),
			"superseded_by_source": landed.SourceSHA,
			"superseded_protected": protectedSHA,
			"reason":               reason,
		}); err != nil {
			return err
		}
	}
	return nil
}

func recoverInterruptedPublishRequests(ctx context.Context, store state.Store, repoID int64) (int, error) {
	running, err := store.ListPublishRequestsByStatus(ctx, repoID, "running")
	if err != nil {
		return 0, err
	}
	recovered := 0
	for _, request := range running {
		if _, err := store.UpdatePublishRequestStatus(ctx, request.ID, "queued", request.SupersededBy); err != nil {
			return recovered, err
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
			return recovered, err
		}
		recovered++
	}
	return recovered, nil
}

func mustJSON(payload any) json.RawMessage {
	data, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return data
}
