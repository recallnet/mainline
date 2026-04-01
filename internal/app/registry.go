package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const globalRegistryVersion = "v1"

type registeredRepo struct {
	RepositoryRoot string    `json:"repository_root"`
	MainWorktree   string    `json:"main_worktree"`
	StatePath      string    `json:"state_path"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type registryFile struct {
	Version      string           `json:"version"`
	Repositories []registeredRepo `json:"repositories"`
}

func globalRegistryPath() (string, error) {
	if override := os.Getenv("MAINLINE_REGISTRY_PATH"); override != "" {
		return override, nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "mainline", "registry.json"), nil
}

func mustGlobalRegistryPath() string {
	path, err := globalRegistryPath()
	if err != nil {
		return ""
	}
	return path
}

func loadRegisteredRepos() ([]registeredRepo, error) {
	path, err := globalRegistryPath()
	if err != nil {
		return nil, err
	}
	return loadRegisteredReposFromPath(path)
}

func loadRegisteredReposFromPath(path string) ([]registeredRepo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var registry registryFile
	if err := json.Unmarshal(data, &registry); err != nil {
		return nil, err
	}
	sort.Slice(registry.Repositories, func(i, j int) bool {
		return registry.Repositories[i].RepositoryRoot < registry.Repositories[j].RepositoryRoot
	})
	return registry.Repositories, nil
}

func registerRepo(mainWorktree string, repoRoot string, statePath string) error {
	path, err := globalRegistryPath()
	if err != nil {
		return err
	}

	mainWorktree = canonicalRegistryPath(mainWorktree)
	repoRoot = canonicalRegistryPath(repoRoot)
	statePath = canonicalRegistryPath(statePath)

	repositories, err := loadRegisteredReposFromPath(path)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	replaced := false
	for i := range repositories {
		if repositories[i].RepositoryRoot != repoRoot {
			continue
		}
		repositories[i].MainWorktree = mainWorktree
		repositories[i].StatePath = statePath
		repositories[i].UpdatedAt = now
		replaced = true
		break
	}
	if !replaced {
		repositories = append(repositories, registeredRepo{
			RepositoryRoot: repoRoot,
			MainWorktree:   mainWorktree,
			StatePath:      statePath,
			UpdatedAt:      now,
		})
	}
	sort.Slice(repositories, func(i, j int) bool {
		return repositories[i].RepositoryRoot < repositories[j].RepositoryRoot
	})

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(registryFile{
		Version:      globalRegistryVersion,
		Repositories: repositories,
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(payload, '\n'), 0o644)
}

func canonicalRegistryPath(path string) string {
	if path == "" {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(path)
}
