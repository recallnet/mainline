package policy

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

const FileName = "mainline.toml"

// File is the persisted repository configuration structure.
type File struct {
	Repo        RepoConfig        `toml:"repo"`
	Integration IntegrationConfig `toml:"integration"`
	Publish     PublishConfig     `toml:"publish"`
}

// ConfigPath returns the config path for a repository root.
func ConfigPath(repoRoot string) string {
	return filepath.Join(repoRoot, FileName)
}

// LoadFile loads a repository config from disk.
func LoadFile(repoRoot string) (File, error) {
	path := ConfigPath(repoRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		return File{}, err
	}

	var cfg File
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return File{}, err
	}

	return cfg, nil
}

// SaveFile writes a repository config to disk.
func SaveFile(repoRoot string, cfg File) error {
	data, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}

	path := ConfigPath(repoRoot)
	return os.WriteFile(path, data, 0o644)
}

// LoadOrDefault loads a config if present or returns defaults.
func LoadOrDefault(repoRoot string) (File, bool, error) {
	cfg, err := LoadFile(repoRoot)
	if err == nil {
		return cfg, true, nil
	}

	if errors.Is(err, os.ErrNotExist) {
		return DefaultFile(), false, nil
	}

	return File{}, false, err
}

// DefaultFile returns the default persisted config scaffold.
func DefaultFile() File {
	defaults := DefaultConfig()
	return File{
		Repo:        defaults.Repo,
		Integration: defaults.Integration,
		Publish:     defaults.Publish,
	}
}
