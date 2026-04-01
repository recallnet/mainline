package git

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBranchStatusReportsExactAheadBehindCounts(t *testing.T) {
	remoteDir := filepath.Join(t.TempDir(), "origin.git")
	runGitCommand(t, t.TempDir(), "git", "init", "--bare", remoteDir)

	repoRoot := t.TempDir()
	runGitCommand(t, repoRoot, "git", "init", "-b", "main")
	runGitCommand(t, repoRoot, "git", "config", "user.name", "Test User")
	runGitCommand(t, repoRoot, "git", "config", "user.email", "test@example.com")
	runGitCommand(t, repoRoot, "git", "config", "core.hooksPath", ".git/hooks")

	writeFile(t, filepath.Join(repoRoot, "README.md"), "# test\n")
	runGitCommand(t, repoRoot, "git", "add", "README.md")
	runGitCommand(t, repoRoot, "git", "commit", "-m", "initial")
	runGitCommand(t, repoRoot, "git", "remote", "add", "origin", remoteDir)
	runGitCommand(t, repoRoot, "git", "push", "-u", "origin", "main")
	runGitCommand(t, remoteDir, "git", "symbolic-ref", "HEAD", "refs/heads/main")

	peerClone := filepath.Join(t.TempDir(), "peer")
	runGitCommand(t, t.TempDir(), "git", "clone", remoteDir, peerClone)
	runGitCommand(t, peerClone, "git", "config", "user.name", "Peer User")
	runGitCommand(t, peerClone, "git", "config", "user.email", "peer@example.com")
	runGitCommand(t, peerClone, "git", "config", "core.hooksPath", ".git/hooks")
	writeFile(t, filepath.Join(peerClone, "peer.txt"), "peer\n")
	runGitCommand(t, peerClone, "git", "add", "peer.txt")
	runGitCommand(t, peerClone, "git", "commit", "-m", "peer change")
	runGitCommand(t, peerClone, "git", "push", "origin", "main")

	writeFile(t, filepath.Join(repoRoot, "local-one.txt"), "one\n")
	runGitCommand(t, repoRoot, "git", "add", "local-one.txt")
	runGitCommand(t, repoRoot, "git", "commit", "-m", "local one")
	writeFile(t, filepath.Join(repoRoot, "local-two.txt"), "two\n")
	runGitCommand(t, repoRoot, "git", "add", "local-two.txt")
	runGitCommand(t, repoRoot, "git", "commit", "-m", "local two")
	runGitCommand(t, repoRoot, "git", "fetch", "origin")

	status, err := NewEngine(repoRoot).BranchStatus("main", "main")
	if err != nil {
		t.Fatalf("BranchStatus: %v", err)
	}
	if status.AheadCount != 2 || status.BehindCount != 1 {
		t.Fatalf("expected ahead=2 behind=1, got %+v", status)
	}
}

func runGitCommand(t *testing.T, dir string, name string, args ...string) string {
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

func writeFile(t *testing.T, path string, contents string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}
