package app

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
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
