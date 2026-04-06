package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/recallnet/mainline/internal/domain"
	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/policy"
	"github.com/recallnet/mainline/internal/state"
)

func processIntegrationSubmission(ctx context.Context, store state.Store, repoRecord state.RepositoryRecord, cfg policy.File, sharedGitDir string, submission state.IntegrationSubmission) (string, error) {
	mainLayout, err := git.DiscoverRepositoryLayout(cfg.Repo.MainWorktree)
	if err != nil {
		return "", err
	}
	mainEngine := git.NewEngine(mainLayout.WorktreeRoot)

	syncResult, err := syncProtectedBranch(mainEngine, cfg)
	if err != nil {
		return failIntegrationSubmission(ctx, store, repoRecord.ID, submission, err)
	}
	if syncResult.Synced {
		if err := appendStateEvent(ctx, store, state.EventRecord{
			RepoID:    repoRecord.ID,
			ItemType:  domain.ItemTypeRepository,
			EventType: domain.EventTypeProtectedSyncedFromUpstream,
			Payload: mustJSON(domain.ProtectedSyncedPayload{
				ProtectedBranch: cfg.Repo.ProtectedBranch,
				Upstream:        syncResult.Upstream,
				BeforeSHA:       syncResult.BeforeSHA,
				AfterSHA:        syncResult.AfterSHA,
			}),
		}); err != nil {
			return "", err
		}
	}

	sourceLayout, err := git.DiscoverRepositoryLayout(submission.SourceWorktree)
	if err != nil {
		return failIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission, syncResult, fmt.Errorf("source worktree is unavailable: %w", err))
	}
	if filepath.Clean(sourceLayout.GitDir) != filepath.Clean(sharedGitDir) {
		return failIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission, syncResult, fmt.Errorf("source worktree %s no longer belongs to repository %s", submission.SourceWorktree, repoRecord.CanonicalPath))
	}

	sourceEngine := git.NewEngine(submission.SourceWorktree)
	worktree, err := sourceEngine.ResolveWorktree(submission.SourceWorktree)
	if err != nil {
		return failIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission, syncResult, err)
	}
	if submission.RefKind == submissionRefKindBranch && worktree.IsDetached {
		return failIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission, syncResult, fmt.Errorf("source worktree %s is detached; check out %q or resubmit by sha", submission.SourceWorktree, submission.SourceRef))
	}
	if submission.RefKind == submissionRefKindBranch && worktree.Branch != submission.BranchName {
		return failIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission, syncResult, fmt.Errorf("source worktree %s is on %q, expected %q", submission.SourceWorktree, worktree.Branch, submission.BranchName))
	}

	clean, err := sourceEngine.WorktreeIsClean(submission.SourceWorktree)
	if err != nil {
		return "", err
	}
	if !clean {
		return failIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission, syncResult, fmt.Errorf("source worktree %s is dirty; clean it and resubmit", submission.SourceWorktree))
	}

	var headSHA string
	if submission.RefKind == submissionRefKindBranch {
		headSHA, err = sourceEngine.BranchHeadSHA(submission.SourceRef)
		if err != nil {
			return failIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission, syncResult, fmt.Errorf("resolve branch head for %q: %w", submission.SourceRef, err))
		}
		if headSHA != submission.SourceSHA {
			if !submission.AllowNewerHead {
				return failIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission, syncResult, fmt.Errorf("branch %q moved from submitted SHA %s to %s; resubmit the branch or submit with --allow-newer-head", submission.SourceRef, submission.SourceSHA, headSHA))
			}
			descends, descendsErr := sourceEngine.IsAncestor(submission.SourceSHA, headSHA)
			if descendsErr != nil {
				return failIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission, syncResult, descendsErr)
			}
			if !descends {
				return failIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission, syncResult, fmt.Errorf("branch %q moved from submitted SHA %s to non-descendant SHA %s; resubmit the branch", submission.SourceRef, submission.SourceSHA, headSHA))
			}
		}
	} else {
		headSHA = worktree.HeadSHA
		if headSHA != submission.SourceSHA {
			return failIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission, syncResult, fmt.Errorf("detached worktree %s moved from submitted SHA %s to %s; resubmit the commit", submission.SourceWorktree, submission.SourceSHA, headSHA))
		}
	}
	protectedTipSHA, err := mainEngine.BranchHeadSHA(cfg.Repo.ProtectedBranch)
	if err != nil {
		return failIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission, syncResult, err)
	}

	if err := runConfiguredChecks(cfg.Checks.PreIntegrate, submission.SourceWorktree, cfg.Checks.CommandTimeout); err != nil {
		if timeoutErr, ok := isCheckTimeoutError(err); ok {
			return blockIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission.ID, syncResult,
				fmt.Errorf("check_timeout: pre-integrate check timed out: %w", timeoutErr),
				map[string]any{
					"branch":             submissionDisplayRef(submission),
					"source_ref":         submission.SourceRef,
					"ref_kind":           submission.RefKind,
					"blocked_reason":     domain.BlockedReasonCheckTimeout,
					"protected_tip_sha":  protectedTipSHA,
					"retry_hint":         "rerun-after-fixing-hanging-check",
					"retry_recommended":  false,
					"source_worktree":    submission.SourceWorktree,
					"protected_branch":   cfg.Repo.ProtectedBranch,
					"protected_upstream": syncResult.Upstream,
					"timeout":            timeoutErr.EffectiveTimeout.String(),
				},
			)
		}
		return blockIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission.ID, syncResult, fmt.Errorf("pre-integrate checks failed: %w", err))
	}

	if err := applyAppTestFault("integration.rebase"); err != nil {
		return failIntegrationSubmission(ctx, store, repoRecord.ID, submission, err)
	}
	if err := sourceEngine.RebaseCurrentBranch(submission.SourceWorktree, cfg.Repo.ProtectedBranch); err != nil {
		if errors.Is(err, git.ErrRebaseConflict) {
			conflictFiles, conflictErr := sourceEngine.ConflictedFiles(submission.SourceWorktree)
			if conflictErr != nil {
				return failIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission, syncResult, conflictErr)
			}
			return blockIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission.ID, syncResult,
				fmt.Errorf("rebase conflict in %s: resolve in the source worktree and resubmit", submission.SourceWorktree),
				map[string]any{
					"branch":             submissionDisplayRef(submission),
					"source_ref":         submission.SourceRef,
					"ref_kind":           submission.RefKind,
					"blocked_reason":     domain.BlockedReasonRebaseConflict,
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
		if errors.Is(err, git.ErrRebaseEmpty) {
			return blockIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission.ID, syncResult,
				fmt.Errorf("rebase stopped in %s because a queued commit became empty on top of %s; inspect the source worktree and resubmit if more changes are needed", submission.SourceWorktree, cfg.Repo.ProtectedBranch),
				map[string]any{
					"branch":             submissionDisplayRef(submission),
					"source_ref":         submission.SourceRef,
					"ref_kind":           submission.RefKind,
					"blocked_reason":     domain.BlockedReasonRebaseEmpty,
					"protected_tip_sha":  protectedTipSHA,
					"retry_hint":         "inspect-empty-rebase-then-resubmit",
					"retry_recommended":  false,
					"source_worktree":    submission.SourceWorktree,
					"protected_branch":   cfg.Repo.ProtectedBranch,
					"protected_upstream": syncResult.Upstream,
				},
			)
		}
		return failIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission, syncResult, err)
	}

	targetRef := submission.SourceRef
	if submission.RefKind == submissionRefKindSHA {
		targetRef, err = sourceEngine.WorktreeHeadSHA(submission.SourceWorktree)
		if err != nil {
			return failIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission, syncResult, err)
		}
	}

	if err := applyAppTestFault("integration.fast_forward"); err != nil {
		return failIntegrationSubmission(ctx, store, repoRecord.ID, submission, err)
	}
	if err := mainEngine.FastForwardCurrentBranch(cfg.Repo.MainWorktree, targetRef); err != nil {
		return failIntegrationSubmissionWithSync(ctx, store, repoRecord.ID, submission, syncResult, err)
	}

	protectedHead, err := mainEngine.BranchHeadSHA(cfg.Repo.ProtectedBranch)
	if err != nil {
		return "", err
	}

	if _, err := store.UpdateIntegrationSubmissionStatus(ctx, submission.ID, domain.SubmissionStatusSucceeded, ""); err != nil {
		return "", err
	}
	if err := appendSubmissionLifecycleEvent(ctx, store, repoRecord.ID, submission.ID, domain.EventTypeIntegrationSucceeded, domain.SubmissionLifecyclePayload{
		Branch:       submissionDisplayRef(submission),
		SourceRef:    submission.SourceRef,
		RefKind:      submission.RefKind,
		ProtectedSHA: protectedHead,
	}); err != nil {
		return "", err
	}
	if err := supersedeObsoleteSubmissions(ctx, store, repoRecord.ID, mainEngine, submission, protectedHead); err != nil {
		return "", err
	}

	if cfg.Publish.Mode == "auto" {
		request, err := store.CreatePublishRequest(ctx, state.PublishRequest{
			RepoID:    repoRecord.ID,
			TargetSHA: protectedHead,
			Priority:  submission.Priority,
			Status:    domain.PublishStatusQueued,
		})
		if err != nil {
			return "", err
		}
		if err := appendStateEvent(ctx, store, state.EventRecord{
			RepoID:    repoRecord.ID,
			ItemType:  domain.ItemTypePublishRequest,
			ItemID:    state.NullInt64(request.ID),
			EventType: domain.EventTypePublishRequested,
			Payload: mustJSON(domain.PublishRequestedPayload{
				TargetSHA: protectedHead,
				Reason:    "integration_succeeded",
				Priority:  submission.Priority,
				Branch:    submissionDisplayRef(submission),
				SourceRef: submission.SourceRef,
				RefKind:   submission.RefKind,
			}),
		}); err != nil {
			return "", err
		}
	}

	if syncResult.Synced {
		return fmt.Sprintf("Synced %s from %s and integrated submission %d from %s onto %s", cfg.Repo.ProtectedBranch, syncResult.Upstream, submission.ID, submissionDisplayRef(submission), cfg.Repo.ProtectedBranch), nil
	}

	return fmt.Sprintf("Integrated submission %d from %s onto %s", submission.ID, submissionDisplayRef(submission), cfg.Repo.ProtectedBranch), nil
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
	return protectedSyncResult{Synced: true, Upstream: status.Upstream, BeforeSHA: beforeSHA, AfterSHA: afterSHA}, nil
}
