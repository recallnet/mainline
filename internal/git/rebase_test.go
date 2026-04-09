package git

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestRebaseCurrentBranchStopsWhenPatchAlreadyExistsUpstream(t *testing.T) {
	repoRoot := t.TempDir()
	runGitCommand(t, repoRoot, "git", "init", "-b", "main")
	runGitCommand(t, repoRoot, "git", "config", "user.name", "Test User")
	runGitCommand(t, repoRoot, "git", "config", "user.email", "test@example.com")
	runGitCommand(t, repoRoot, "git", "config", "commit.gpgsign", "false")
	runGitCommand(t, repoRoot, "git", "config", "tag.gpgsign", "false")
	runGitCommand(t, repoRoot, "git", "config", "core.hooksPath", ".git/hooks")

	writeFile(t, filepath.Join(repoRoot, "README.md"), "# test\n")
	runGitCommand(t, repoRoot, "git", "add", "README.md")
	runGitCommand(t, repoRoot, "git", "commit", "-m", "initial")

	runGitCommand(t, repoRoot, "git", "checkout", "-b", "feature/duplicate-patch")
	writeFile(t, filepath.Join(repoRoot, "README.md"), "# test\nqueued change\n")
	runGitCommand(t, repoRoot, "git", "commit", "-am", "feature patch")

	runGitCommand(t, repoRoot, "git", "checkout", "main")
	writeFile(t, filepath.Join(repoRoot, "README.md"), "# test\nqueued change\n")
	runGitCommand(t, repoRoot, "git", "commit", "-am", "upstream patch")

	runGitCommand(t, repoRoot, "git", "checkout", "feature/duplicate-patch")
	err := NewEngine(repoRoot).RebaseCurrentBranch(repoRoot, "main")
	if !errors.Is(err, ErrRebaseEmpty) {
		t.Fatalf("expected ErrRebaseEmpty for duplicate patch rebase, got %v", err)
	}

	status := runGitCommand(t, repoRoot, "git", "status")
	if !strings.Contains(status, "rebase in progress") {
		t.Fatalf("expected repository to remain in rebase state, got %q", status)
	}
}
