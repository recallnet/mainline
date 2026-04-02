package app

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	registryDir, err := os.MkdirTemp("", "mainline-app-tests-")
	if err != nil {
		_, _ = os.Stderr.WriteString("create temp registry dir: " + err.Error() + "\n")
		os.Exit(1)
	}
	defer os.RemoveAll(registryDir)

	if err := os.Setenv("MAINLINE_REGISTRY_PATH", filepath.Join(registryDir, "registry.json")); err != nil {
		_, _ = os.Stderr.WriteString("set MAINLINE_REGISTRY_PATH: " + err.Error() + "\n")
		os.Exit(1)
	}

	os.Exit(m.Run())
}

func runTestCommand(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %v failed: %v: %s", name, args, err, stderr.String())
	}

	return stdout.String()
}

func configureTestGitRepo(t *testing.T, repoRoot string) {
	t.Helper()
	runTestCommand(t, repoRoot, "git", "config", "user.name", "Test User")
	runTestCommand(t, repoRoot, "git", "config", "user.email", "test@example.com")
	runTestCommand(t, repoRoot, "git", "config", "commit.gpgsign", "false")
	runTestCommand(t, repoRoot, "git", "config", "tag.gpgsign", "false")
}

func createBareCloneWorktree(t *testing.T) (string, string) {
	t.Helper()

	seedRoot := t.TempDir()
	runTestCommand(t, seedRoot, "git", "init", "-b", "main")
	configureTestGitRepo(t, seedRoot)
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
	configureTestGitRepo(t, worktreePath)

	return bareDir, worktreePath
}

func createTestRepoWithRemote(t *testing.T) (string, string) {
	t.Helper()

	remoteDir := filepath.Join(t.TempDir(), "origin.git")
	runTestCommand(t, t.TempDir(), "git", "init", "--bare", remoteDir)

	repoRoot := t.TempDir()
	runTestCommand(t, repoRoot, "git", "init", "-b", "main")
	configureTestGitRepo(t, repoRoot)
	runTestCommand(t, repoRoot, "git", "config", "core.hooksPath", ".git/hooks")

	readme := filepath.Join(repoRoot, "README.md")
	if err := os.WriteFile(readme, []byte("# test\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	runTestCommand(t, repoRoot, "git", "add", "README.md")
	runTestCommand(t, repoRoot, "git", "commit", "-m", "initial")
	runTestCommand(t, repoRoot, "git", "remote", "add", "origin", remoteDir)
	runTestCommand(t, repoRoot, "git", "push", "-u", "origin", "main")
	runTestCommand(t, remoteDir, "git", "symbolic-ref", "HEAD", "refs/heads/main")

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
