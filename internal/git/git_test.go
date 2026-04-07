package git

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestListWorktreesHandlesBareStorageRepos(t *testing.T) {
	seedRoot := t.TempDir()
	runGitCommand(t, seedRoot, "git", "init", "-b", "main")
	runGitCommand(t, seedRoot, "git", "config", "user.name", "Test User")
	runGitCommand(t, seedRoot, "git", "config", "user.email", "test@example.com")
	runGitCommand(t, seedRoot, "git", "config", "core.hooksPath", ".git/hooks")
	writeFile(t, filepath.Join(seedRoot, "README.md"), "# test\n")
	runGitCommand(t, seedRoot, "git", "add", "README.md")
	runGitCommand(t, seedRoot, "git", "commit", "-m", "initial")

	bareDir := filepath.Join(t.TempDir(), "repo.git")
	runGitCommand(t, t.TempDir(), "git", "clone", "--bare", seedRoot, bareDir)

	mainWorktree := filepath.Join(t.TempDir(), "main-worktree")
	runGitCommand(t, seedRoot, "git", "--git-dir", bareDir, "worktree", "add", mainWorktree, "main")
	extraWorktree := filepath.Join(t.TempDir(), "feature-worktree")
	runGitCommand(t, mainWorktree, "git", "worktree", "add", "-b", "feature/test", extraWorktree)

	worktrees, err := NewEngine(mainWorktree).ListWorktrees()
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	if len(worktrees) < 3 {
		t.Fatalf("expected bare root, main worktree, and feature worktree, got %+v", worktrees)
	}

	seen := map[string]Worktree{}
	for _, wt := range worktrees {
		seen[wt.Path] = wt
	}
	mainPath := normalizePath(mainWorktree)
	extraPath := normalizePath(extraWorktree)
	barePath := normalizePath(bareDir)
	if _, ok := seen[barePath]; !ok {
		t.Fatalf("expected bare repository entry %q in %+v", barePath, worktrees)
	}
	if wt, ok := seen[mainPath]; !ok || wt.Branch != "main" {
		t.Fatalf("expected main worktree entry for %q, got %+v", mainPath, seen[mainPath])
	}
	if wt, ok := seen[extraPath]; !ok || wt.Branch != "feature/test" {
		t.Fatalf("expected feature worktree entry for %q, got %+v", extraPath, seen[extraPath])
	}
}

func TestParseWorktreeListPorcelainHandlesBareAndLinkedEntries(t *testing.T) {
	output := strings.TrimSpace(`
worktree /repo.git
bare

worktree /repo-main
HEAD 1111111111111111111111111111111111111111
branch refs/heads/main

worktree /repo-feature
HEAD 2222222222222222222222222222222222222222
detached
`) + "\n"
	worktrees := parseWorktreeListPorcelain(output, "/repo-main")
	if len(worktrees) != 3 {
		t.Fatalf("expected 3 worktrees, got %+v", worktrees)
	}
	if !worktrees[0].IsBare || worktrees[0].Path != "/repo.git" {
		t.Fatalf("expected bare root entry first, got %+v", worktrees[0])
	}
	if worktrees[1].Branch != "main" || !worktrees[1].IsCurrent {
		t.Fatalf("expected current main entry, got %+v", worktrees[1])
	}
	if !worktrees[2].IsDetached || worktrees[2].HeadSHA == "" {
		t.Fatalf("expected detached feature entry, got %+v", worktrees[2])
	}
}

