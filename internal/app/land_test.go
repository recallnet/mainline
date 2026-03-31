package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	if err := runCLI([]string{"land", "--repo", featurePath, "--json", "--timeout", "30s", "--poll-interval", "10ms"}, &stdout, &stderr); err != nil {
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
	if err := runCLI([]string{"land", "--repo", featureOne, "--timeout", "30s", "--poll-interval", "10ms"}, &stdout, &stderr); err != nil {
		t.Fatalf("runCLI first land returned error: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	err := runCLI([]string{"land", "--repo", featureTwo, "--timeout", "30s", "--poll-interval", "10ms"}, &stdout, &stderr)
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
	err := runCLI([]string{"land", "--repo", featurePath, "--timeout", "30s", "--poll-interval", "10ms"}, &stdout, &stderr)
	if err == nil {
		t.Fatalf("expected dirty protected branch preflight failure")
	}
	if !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("expected dirty preflight error, got %v", err)
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
