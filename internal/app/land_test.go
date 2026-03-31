package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
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
