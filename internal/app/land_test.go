package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/state"
)

func TestLandJSONIntegratesAndPublishesBranch(t *testing.T) {
	repoRoot, remoteDir := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	featurePath := t.TempDir() + "/feature-land"
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/land", featurePath)
	writeFileAndCommit(t, featurePath, "land.txt", "land\n", "feature land")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLI([]string{"land", "--repo", featurePath, "--json", "--timeout", "30s", "--poll-interval", "10ms"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCLI land returned error: %v", err)
	}

	var result landResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal land result: %v", err)
	}
	if result.SubmissionStatus != "succeeded" {
		t.Fatalf("expected succeeded submission, got %+v", result)
	}
	if result.PublishStatus != "succeeded" || !result.Published {
		t.Fatalf("expected succeeded publish, got %+v", result)
	}

	localHead := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	remoteHead := trimNewline(runTestCommand(t, remoteDir, "git", "rev-parse", "refs/heads/main"))
	if remoteHead != localHead {
		t.Fatalf("expected remote head %q, got %q", localHead, remoteHead)
	}
	if result.ProtectedSHA != localHead {
		t.Fatalf("expected protected sha %q, got %q", localHead, result.ProtectedSHA)
	}
}

func TestLandTreatsLocalOnlyManualRepoAsLandedWithoutPublish(t *testing.T) {
	bareDir, mainWorktree := createBareCloneWorktree(t)
	configPath := filepath.Join(mainWorktree, "mainline.toml")
	committedConfig := `[repo]
ProtectedBranch = 'main'
MainWorktree = '` + canonicalRegistryPath(mainWorktree) + `'

[publish]
Mode = 'manual'
`
	if err := os.WriteFile(configPath, []byte(committedConfig), 0o644); err != nil {
		t.Fatalf("WriteFile(mainline.toml): %v", err)
	}
	runTestCommand(t, mainWorktree, "git", "add", "mainline.toml")
	runTestCommand(t, mainWorktree, "git", "commit", "-m", "Initialize local-only mainline policy")

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", mainWorktree, "--protected-branch", "main", "--main-worktree", mainWorktree}, newStepPrinter(&initOut), &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	featurePath := filepath.Join(t.TempDir(), "feature-local-only-land")
	runTestCommand(t, t.TempDir(), "git", "--git-dir", bareDir, "worktree", "add", "-b", "feature/local-only-land", featurePath, "main")
	configureTestGitRepo(t, featurePath)
	writeFileAndCommit(t, featurePath, "local-only-land.txt", "local only land\n", "feature local only land")
	featureSHA := trimNewline(runTestCommand(t, featurePath, "git", "rev-parse", "HEAD"))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLI([]string{"land", "--repo", featurePath, "--json", "--timeout", "30s", "--poll-interval", "10ms"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCLI land returned error: %v: %s", err, stdout.String())
	}

	var result landResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal land result: %v", err)
	}
	if result.SubmissionStatus != "succeeded" || !result.Published {
		t.Fatalf("expected local-only land success, got %+v", result)
	}
	if result.PublishRequestID != 0 || result.PublishStatus != "" {
		t.Fatalf("expected no publish request for local-only land, got %+v", result)
	}
	if result.ProtectedSHA != featureSHA {
		t.Fatalf("expected protected sha %q, got %q", featureSHA, result.ProtectedSHA)
	}
	localHead := trimNewline(runTestCommand(t, mainWorktree, "git", "rev-parse", "HEAD"))
	if localHead != featureSHA {
		t.Fatalf("expected protected worktree head %q, got %q", featureSHA, localHead)
	}
}

