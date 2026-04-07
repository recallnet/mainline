package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

const (
	IntegrationLock = "integration"
	PublishLock     = "publish"
)

// LeaseMetadata describes a held or stale lock.
type LeaseMetadata struct {
	Domain    string    `json:"domain"`
	RepoRoot  string    `json:"repo_root"`
	Owner     string    `json:"owner"`
	Stage     string    `json:"stage,omitempty"`
	RequestID int64     `json:"request_id,omitempty"`
	PID       int       `json:"pid,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Lease is a held file lock.
type Lease struct {
	path string
	file *flock.Flock
}

// LockManager manages per-repo file locks.
type LockManager struct {
	repoRoot string
	gitDir   string
	baseDir  string
}

// NewLockManager returns a repo-local lock manager rooted in shared git storage.
func NewLockManager(repoRoot string, gitDir string) LockManager {
	return LockManager{
		repoRoot: repoRoot,
		gitDir:   gitDir,
		baseDir:  filepath.Join(gitDir, defaultDirName, "locks"),
	}
}

// Acquire obtains an exclusive lease for a domain.
func (m LockManager) Acquire(domain string, owner string) (*Lease, error) {
	if err := os.MkdirAll(m.baseDir, 0o755); err != nil {
		return nil, err
	}

	lockPath := filepath.Join(m.baseDir, domain+".lock")
	file := flock.New(lockPath)
	locked, err := file.TryLock()
	if err != nil {
		return nil, fmt.Errorf("acquire %s lock: %w", domain, err)
	}
	if !locked {
		return nil, ErrLockHeld
	}

	metadata := LeaseMetadata{
		Domain:    domain,
		RepoRoot:  m.repoRoot,
		Owner:     owner,
		CreatedAt: time.Now().UTC(),
	}
	if err := writeLeaseMetadata(lockPath+".json", metadata); err != nil {
		file.Unlock()
		return nil, err
	}

	return &Lease{path: lockPath, file: file}, nil
}

// Metadata returns the current lease metadata for a domain if present.
func (m LockManager) Metadata(domain string) (LeaseMetadata, error) {
	return readLeaseMetadata(filepath.Join(m.baseDir, domain+".lock.json"))
}

// InspectStale returns stale leases older than the given threshold.
func (m LockManager) InspectStale(olderThan time.Duration) ([]LeaseMetadata, error) {
	entries, err := os.ReadDir(m.baseDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	now := time.Now().UTC()
	var stale []LeaseMetadata
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		metadata, err := readLeaseMetadata(filepath.Join(m.baseDir, entry.Name()))
		if err != nil {
			return nil, err
		}

		if now.Sub(metadata.CreatedAt) >= olderThan {
			stale = append(stale, metadata)
		}
	}

	return stale, nil
}

// Release unlocks and removes the lease metadata.
func (l *Lease) Release() error {
	if l == nil || l.file == nil {
		return nil
	}

	defer os.Remove(l.path + ".json")
	return l.file.Unlock()
}

// UpdateMetadata overwrites the persisted metadata for an active lease.
func (l *Lease) UpdateMetadata(metadata LeaseMetadata) error {
	if l == nil {
		return nil
	}
	return writeLeaseMetadata(l.path+".json", metadata)
}

var ErrLockHeld = errors.New("lock is already held")

func writeLeaseMetadata(path string, metadata LeaseMetadata) error {
	payload, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	return os.WriteFile(path, payload, 0o644)
}

func readLeaseMetadata(path string) (LeaseMetadata, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return LeaseMetadata{}, err
	}

	var metadata LeaseMetadata
	if err := json.Unmarshal(payload, &metadata); err != nil {
		return LeaseMetadata{}, err
	}

	return metadata, nil
}
