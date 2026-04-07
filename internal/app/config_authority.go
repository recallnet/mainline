package app

import (
	"context"
	"os"
	"path/filepath"

	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/policy"
	"github.com/recallnet/mainline/internal/state"
)

type configAuthority struct {
	Root    string
	Path    string
	File    policy.File
	Present bool
}

func loadConfigAuthority(ctx context.Context, layout git.RepositoryLayout, store state.Store, fallbackMainWorktree string) (configAuthority, error) {
	root := resolveConfigAuthorityRoot(ctx, layout, store, fallbackMainWorktree)
	cfg, present, err := policy.LoadOrDefault(root)
	if err != nil {
		return configAuthority{}, err
	}
	if cfg.Repo.MainWorktree == "" {
		cfg.Repo.MainWorktree = canonicalRegistryPath(root)
	}
	return configAuthority{
		Root:    root,
		Path:    policy.ConfigPath(root),
		File:    cfg,
		Present: present,
	}, nil
}

func saveConfigAuthority(root string, cfg policy.File) error {
	cfg.Repo.MainWorktree = canonicalRegistryPath(cfg.Repo.MainWorktree)
	return policy.SaveFile(root, cfg)
}

func resolveConfigAuthorityRoot(ctx context.Context, layout git.RepositoryLayout, store state.Store, fallbackMainWorktree string) string {
	if store.Exists() {
		if record, err := store.GetRepositoryByPath(ctx, layout.RepositoryRoot); err == nil && record.MainWorktree != "" && pathExists(record.MainWorktree) {
			return canonicalRegistryPath(record.MainWorktree)
		}
	}
	if fallbackMainWorktree != "" {
		candidate := canonicalRegistryPath(fallbackMainWorktree)
		if pathExists(candidate) {
			return candidate
		}
	}
	if repositoryRootHasWorktree(layout.RepositoryRoot) {
		return canonicalRegistryPath(layout.RepositoryRoot)
	}
	if layout.WorktreeRoot != "" {
		return canonicalRegistryPath(layout.WorktreeRoot)
	}
	return canonicalRegistryPath(layout.RepositoryRoot)
}

func resolveConfigAuthorityRootForPath(path string) string {
	return canonicalRegistryPath(filepath.Clean(path))
}

func repositoryRootHasWorktree(repoRoot string) bool {
	if repoRoot == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(repoRoot, ".git"))
	return err == nil && info != nil
}
