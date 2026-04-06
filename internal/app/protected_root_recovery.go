package app

import (
	"context"
	"fmt"
	"os"

	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/policy"
	"github.com/recallnet/mainline/internal/state"
)

type protectedRootRecoveryMode int

const (
	protectedRootRecoveryRequireIdle protectedRootRecoveryMode = iota
	protectedRootRecoveryAllowQueued
)

func ensureProtectedRootHealthy(ctx context.Context, engine git.Engine, cfg policy.File, store state.Store, repoRecord state.RepositoryRecord, mode protectedRootRecoveryMode) (git.HealthReport, error) {
	report, err := engine.InspectHealth(cfg.Repo.ProtectedBranch, cfg.Repo.MainWorktree)
	if err != nil {
		return git.HealthReport{}, err
	}
	if !report.MainWorktreeExists {
		return git.HealthReport{}, fmt.Errorf("main worktree %s is missing", cfg.Repo.MainWorktree)
	}
	if !report.ProtectedBranchExists {
		return git.HealthReport{}, fmt.Errorf("protected branch %q does not exist", cfg.Repo.ProtectedBranch)
	}
	if report.MainWorktreeDetached {
		return git.HealthReport{}, fmt.Errorf("configured main worktree %s is detached; switch it with `git checkout --ignore-other-worktrees %s` before retrying", cfg.Repo.MainWorktree, cfg.Repo.ProtectedBranch)
	}
	if report.MainWorktreeBranch != "" && report.MainWorktreeBranch != cfg.Repo.ProtectedBranch {
		return git.HealthReport{}, fmt.Errorf("configured main worktree %s is on branch %s, expected %s; switch it with `git checkout --ignore-other-worktrees %s` before retrying", cfg.Repo.MainWorktree, report.MainWorktreeBranch, cfg.Repo.ProtectedBranch, cfg.Repo.ProtectedBranch)
	}
	if report.ProtectedBranchClean && !report.HasDivergedUpstream {
		return report, nil
	}

	repaired, _, repairErr := tryRepairCanonicalProtectedRootWithMode(ctx, engine, cfg, store, repoRecord, mode)
	if repairErr != nil {
		return git.HealthReport{}, repairErr
	}
	if repaired {
		report, err = engine.InspectHealth(cfg.Repo.ProtectedBranch, cfg.Repo.MainWorktree)
		if err != nil {
			return git.HealthReport{}, err
		}
	}

	if !report.ProtectedBranchClean {
		return git.HealthReport{}, protectedWorktreeDirtyError(cfg.Repo.MainWorktree, report.ProtectedDirtyPaths)
	}
	if report.HasDivergedUpstream {
		return git.HealthReport{}, fmt.Errorf("protected branch %q has diverged from upstream %s", cfg.Repo.ProtectedBranch, report.UpstreamRef)
	}
	return report, nil
}

func tryRepairCanonicalProtectedRootWithMode(ctx context.Context, engine git.Engine, cfg policy.File, store state.Store, repoRecord state.RepositoryRecord, mode protectedRootRecoveryMode) (bool, string, error) {
	if cfg.Repo.MainWorktree == "" {
		return false, "", nil
	}
	if _, err := os.Stat(cfg.Repo.MainWorktree); err != nil {
		return false, fmt.Sprintf("left protected root dirty because main worktree %s could not be inspected", cfg.Repo.MainWorktree), nil
	}
	operation, err := engine.InProgressOperation(cfg.Repo.MainWorktree)
	if err != nil {
		return false, "", err
	}
	if operation != "" {
		aborted, err := engine.AbortInProgressOperation(cfg.Repo.MainWorktree)
		if err != nil {
			return false, fmt.Sprintf("left protected root dirty because %s could not be aborted: %v", operation, err), nil
		}
		if aborted != "" {
			return true, fmt.Sprintf("aborted %s on protected branch worktree", aborted), nil
		}
	}
	return tryRepairCanonicalProtectedRoot(ctx, engine, cfg, store, repoRecord, mode == protectedRootRecoveryRequireIdle)
}
