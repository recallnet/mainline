package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepoInitAndShow(t *testing.T) {
	repoRoot, worktreePath := createTestRepo(t)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot, "--main-worktree", worktreePath}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	configPath := filepath.Join(repoRoot, "mainline.toml")
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("expected config file at %s: %v", configPath, err)
	}

	var showOut bytes.Buffer
	var showErr bytes.Buffer
	if err := runRepoShow([]string{"--repo", repoRoot}, &showOut, &showErr); err != nil {
		t.Fatalf("runRepoShow returned error: %v", err)
	}

	output := showOut.String()
	if !strings.Contains(output, "Config present: true") {
		t.Fatalf("expected config present in output, got %q", output)
	}
	if !strings.Contains(output, "Protected branch: main") {
		t.Fatalf("expected protected branch in output, got %q", output)
	}
}

func TestDoctorDetectsDirtyProtectedBranch(t *testing.T) {
	repoRoot, worktreePath := createTestRepo(t)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot, "--main-worktree", worktreePath}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	dirtyFile := filepath.Join(worktreePath, "dirty.txt")
	if err := os.WriteFile(dirtyFile, []byte("dirty"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	var doctorOut bytes.Buffer
	var doctorErr bytes.Buffer
	if err := runDoctor([]string{"--repo", repoRoot}, &doctorOut, &doctorErr); err != nil {
		t.Fatalf("runDoctor returned error: %v", err)
	}

	output := doctorOut.String()
	if !strings.Contains(output, "Protected branch clean: no") {
		t.Fatalf("expected dirty protected branch report, got %q", output)
	}
	if !strings.Contains(output, "Main worktree exists: yes") {
		t.Fatalf("expected main worktree report, got %q", output)
	}
}

func TestDoctorDetectsMissingCanonicalWorktree(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	missingWorktree := filepath.Join(repoRoot, "missing-main-worktree")

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot, "--main-worktree", missingWorktree}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	var doctorOut bytes.Buffer
	var doctorErr bytes.Buffer
	if err := runDoctor([]string{"--repo", repoRoot}, &doctorOut, &doctorErr); err != nil {
		t.Fatalf("runDoctor returned error: %v", err)
	}

	output := doctorOut.String()
	if !strings.Contains(output, "Main worktree exists: no") {
		t.Fatalf("expected missing main worktree report, got %q", output)
	}
}

func TestRepoInitFromLinkedWorktreeWritesSharedConfig(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	linkedWorktree := filepath.Join(t.TempDir(), "feature-worktree")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature", linkedWorktree)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", linkedWorktree}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(repoRoot, "mainline.toml")); err != nil {
		t.Fatalf("expected shared config in main repo root: %v", err)
	}

	if _, err := os.Stat(filepath.Join(linkedWorktree, "mainline.toml")); !os.IsNotExist(err) {
		t.Fatalf("expected no separate config in linked worktree, got err=%v", err)
	}
}

func TestRepoInitFromBareCloneWorktreeUsesSharedStorage(t *testing.T) {
	bareDir, worktreePath := createBareCloneWorktree(t)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", worktreePath}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(bareDir, "mainline.toml")); err != nil {
		t.Fatalf("expected config in bare repo storage: %v", err)
	}

	if _, err := os.Stat(filepath.Join(bareDir, "mainline", "state.db")); err != nil {
		t.Fatalf("expected state db in bare repo storage: %v", err)
	}

	if strings.Contains(initOut.String(), filepath.Join(worktreePath, ".git")) {
		t.Fatalf("expected shared storage state path, got output %q", initOut.String())
	}
}

func createTestRepo(t *testing.T) (string, string) {
	t.Helper()

	root := t.TempDir()
	runTestCommand(t, root, "git", "init", "-b", "main")
	runTestCommand(t, root, "git", "config", "user.name", "Test User")
	runTestCommand(t, root, "git", "config", "user.email", "test@example.com")

	readme := filepath.Join(root, "README.md")
	if err := os.WriteFile(readme, []byte("# test\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	runTestCommand(t, root, "git", "add", "README.md")
	runTestCommand(t, root, "git", "commit", "-m", "initial")

	return root, root
}
