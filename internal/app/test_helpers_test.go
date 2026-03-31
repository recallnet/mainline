package app

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runTestCommand(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()

	cmd := exec.Command(name, args...)
	cmd.Dir = dir

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %v failed: %v: %s", name, args, err, stderr.String())
	}

	return stdout.String()
}

func createBareCloneWorktree(t *testing.T) (string, string) {
	t.Helper()

	seedRoot := t.TempDir()
	runTestCommand(t, seedRoot, "git", "init", "-b", "main")
	runTestCommand(t, seedRoot, "git", "config", "user.name", "Test User")
	runTestCommand(t, seedRoot, "git", "config", "user.email", "test@example.com")
	runTestCommand(t, seedRoot, "git", "config", "core.hooksPath", ".git/hooks")

	readme := filepath.Join(seedRoot, "README.md")
	if err := os.WriteFile(readme, []byte("# test\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	runTestCommand(t, seedRoot, "git", "add", "README.md")
	runTestCommand(t, seedRoot, "git", "commit", "-m", "initial")

	bareDir := filepath.Join(t.TempDir(), "repo.git")
	runTestCommand(t, t.TempDir(), "git", "clone", "--bare", seedRoot, bareDir)

	worktreePath := filepath.Join(t.TempDir(), "main-worktree")
	runTestCommand(t, seedRoot, "git", "--git-dir", bareDir, "worktree", "add", worktreePath, "main")

	return bareDir, worktreePath
}

func createTestRepoWithRemote(t *testing.T) (string, string) {
	t.Helper()

	remoteDir := filepath.Join(t.TempDir(), "origin.git")
	runTestCommand(t, t.TempDir(), "git", "init", "--bare", remoteDir)

	repoRoot := t.TempDir()
	runTestCommand(t, repoRoot, "git", "init", "-b", "main")
	runTestCommand(t, repoRoot, "git", "config", "user.name", "Test User")
	runTestCommand(t, repoRoot, "git", "config", "user.email", "test@example.com")
	runTestCommand(t, repoRoot, "git", "config", "core.hooksPath", ".git/hooks")

	readme := filepath.Join(repoRoot, "README.md")
	if err := os.WriteFile(readme, []byte("# test\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	runTestCommand(t, repoRoot, "git", "add", "README.md")
	runTestCommand(t, repoRoot, "git", "commit", "-m", "initial")
	runTestCommand(t, repoRoot, "git", "remote", "add", "origin", remoteDir)
	runTestCommand(t, repoRoot, "git", "push", "-u", "origin", "main")

	return repoRoot, remoteDir
}

func hooksDirForRepo(t *testing.T, repoRoot string) string {
	t.Helper()

	cmd := exec.Command("git", "config", "--get", "core.hooksPath")
	cmd.Dir = repoRoot
	output, err := cmd.Output()
	hooksPath := strings.TrimSpace(string(output))
	if err != nil && hooksPath == "" {
		return filepath.Join(repoRoot, ".git", "hooks")
	}
	if hooksPath == "" {
		return filepath.Join(repoRoot, ".git", "hooks")
	}
	if filepath.IsAbs(hooksPath) {
		return hooksPath
	}
	return filepath.Join(repoRoot, hooksPath)
}