func TestWorktreeIsCleanHonorsGitIgnore(t *testing.T) {
	repoRoot := t.TempDir()
	runGitCommand(t, repoRoot, "git", "init", "-b", "main")
	runGitCommand(t, repoRoot, "git", "config", "user.name", "Test User")
	runGitCommand(t, repoRoot, "git", "config", "user.email", "test@example.com")
	runGitCommand(t, repoRoot, "git", "config", "core.hooksPath", ".git/hooks")

	writeFile(t, filepath.Join(repoRoot, ".gitignore"), ".agents/skills/\n")
	writeFile(t, filepath.Join(repoRoot, "README.md"), "# test\n")
	runGitCommand(t, repoRoot, "git", "add", ".gitignore", "README.md")
	runGitCommand(t, repoRoot, "git", "commit", "-m", "initial")

	if err := os.MkdirAll(filepath.Join(repoRoot, ".agents", "skills", "onboarding"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeFile(t, filepath.Join(repoRoot, ".agents", "skills", "onboarding", "SKILL.md"), "generated\n")

	clean, err := NewEngine(repoRoot).WorktreeIsClean(repoRoot)
	if err != nil {
		t.Fatalf("WorktreeIsClean: %v", err)
	}
	if !clean {
		t.Fatalf("expected ignored generated skills to keep worktree clean")
	}

	paths, err := NewEngine(repoRoot).WorktreeDirtyPaths(repoRoot)
	if err != nil {
		t.Fatalf("WorktreeDirtyPaths: %v", err)
	}
	if len(paths) != 0 {
		t.Fatalf("expected no dirty paths for ignored generated files, got %+v", paths)
	}
}

func TestWorktreeIsCleanStillHonorsAstrochickenOverride(t *testing.T) {
	repoRoot := t.TempDir()
	runGitCommand(t, repoRoot, "git", "init", "-b", "main")
	runGitCommand(t, repoRoot, "git", "config", "user.name", "Test User")
	runGitCommand(t, repoRoot, "git", "config", "user.email", "test@example.com")
	runGitCommand(t, repoRoot, "git", "config", "core.hooksPath", ".git/hooks")

	writeFile(t, filepath.Join(repoRoot, "README.md"), "# test\n")
	runGitCommand(t, repoRoot, "git", "add", "README.md")
	runGitCommand(t, repoRoot, "git", "commit", "-m", "initial")

	if err := os.MkdirAll(filepath.Join(repoRoot, ".astrochicken", "policies"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeFile(t, filepath.Join(repoRoot, ".astrochicken", "README.md"), "local\n")
	writeFile(t, filepath.Join(repoRoot, ".astrochicken", "policies", "ingress-policy.json"), "{}\n")

	clean, err := NewEngine(repoRoot).WorktreeIsClean(repoRoot)
	if err != nil {
		t.Fatalf("WorktreeIsClean: %v", err)
	}
	if !clean {
		t.Fatalf("expected .astrochicken authoring surface to remain ignorable")
	}
}

func TestPushBranchWithNoVerifyBypassesPrePushHook(t *testing.T) {
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

	hookMarker := filepath.Join(t.TempDir(), "pre-push-ran")
	hookPath := filepath.Join(repoRoot, ".git", "hooks", "pre-push")
	writeFile(t, hookPath, "#!/bin/sh\necho hook > "+hookMarker+"\nexit 9\n")
	if err := os.Chmod(hookPath, 0o755); err != nil {
		t.Fatalf("Chmod(pre-push): %v", err)
	}

	writeFile(t, filepath.Join(repoRoot, "second.txt"), "second\n")
	runGitCommand(t, repoRoot, "git", "add", "second.txt")
	runGitCommand(t, repoRoot, "git", "commit", "-m", "second")
	localHead := strings.TrimSpace(runGitCommand(t, repoRoot, "git", "rev-parse", "HEAD"))

	if err := NewEngine(repoRoot).PushBranch(repoRoot, "origin", "main", true); err != nil {
		t.Fatalf("PushBranch: %v", err)
	}

	if _, err := os.Stat(hookMarker); !os.IsNotExist(err) {
		t.Fatalf("expected pre-push hook to be bypassed, got %v", err)
	}
	remoteHead := strings.TrimSpace(runGitCommand(t, remoteDir, "git", "rev-parse", "refs/heads/main"))
	if remoteHead != localHead {
		t.Fatalf("expected remote head %q, got %q", localHead, remoteHead)
	}
}

func TestRebaseCurrentBranchStopsWhenQueuedCommitBecomesEmpty(t *testing.T) {
	repoRoot := t.TempDir()
	runGitCommand(t, repoRoot, "git", "init", "-b", "main")
	runGitCommand(t, repoRoot, "git", "config", "user.name", "Test User")
	runGitCommand(t, repoRoot, "git", "config", "user.email", "test@example.com")
	runGitCommand(t, repoRoot, "git", "config", "core.hooksPath", ".git/hooks")

	writeFile(t, filepath.Join(repoRoot, "README.md"), "# test\n")
	runGitCommand(t, repoRoot, "git", "add", "README.md")
	runGitCommand(t, repoRoot, "git", "commit", "-m", "initial")

	featureOne := filepath.Join(t.TempDir(), "feature-one")
	runGitCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/one", featureOne)
	writeFile(t, filepath.Join(featureOne, "README.md"), "# shared\n")
	runGitCommand(t, featureOne, "git", "add", "README.md")
	runGitCommand(t, featureOne, "git", "commit", "-m", "feature one")

	featureTwo := filepath.Join(t.TempDir(), "feature-two")
	runGitCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/two", featureTwo)
	writeFile(t, filepath.Join(featureTwo, "README.md"), "# shared\n")
	runGitCommand(t, featureTwo, "git", "add", "README.md")
	runGitCommand(t, featureTwo, "git", "commit", "-m", "feature two")

	runGitCommand(t, repoRoot, "git", "merge", "--ff-only", "feature/one")

	engine := NewEngine(repoRoot)
	err := engine.RebaseCurrentBranch(featureTwo, "main")
	if !errors.Is(err, ErrRebaseEmpty) {
		t.Fatalf("expected ErrRebaseEmpty, got %v", err)
	}

	operation, opErr := engine.InProgressOperation(featureTwo)
	if opErr != nil {
		t.Fatalf("InProgressOperation: %v", opErr)
	}
	if operation != "rebase" {
		t.Fatalf("expected rebase in progress, got %q", operation)
	}
}

func runGitCommand(t *testing.T, dir string, name string, args ...string) string {
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

func writeFile(t *testing.T, path string, contents string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}