func TestLandReturnsBlockedSubmissionFailure(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	featureOne := t.TempDir() + "/feature-one"
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/one", featureOne)
	replaceFileAndCommit(t, featureOne, "README.md", "# alpha\n", "feature one")

	featureTwo := t.TempDir() + "/feature-two"
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/two", featureTwo)
	replaceFileAndCommit(t, featureTwo, "README.md", "# beta\n", "feature two")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCLI([]string{"land", "--repo", featureOne, "--timeout", "30s", "--poll-interval", "10ms"}, newStepPrinter(&stdout), &stderr); err != nil {
		t.Fatalf("runCLI first land returned error: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	err := runCLI([]string{"land", "--repo", featureTwo, "--timeout", "30s", "--poll-interval", "10ms"}, newStepPrinter(&stdout), &stderr)
	if err == nil {
		t.Fatalf("expected blocked land to fail")
	}
	if !strings.Contains(err.Error(), "submission") || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("expected blocked submission error, got %v", err)
	}
	if !strings.Contains(stdout.String(), "Submission status: blocked") {
		t.Fatalf("expected blocked status in output, got %q", stdout.String())
	}
}

func TestLandFailsPreflightBeforeQueueingWhenProtectedWorktreeIsDirty(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	featurePath := t.TempDir() + "/feature-preflight"
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/preflight", featurePath)
	writeFileAndCommit(t, featurePath, "preflight.txt", "preflight\n", "feature preflight")

	if err := os.WriteFile(filepath.Join(repoRoot, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("WriteFile dirty.txt: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runCLI([]string{"land", "--repo", featurePath, "--timeout", "30s", "--poll-interval", "10ms"}, newStepPrinter(&stdout), &stderr)
	if err == nil {
		t.Fatalf("expected dirty protected branch preflight failure")
	}
	if !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("expected dirty preflight error, got %v", err)
	}
	if !strings.Contains(err.Error(), "dirty.txt") {
		t.Fatalf("expected dirty file path in preflight error, got %v", err)
	}

	layout, discoverErr := git.DiscoverRepositoryLayout(repoRoot)
	if discoverErr != nil {
		t.Fatalf("DiscoverRepositoryLayout: %v", discoverErr)
	}
	store := state.NewStore(state.DefaultPath(layout.GitDir))
	repoRecord, getErr := store.GetRepositoryByPath(context.Background(), layout.RepositoryRoot)
	if getErr != nil {
		t.Fatalf("GetRepositoryByPath: %v", getErr)
	}
	submissions, listErr := store.ListIntegrationSubmissions(context.Background(), repoRecord.ID)
	if listErr != nil {
		t.Fatalf("ListIntegrationSubmissions: %v", listErr)
	}
	if len(submissions) != 0 {
		t.Fatalf("expected no queued submissions after preflight failure, got %+v", submissions)
	}
}

func TestLandDirtyMainlineConfigGuidesBootstrapCommit(t *testing.T) {
	bareDir, mainWorktree := createBareCloneWorktree(t)
	featurePath := filepath.Join(t.TempDir(), "feature-policy-dirty")
	runTestCommand(t, t.TempDir(), "git", "--git-dir", bareDir, "worktree", "add", "-b", "feature/policy-dirty", featurePath, "main")
	configureTestGitRepo(t, featurePath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", featurePath, "--protected-branch", "main", "--main-worktree", mainWorktree}, newStepPrinter(&initOut), &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	writeFileAndCommit(t, featurePath, "policy-dirty.txt", "policy dirty\n", "feature policy dirty")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runCLI([]string{"land", "--repo", featurePath, "--timeout", "30s", "--poll-interval", "10ms"}, newStepPrinter(&stdout), &stderr)
	if err == nil {
		t.Fatalf("expected dirty protected branch preflight failure")
	}
	message := err.Error()
	if !strings.Contains(message, "(mainline.toml)") {
		t.Fatalf("expected full mainline.toml dirty path, got %v", err)
	}
	if strings.Contains(message, "(ainline.toml)") {
		t.Fatalf("expected dirty path not to be truncated, got %v", err)
	}
	canonicalMainWorktree := canonicalRegistryPath(mainWorktree)
	if !strings.Contains(message, "git -C "+canonicalMainWorktree+" add mainline.toml") {
		t.Fatalf("expected bootstrap add guidance, got %v", err)
	}
	if !strings.Contains(message, "Initialize mainline repo policy") {
		t.Fatalf("expected bootstrap commit guidance, got %v", err)
	}
	if !strings.Contains(message, "commit or revert mainline.toml") {
		t.Fatalf("expected direct policy-file guidance, got %v", err)
	}
}

func TestLandFailsFastWhenConfiguredRemoteIsMissing(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	featurePath := filepath.Join(t.TempDir(), "feature-missing-remote")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/missing-remote", featurePath)
	writeFileAndCommit(t, featurePath, "missing-remote.txt", "missing remote\n", "feature missing remote")

	protectedBefore := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runCLI([]string{"land", "--repo", featurePath, "--json", "--timeout", "30s", "--poll-interval", "10ms"}, newStepPrinter(&stdout), &stderr)
	if err == nil {
		t.Fatalf("expected land to fail fast when configured remote is missing")
	}
	if !strings.Contains(err.Error(), "configured remote origin does not exist") {
		t.Fatalf("expected missing remote error, got %v", err)
	}
	if !strings.Contains(err.Error(), "publish cannot run") {
		t.Fatalf("expected publish-specific guidance, got %v", err)
	}

	protectedAfter := trimNewline(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))
	if protectedAfter != protectedBefore {
		t.Fatalf("expected land preflight not to integrate before failing, got %s then %s", protectedBefore, protectedAfter)
	}

	layout, discoverErr := git.DiscoverRepositoryLayout(repoRoot)
	if discoverErr != nil {
		t.Fatalf("DiscoverRepositoryLayout: %v", discoverErr)
	}
	store := state.NewStore(state.DefaultPath(layout.GitDir))
	repoRecord, getErr := store.GetRepositoryByPath(context.Background(), layout.RepositoryRoot)
	if getErr != nil {
		t.Fatalf("GetRepositoryByPath: %v", getErr)
	}
	submissions, listErr := store.ListIntegrationSubmissions(context.Background(), repoRecord.ID)
	if listErr != nil {
		t.Fatalf("ListIntegrationSubmissions: %v", listErr)
	}
	if len(submissions) != 0 {
		t.Fatalf("expected no queued submissions after missing remote preflight failure, got %+v", submissions)
	}
}

func TestLandFailsIfSucceededSubmissionIsNotReachableFromProtectedBranch(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	featurePath := filepath.Join(t.TempDir(), "feature-land-verify")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/land-verify", featurePath)
	writeFileAndCommit(t, featurePath, "verify.txt", "verify\n", "feature verify")

	queued, err := queueSubmission(submitOptions{repoPath: featurePath})
	if err != nil {
		t.Fatalf("queueSubmission: %v", err)
	}
	if _, err := queued.Store.UpdateIntegrationSubmissionStatus(context.Background(), queued.Submission.ID, "succeeded", ""); err != nil {
		t.Fatalf("UpdateIntegrationSubmissionStatus: %v", err)
	}

	result, err := waitForLandedPublish(queued, 100*time.Millisecond, time.Millisecond)
	if err == nil {
		t.Fatalf("expected verification failure")
	}
	if !strings.Contains(err.Error(), "has no integration.succeeded event with protected_sha") {
		t.Fatalf("expected reachability error, got %v", err)
	}
	if result.Error == "" || !strings.Contains(result.Error, "has no integration.succeeded event with protected_sha") {
		t.Fatalf("expected land result error, got %+v", result)
	}
}
