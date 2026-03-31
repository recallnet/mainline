package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/recallnet/mainline/internal/policy"
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

func TestRepoInitSupportsJSONOutput(t *testing.T) {
	repoRoot, worktreePath := createTestRepo(t)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot, "--main-worktree", worktreePath, "--json"}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(initOut.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result["ok"] != true {
		t.Fatalf("expected ok=true, got %#v", result)
	}
	if result["protected_branch"] != "main" {
		t.Fatalf("expected protected branch main, got %#v", result["protected_branch"])
	}
	if result["main_worktree"] != worktreePath {
		t.Fatalf("expected main worktree %q, got %#v", worktreePath, result["main_worktree"])
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

func TestDoctorSucceedsFromBareCloneWorktree(t *testing.T) {
	bareDir, worktreePath := createBareCloneWorktree(t)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", worktreePath}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	var doctorOut bytes.Buffer
	var doctorErr bytes.Buffer
	if err := runDoctor([]string{"--repo", worktreePath, "--json"}, &doctorOut, &doctorErr); err != nil {
		t.Fatalf("runDoctor returned error: %v", err)
	}

	var report map[string]any
	if err := json.Unmarshal(doctorOut.Bytes(), &report); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	wantBareDir, err := filepath.EvalSymlinks(bareDir)
	if err != nil {
		t.Fatalf("EvalSymlinks(bareDir): %v", err)
	}
	wantWorktreePath, err := filepath.EvalSymlinks(worktreePath)
	if err != nil {
		t.Fatalf("EvalSymlinks(worktreePath): %v", err)
	}

	if got := report["repository_root"]; got != wantBareDir {
		t.Fatalf("expected repository root %q, got %#v", wantBareDir, got)
	}

	if got := report["main_worktree_path"]; got != wantWorktreePath {
		t.Fatalf("expected main worktree path %q, got %#v", wantWorktreePath, got)
	}
}

func TestDoctorDoesNotCreateStateDBBeforeInit(t *testing.T) {
	repoRoot, _ := createTestRepo(t)

	var doctorOut bytes.Buffer
	var doctorErr bytes.Buffer
	if err := runDoctor([]string{"--repo", repoRoot}, &doctorOut, &doctorErr); err != nil {
		t.Fatalf("runDoctor returned error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(repoRoot, ".git", "mainline", "state.db")); !os.IsNotExist(err) {
		t.Fatalf("expected no state db before init, got err=%v", err)
	}
}

func TestDoctorWarnsWhenWorktreeOutsidePolicyPrefix(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	featurePath := filepath.Join(t.TempDir(), "outside-prefix")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/outside", featurePath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	cfg, _, err := policy.LoadOrDefault(repoRoot)
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	cfg.Repo.WorktreeLayoutPolicy = "enforce-prefix"
	cfg.Repo.WorktreeRootPrefix = filepath.Join(repoRoot, "_wt")
	if err := policy.SaveFile(repoRoot, cfg); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}

	var doctorOut bytes.Buffer
	var doctorErr bytes.Buffer
	if err := runDoctor([]string{"--repo", repoRoot}, &doctorOut, &doctorErr); err != nil {
		t.Fatalf("runDoctor returned error: %v", err)
	}

	output := doctorOut.String()
	if !strings.Contains(output, "Warnings: 1") {
		t.Fatalf("expected warning count, got %q", output)
	}
	if !strings.Contains(output, "worktree outside policy prefix") {
		t.Fatalf("expected policy warning, got %q", output)
	}
}

func TestDoctorAcceptsSymlinkedPolicyPrefix(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	actualPrefix := filepath.Join(t.TempDir(), "actual-prefix")
	if err := os.MkdirAll(actualPrefix, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	symlinkRoot := filepath.Join(t.TempDir(), "prefix-link")
	if err := os.Symlink(actualPrefix, symlinkRoot); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	featurePath := filepath.Join(actualPrefix, "feature-inside")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/inside", featurePath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	cfg, _, err := policy.LoadOrDefault(repoRoot)
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	cfg.Repo.WorktreeLayoutPolicy = "enforce-prefix"
	cfg.Repo.WorktreeRootPrefix = symlinkRoot
	if err := policy.SaveFile(repoRoot, cfg); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}

	var doctorOut bytes.Buffer
	var doctorErr bytes.Buffer
	if err := runDoctor([]string{"--repo", repoRoot}, &doctorOut, &doctorErr); err != nil {
		t.Fatalf("runDoctor returned error: %v", err)
	}

	output := doctorOut.String()
	if !strings.Contains(output, "Warnings: 0") {
		t.Fatalf("expected no warnings, got %q", output)
	}
}

func TestConfigEditScaffoldsMissingConfigAndInvokesEditor(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	editorPath := filepath.Join(t.TempDir(), "editor.sh")
	editorOutput := []byte(`#!/bin/sh
perl -0pi -e "s/ProtectedBranch = 'main'/ProtectedBranch = 'stable'/" "$1"
`)
	if err := os.WriteFile(editorPath, editorOutput, 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runConfigEdit([]string{"--repo", repoRoot, "--editor", editorPath, "--print-path"}, &stdout, &stderr); err != nil {
		t.Fatalf("runConfigEdit returned error: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, filepath.Join(repoRoot, "mainline.toml")) {
		t.Fatalf("expected config path in output, got %q", output)
	}
	if !strings.Contains(output, "Edited ") {
		t.Fatalf("expected edited message, got %q", output)
	}

	cfg, err := policy.LoadFile(repoRoot)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if cfg.Repo.ProtectedBranch != "stable" {
		t.Fatalf("expected edited protected branch, got %+v", cfg.Repo)
	}
}

func TestConfigEditUsesSharedRepoRootFromLinkedWorktree(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	linkedWorktree := filepath.Join(t.TempDir(), "feature-worktree")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/config", linkedWorktree)

	editorPath := filepath.Join(t.TempDir(), "editor-touch.sh")
	editorOutput := []byte(`#!/bin/sh
echo "# edited" >> "$1"
`)
	if err := os.WriteFile(editorPath, editorOutput, 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runConfigEdit([]string{"--repo", linkedWorktree, "--editor", editorPath}, &stdout, &stderr); err != nil {
		t.Fatalf("runConfigEdit returned error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(repoRoot, "mainline.toml")); err != nil {
		t.Fatalf("expected shared config in repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(linkedWorktree, "mainline.toml")); !os.IsNotExist(err) {
		t.Fatalf("expected no per-worktree config, got err=%v", err)
	}
}

func TestConfigEditRequiresEditor(t *testing.T) {
	repoRoot, _ := createTestRepo(t)

	originalVisual := os.Getenv("VISUAL")
	originalEditor := os.Getenv("EDITOR")
	t.Cleanup(func() {
		_ = os.Setenv("VISUAL", originalVisual)
		_ = os.Setenv("EDITOR", originalEditor)
	})
	_ = os.Unsetenv("VISUAL")
	_ = os.Unsetenv("EDITOR")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runConfigEdit([]string{"--repo", repoRoot}, &stdout, &stderr)
	if err == nil {
		t.Fatalf("expected missing editor error")
	}
	if !strings.Contains(err.Error(), "no editor configured") {
		t.Fatalf("expected missing editor message, got %v", err)
	}
}

func TestConfigEditSupportsEditorCommandsWithArgs(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	editorPath := filepath.Join(t.TempDir(), "editor-args.sh")
	editorOutput := []byte(`#!/bin/sh
test "$1" = "--wait" || exit 11
echo "# args-ok" >> "$2"
`)
	if err := os.WriteFile(editorPath, editorOutput, 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runConfigEdit([]string{"--repo", repoRoot, "--editor", editorPath + " --wait"}, &stdout, &stderr); err != nil {
		t.Fatalf("runConfigEdit returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(repoRoot, "mainline.toml"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "# args-ok") {
		t.Fatalf("expected editor with args to mutate config, got %q", string(data))
	}
}

func TestConfigEditJSONKeepsEditorStdoutOffMachineOutput(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	editorPath := filepath.Join(t.TempDir(), "editor-json.sh")
	editorOutput := []byte(`#!/bin/sh
echo "editor-noise"
echo "# json-ok" >> "$1"
`)
	if err := os.WriteFile(editorPath, editorOutput, 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runConfigEdit([]string{"--repo", repoRoot, "--editor", editorPath, "--json"}, &stdout, &stderr); err != nil {
		t.Fatalf("runConfigEdit returned error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v\nstdout=%q", err, stdout.String())
	}
	if result["ok"] != true {
		t.Fatalf("expected ok=true, got %#v", result)
	}
	if !strings.Contains(stderr.String(), "editor-noise") {
		t.Fatalf("expected editor stdout to be redirected to stderr, got stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func createTestRepo(t *testing.T) (string, string) {
	t.Helper()

	root := t.TempDir()
	runTestCommand(t, root, "git", "init", "-b", "main")
	runTestCommand(t, root, "git", "config", "user.name", "Test User")
	runTestCommand(t, root, "git", "config", "user.email", "test@example.com")
	runTestCommand(t, root, "git", "config", "core.hooksPath", ".git/hooks")

	readme := filepath.Join(root, "README.md")
	if err := os.WriteFile(readme, []byte("# test\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	runTestCommand(t, root, "git", "add", "README.md")
	runTestCommand(t, root, "git", "commit", "-m", "initial")

	return root, root
}
