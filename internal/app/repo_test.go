package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/policy"
	"github.com/recallnet/mainline/internal/state"
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
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(configPath): %v", err)
	}
	if !strings.Contains(string(data), "MaxQueueDepth = 0") {
		t.Fatalf("expected default MaxQueueDepth scaffold in config, got %q", string(data))
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
	if !strings.Contains(initOut.String(), "Initialize mainline repo policy") {
		t.Fatalf("expected init output to include recommended commit, got %q", initOut.String())
	}
}

func TestRepoShowRepairsMissingRepositoryRecord(t *testing.T) {
	repoRoot, worktreePath := createTestRepo(t)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot, "--main-worktree", worktreePath}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	layout, err := git.DiscoverRepositoryLayout(repoRoot)
	if err != nil {
		t.Fatalf("DiscoverRepositoryLayout: %v", err)
	}
	statePath := state.DefaultPath(layout.GitDir)
	db, err := sql.Open("sqlite", statePath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`DELETE FROM repositories`); err != nil {
		t.Fatalf("DELETE repositories: %v", err)
	}

	var showOut bytes.Buffer
	var showErr bytes.Buffer
	if err := runRepoShow([]string{"--repo", repoRoot}, &showOut, &showErr); err != nil {
		t.Fatalf("runRepoShow returned error: %v", err)
	}

	output := showOut.String()
	if !strings.Contains(output, "State path: "+statePath) {
		t.Fatalf("expected state path after record repair, got %q", output)
	}

	store := state.NewStore(statePath)
	record, err := store.GetRepositoryByPath(context.Background(), layout.RepositoryRoot)
	if err != nil {
		t.Fatalf("GetRepositoryByPath after repair: %v", err)
	}
	if record.CanonicalPath != layout.RepositoryRoot {
		t.Fatalf("expected repaired canonical path %q, got %+v", layout.RepositoryRoot, record)
	}
}

func TestRepoRootReportsTrustworthyCanonicalCheckout(t *testing.T) {
	repoRoot, worktreePath := createTestRepo(t)
	canonicalRoot := canonicalRegistryPath(repoRoot)
	canonicalWorktree := canonicalRegistryPath(worktreePath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot, "--main-worktree", worktreePath}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}
	runTestCommand(t, repoRoot, "git", "add", "mainline.toml")
	runTestCommand(t, repoRoot, "git", "commit", "-m", "init mainline")

	var rootOut bytes.Buffer
	var rootErr bytes.Buffer
	if err := runRepoRoot([]string{"--repo", repoRoot, "--json"}, &rootOut, &rootErr); err != nil {
		t.Fatalf("runRepoRoot returned error: %v", err)
	}

	var result repoRootResult
	if err := json.Unmarshal(rootOut.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !result.Trustworthy {
		t.Fatalf("expected trustworthy root result, got %+v", result)
	}
	if result.RootCheckout.Path != canonicalRoot || !result.RootCheckout.IsCanonical || !result.RootCheckout.Clean {
		t.Fatalf("unexpected root checkout info: %+v", result.RootCheckout)
	}
	if result.MainWorktree != canonicalWorktree {
		t.Fatalf("expected canonical main worktree %q, got %+v", canonicalWorktree, result)
	}
}

func TestRepoRootAdoptsCleanRootAsCanonicalMainWorktree(t *testing.T) {
	repoRoot, worktreePath := createTestRepo(t)
	canonicalWorktree := canonicalRegistryPath(worktreePath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	nonCanonical := filepath.Join(repoRoot, "shadow-main")
	if err := runRepoInit([]string{"--repo", repoRoot, "--main-worktree", nonCanonical}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}
	runTestCommand(t, repoRoot, "git", "add", "mainline.toml")
	runTestCommand(t, repoRoot, "git", "commit", "-m", "init mainline")

	var rootOut bytes.Buffer
	var rootErr bytes.Buffer
	if err := runRepoRoot([]string{"--repo", repoRoot, "--adopt-root", "--json"}, &rootOut, &rootErr); err != nil {
		t.Fatalf("runRepoRoot returned error: %v", err)
	}

	cfg, _, err := policy.LoadOrDefault(repoRoot)
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	if cfg.Repo.MainWorktree != canonicalWorktree {
		t.Fatalf("expected main worktree to be adopted to root %q, got %q", canonicalWorktree, cfg.Repo.MainWorktree)
	}

	layout, err := git.DiscoverRepositoryLayout(repoRoot)
	if err != nil {
		t.Fatalf("DiscoverRepositoryLayout: %v", err)
	}
	store := state.NewStore(state.DefaultPath(layout.GitDir))
	record, err := store.GetRepositoryByPath(context.Background(), layout.RepositoryRoot)
	if err != nil {
		t.Fatalf("GetRepositoryByPath: %v", err)
	}
	if record.MainWorktree != canonicalWorktree {
		t.Fatalf("expected repo record main worktree %q, got %+v", canonicalWorktree, record)
	}
}

func TestRepoRootRejectsAdoptWhenRootCheckoutIsDirty(t *testing.T) {
	repoRoot, _ := createTestRepo(t)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot, "--main-worktree", filepath.Join(repoRoot, "shadow-main")}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}
	runTestCommand(t, repoRoot, "git", "add", "mainline.toml")
	runTestCommand(t, repoRoot, "git", "commit", "-m", "init mainline")

	if err := os.WriteFile(filepath.Join(repoRoot, "DIRTY.txt"), []byte("dirty"), 0o644); err != nil {
		t.Fatalf("WriteFile(DIRTY.txt): %v", err)
	}

	var rootOut bytes.Buffer
	var rootErr bytes.Buffer
	err := runRepoRoot([]string{"--repo", repoRoot, "--adopt-root", "--json"}, &rootOut, &rootErr)
	if err == nil {
		t.Fatalf("expected adopt-root to fail when root checkout is dirty")
	}
	if !strings.Contains(err.Error(), "complete the recommended actions first") {
		t.Fatalf("expected root adoption guidance, got %v", err)
	}
}

func TestRepoRootAdoptRequiresInitializedRepo(t *testing.T) {
	repoRoot, _ := createTestRepo(t)

	var rootOut bytes.Buffer
	var rootErr bytes.Buffer
	err := runRepoRoot([]string{"--repo", repoRoot, "--adopt-root", "--json"}, &rootOut, &rootErr)
	if err == nil {
		t.Fatalf("expected adopt-root to require repo init first")
	}
	if !strings.Contains(err.Error(), "run `mq repo init") {
		t.Fatalf("expected repo init guidance, got %v", err)
	}
}

func TestRepoInitSupportsJSONOutput(t *testing.T) {
	repoRoot, worktreePath := createTestRepo(t)
	registryPath := filepath.Join(t.TempDir(), "registry.json")
	t.Setenv("MAINLINE_REGISTRY_PATH", registryPath)

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
	if result["main_worktree"] != canonicalRegistryPath(worktreePath) {
		t.Fatalf("expected main worktree %q, got %#v", canonicalRegistryPath(worktreePath), result["main_worktree"])
	}
	if result["recommended_commit_message"] != "Initialize mainline repo policy" {
		t.Fatalf("expected recommended commit message, got %#v", result["recommended_commit_message"])
	}
	if result["global_registry_path"] != registryPath {
		t.Fatalf("expected registry path %q, got %#v", registryPath, result["global_registry_path"])
	}
	nextSteps, ok := result["next_steps"].([]any)
	if !ok || len(nextSteps) == 0 {
		t.Fatalf("expected next steps in json output, got %#v", result["next_steps"])
	}
}

func TestRepoInitRegistersRepoForGlobalDaemon(t *testing.T) {
	registryPath := filepath.Join(t.TempDir(), "registry.json")
	t.Setenv("MAINLINE_REGISTRY_PATH", registryPath)

	repoRoot, worktreePath := createTestRepo(t)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot, "--main-worktree", worktreePath}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	repos, err := loadRegisteredReposFromPath(registryPath)
	if err != nil {
		t.Fatalf("loadRegisteredReposFromPath: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("expected one registered repo, got %+v", repos)
	}
	if repos[0].RepositoryRoot != canonicalRegistryPath(repoRoot) || repos[0].MainWorktree != canonicalRegistryPath(worktreePath) {
		t.Fatalf("unexpected registered repo entry: %+v", repos[0])
	}
}

func TestRepoInitRepairsMalformedGlobalRegistryOnWrite(t *testing.T) {
	registryPath := filepath.Join(t.TempDir(), "registry.json")
	t.Setenv("MAINLINE_REGISTRY_PATH", registryPath)
	if err := os.WriteFile(registryPath, []byte("{\n  \"version\": \"v1\",\n  \"repositories\": []\n}\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(registryPath): %v", err)
	}

	repoRoot, worktreePath := createTestRepo(t)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot, "--main-worktree", worktreePath}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	repos, err := loadRegisteredReposFromPath(registryPath)
	if err != nil {
		t.Fatalf("loadRegisteredReposFromPath: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("expected one registered repo after registry repair, got %+v", repos)
	}
}

func TestLoadRegisteredReposPrunesMissingEntries(t *testing.T) {
	registryPath := filepath.Join(t.TempDir(), "registry.json")
	repoRoot, worktreePath := createTestRepo(t)
	layout, err := git.DiscoverRepositoryLayout(repoRoot)
	if err != nil {
		t.Fatalf("DiscoverRepositoryLayout: %v", err)
	}
	missingRoot := filepath.Join(t.TempDir(), "missing-repo")
	missingWorktree := filepath.Join(t.TempDir(), "missing-worktree")
	missingState := filepath.Join(t.TempDir(), "missing.db")
	if err := os.WriteFile(registryPath, []byte(fmt.Sprintf("{\n  \"version\": \"v1\",\n  \"repositories\": [\n    {\n      \"repository_root\": %q,\n      \"main_worktree\": %q,\n      \"state_path\": %q,\n      \"updated_at\": \"2026-04-01T00:00:00Z\"\n    },\n    {\n      \"repository_root\": %q,\n      \"main_worktree\": %q,\n      \"state_path\": %q,\n      \"updated_at\": \"2026-04-01T00:00:00Z\"\n    }\n  ]\n}\n",
		canonicalRegistryPath(missingRoot), canonicalRegistryPath(missingWorktree), canonicalRegistryPath(missingState),
		canonicalRegistryPath(repoRoot), canonicalRegistryPath(worktreePath), canonicalRegistryPath(state.DefaultPath(layout.GitDir)),
	)), 0o644); err != nil {
		t.Fatalf("WriteFile(registryPath): %v", err)
	}

	repos, err := loadRegisteredReposFromPath(registryPath)
	if err != nil {
		t.Fatalf("loadRegisteredReposFromPath: %v", err)
	}
	if len(repos) != 1 || repos[0].RepositoryRoot != canonicalRegistryPath(repoRoot) {
		t.Fatalf("expected stale entry pruned, got %+v", repos)
	}
}

func TestRegistryPruneCommandRemovesMissingEntries(t *testing.T) {
	registryPath := filepath.Join(t.TempDir(), "registry.json")
	missingRoot := filepath.Join(t.TempDir(), "missing-repo")
	if err := os.WriteFile(registryPath, []byte(fmt.Sprintf("{\n  \"version\": \"v1\",\n  \"repositories\": [\n    {\n      \"repository_root\": %q,\n      \"main_worktree\": %q,\n      \"state_path\": %q,\n      \"updated_at\": \"2026-04-01T00:00:00Z\"\n    }\n  ]\n}\n",
		canonicalRegistryPath(missingRoot), canonicalRegistryPath(filepath.Join(t.TempDir(), "missing-worktree")), canonicalRegistryPath(filepath.Join(t.TempDir(), "missing.db")))), 0o644); err != nil {
		t.Fatalf("WriteFile(registryPath): %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runRegistryPrune([]string{"--registry", registryPath, "--json"}, &stdout, &stderr); err != nil {
		t.Fatalf("runRegistryPrune: %v", err)
	}
	var result registryPruneResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result.PrunedCount != 1 || result.RemainingCount != 0 {
		t.Fatalf("expected one pruned entry, got %+v", result)
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

func TestDoctorRepairsMissingRepositoryRecord(t *testing.T) {
	repoRoot, worktreePath := createTestRepo(t)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot, "--main-worktree", worktreePath}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	layout, err := git.DiscoverRepositoryLayout(repoRoot)
	if err != nil {
		t.Fatalf("DiscoverRepositoryLayout: %v", err)
	}
	statePath := state.DefaultPath(layout.GitDir)
	db, err := sql.Open("sqlite", statePath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`DELETE FROM repositories`); err != nil {
		t.Fatalf("DELETE repositories: %v", err)
	}

	var doctorOut bytes.Buffer
	var doctorErr bytes.Buffer
	if err := runDoctor([]string{"--repo", repoRoot}, &doctorOut, &doctorErr); err != nil {
		t.Fatalf("runDoctor returned error: %v", err)
	}

	store := state.NewStore(statePath)
	record, err := store.GetRepositoryByPath(context.Background(), layout.RepositoryRoot)
	if err != nil {
		t.Fatalf("GetRepositoryByPath after doctor repair: %v", err)
	}
	if record.CanonicalPath != layout.RepositoryRoot {
		t.Fatalf("expected repaired canonical path %q, got %+v", layout.RepositoryRoot, record)
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

	cfg, _, err := policy.LoadOrDefault(repoRoot)
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	wantRepoRoot, err := filepath.EvalSymlinks(repoRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks(repoRoot): %v", err)
	}
	if cfg.Repo.MainWorktree != wantRepoRoot {
		t.Fatalf("expected repo init to default canonical main worktree to repo root %q, got %q", wantRepoRoot, cfg.Repo.MainWorktree)
	}
}

func TestRepoInitCanonicalizesRelativeMainWorktreeToRepoRoot(t *testing.T) {
	repoRoot, _ := createTestRepo(t)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot, "--main-worktree", "."}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	cfg, _, err := policy.LoadOrDefault(repoRoot)
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	wantRepoRoot, err := filepath.EvalSymlinks(repoRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks(repoRoot): %v", err)
	}
	if cfg.Repo.MainWorktree != wantRepoRoot {
		t.Fatalf("expected relative main worktree to canonicalize to %q, got %q", wantRepoRoot, cfg.Repo.MainWorktree)
	}
}

func TestRepoInitRejectsDetachedProtectedWorktree(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	detachedPath := filepath.Join(t.TempDir(), "detached-protected")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "--detach", detachedPath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	err := runRepoInit([]string{"--repo", detachedPath, "--main-worktree", detachedPath}, &initOut, &initErr)
	if err == nil {
		t.Fatalf("expected detached protected worktree rejection")
	}
	if !strings.Contains(err.Error(), "detached HEAD") {
		t.Fatalf("expected detached HEAD guidance, got %v", err)
	}
}

func TestRepoInitRejectsNonDefaultProtectedBranchWithoutExplicitOverride(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	protectedPath := filepath.Join(t.TempDir(), "protected-main")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "protected-main", protectedPath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	err := runRepoInit([]string{"--repo", protectedPath, "--main-worktree", protectedPath}, &initOut, &initErr)
	if err == nil {
		t.Fatalf("expected non-default protected branch rejection")
	}
	if !strings.Contains(err.Error(), `local branch "main"`) || !strings.Contains(err.Error(), `current branch is "protected-main"`) {
		t.Fatalf("expected main-branch guidance, got %v", err)
	}
}

func TestRepoShowWarnsWhenRootCheckoutIsDirtyAndNonCanonical(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	featurePath := filepath.Join(t.TempDir(), "feature-root-warning")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/root-warning", featurePath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot, "--main-worktree", featurePath}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	if err := os.WriteFile(filepath.Join(repoRoot, "dirty-root.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("WriteFile dirty-root.txt: %v", err)
	}

	var showOut bytes.Buffer
	var showErr bytes.Buffer
	if err := runRepoShow([]string{"--repo", repoRoot, "--json"}, &showOut, &showErr); err != nil {
		t.Fatalf("runRepoShow returned error: %v", err)
	}

	var result repoShowResult
	if err := json.Unmarshal(showOut.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	wantRepoRoot, err := filepath.EvalSymlinks(repoRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks(repoRoot): %v", err)
	}
	if result.RootCheckout.Path != wantRepoRoot {
		t.Fatalf("expected root checkout path %q, got %+v", wantRepoRoot, result.RootCheckout)
	}
	if result.RootCheckout.IsCanonical {
		t.Fatalf("expected non-canonical root checkout warning state, got %+v", result.RootCheckout)
	}
	if result.RootCheckout.Clean {
		t.Fatalf("expected dirty root checkout, got %+v", result.RootCheckout)
	}
	joined := strings.Join(result.Warnings, "\n")
	if !strings.Contains(joined, "differs from repository root") {
		t.Fatalf("expected canonical root mismatch warning, got %#v", result.Warnings)
	}
	if !strings.Contains(joined, "is dirty") {
		t.Fatalf("expected dirty root warning, got %#v", result.Warnings)
	}
}

func TestDoctorWarnsWhenRootCheckoutIsDirtyAndNonCanonical(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	featurePath := filepath.Join(t.TempDir(), "feature-doctor-root-warning")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/doctor-root-warning", featurePath)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot, "--main-worktree", featurePath}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	if err := os.WriteFile(filepath.Join(repoRoot, "dirty-root.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("WriteFile dirty-root.txt: %v", err)
	}

	var doctorOut bytes.Buffer
	var doctorErr bytes.Buffer
	if err := runDoctor([]string{"--repo", repoRoot, "--json"}, &doctorOut, &doctorErr); err != nil {
		t.Fatalf("runDoctor returned error: %v", err)
	}

	var result doctorResult
	if err := json.Unmarshal(doctorOut.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	wantRepoRoot, err := filepath.EvalSymlinks(repoRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks(repoRoot): %v", err)
	}
	if result.RootCheckout.Path != wantRepoRoot {
		t.Fatalf("expected root checkout path %q, got %+v", wantRepoRoot, result.RootCheckout)
	}
	joined := strings.Join(result.Warnings, "\n")
	if !strings.Contains(joined, "differs from repository root") {
		t.Fatalf("expected canonical root mismatch warning, got %#v", result.Warnings)
	}
	if !strings.Contains(joined, "is dirty") {
		t.Fatalf("expected dirty root warning, got %#v", result.Warnings)
	}
}

func TestDoctorJSONIncludesProtectedDirtyPaths(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	if err := os.WriteFile(filepath.Join(repoRoot, "skills-lock.json"), []byte("{\"drift\":true}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(skills-lock.json): %v", err)
	}

	var out bytes.Buffer
	var errOut bytes.Buffer
	if err := runDoctor([]string{"--repo", repoRoot, "--json"}, &out, &errOut); err != nil {
		t.Fatalf("runDoctor returned error: %v", err)
	}

	var result doctorResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal doctor result: %v", err)
	}
	if result.ProtectedBranchClean {
		t.Fatalf("expected protected branch to be dirty, got %+v", result)
	}
	if len(result.ProtectedDirtyPaths) != 1 || result.ProtectedDirtyPaths[0] != "skills-lock.json" {
		t.Fatalf("expected protected dirty path to include skills-lock.json, got %+v", result.ProtectedDirtyPaths)
	}
}

func TestStatusJSONWorksWhenCanonicalRootIsDirty(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	if err := os.WriteFile(filepath.Join(repoRoot, "dirty-root.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("WriteFile dirty-root.txt: %v", err)
	}

	var statusOut bytes.Buffer
	var statusErr bytes.Buffer
	if err := runStatus([]string{"--repo", repoRoot, "--json", "--events", "3"}, &statusOut, &statusErr); err != nil {
		t.Fatalf("runStatus returned error: %v", err)
	}

	var result statusResult
	err := json.Unmarshal(statusOut.Bytes(), &result)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result.RepositoryRoot == "" || result.ProtectedBranch != "main" {
		t.Fatalf("unexpected status result: %+v", result)
	}
}

func TestRepoAuditListsUnmergedWorktreeBranches(t *testing.T) {
	repoRoot, _ := createTestRepo(t)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", repoRoot}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}
	runTestCommand(t, repoRoot, "git", "add", "mainline.toml")
	runTestCommand(t, repoRoot, "git", "commit", "-m", "Initialize mainline repo policy")

	mergedPath := filepath.Join(t.TempDir(), "feature-merged")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/merged", mergedPath)
	writeFileAndCommit(t, mergedPath, "merged.txt", "merged\n", "feature merged")
	runTestCommand(t, repoRoot, "git", "merge", "--ff-only", "feature/merged")

	unmergedPath := filepath.Join(t.TempDir(), "feature-unmerged")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/unmerged", unmergedPath)
	writeFileAndCommit(t, unmergedPath, "unmerged.txt", "unmerged\n", "feature unmerged")

	var auditOut bytes.Buffer
	var auditErr bytes.Buffer
	if err := runRepoAudit([]string{"--repo", repoRoot, "--json"}, &auditOut, &auditErr); err != nil {
		t.Fatalf("runRepoAudit returned error: %v", err)
	}

	var result repoAuditResult
	if err := json.Unmarshal(auditOut.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(result.Unmerged) != 1 {
		t.Fatalf("expected one unmerged branch, got %+v", result.Unmerged)
	}
	if result.Unmerged[0].Branch != "feature/unmerged" {
		t.Fatalf("expected feature/unmerged, got %+v", result.Unmerged[0])
	}
	wantPath, err := filepath.EvalSymlinks(unmergedPath)
	if err != nil {
		t.Fatalf("EvalSymlinks(unmergedPath): %v", err)
	}
	if result.Unmerged[0].WorktreePath != wantPath {
		t.Fatalf("expected worktree path %q, got %+v", wantPath, result.Unmerged[0])
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

func TestRepoShowReportsBareStorageTopologyForRootCheckout(t *testing.T) {
	bareDir, worktreePath := createBareCloneWorktree(t)

	var initOut bytes.Buffer
	var initErr bytes.Buffer
	if err := runRepoInit([]string{"--repo", worktreePath, "--protected-branch", "main"}, &initOut, &initErr); err != nil {
		t.Fatalf("runRepoInit returned error: %v", err)
	}

	var showOut bytes.Buffer
	var showErr bytes.Buffer
	if err := runRepoShow([]string{"--repo", worktreePath, "--json"}, &showOut, &showErr); err != nil {
		t.Fatalf("runRepoShow returned error: %v", err)
	}

	var result repoShowResult
	if err := json.Unmarshal(showOut.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	wantBareDir, err := filepath.EvalSymlinks(bareDir)
	if err != nil {
		t.Fatalf("EvalSymlinks(bareDir): %v", err)
	}
	if result.RootCheckout.Path != wantBareDir || result.RootCheckout.Exists {
		t.Fatalf("expected bare storage root checkout info, got %+v", result.RootCheckout)
	}
	if result.RootCheckout.Topology != "bare-repository-storage" {
		t.Fatalf("expected bare storage topology, got %+v", result.RootCheckout)
	}
	if !strings.Contains(strings.Join(result.Warnings, "\n"), "bare Git storage") {
		t.Fatalf("expected bare storage warning, got %#v", result.Warnings)
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
	runTestCommand(t, repoRoot, "git", "add", "mainline.toml")
	runTestCommand(t, repoRoot, "git", "commit", "-m", "Initialize mainline repo policy")

	cfg, _, err := policy.LoadOrDefault(repoRoot)
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	cfg.Repo.WorktreeLayoutPolicy = "enforce-prefix"
	cfg.Repo.WorktreeRootPrefix = filepath.Join(repoRoot, "_wt")
	if err := policy.SaveFile(repoRoot, cfg); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}
	runTestCommand(t, repoRoot, "git", "add", "mainline.toml")
	runTestCommand(t, repoRoot, "git", "commit", "-m", "Update worktree layout policy")

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
	runTestCommand(t, repoRoot, "git", "add", "mainline.toml")
	runTestCommand(t, repoRoot, "git", "commit", "-m", "Update worktree layout policy")

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

func TestDoctorFixClearsStaleLockAndRecoversRunningSubmission(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	featurePath := filepath.Join(t.TempDir(), "feature-running")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/running", featurePath)
	writeFileAndCommit(t, featurePath, "running.txt", "running\n", "feature running")
	submitBranch(t, featurePath)

	layout, err := git.DiscoverRepositoryLayout(repoRoot)
	if err != nil {
		t.Fatalf("DiscoverRepositoryLayout: %v", err)
	}
	store := state.NewStore(state.DefaultPath(layout.GitDir))
	repoRecord, err := store.GetRepositoryByPath(context.Background(), layout.RepositoryRoot)
	if err != nil {
		t.Fatalf("GetRepositoryByPath: %v", err)
	}
	submissions, err := store.ListIntegrationSubmissions(context.Background(), repoRecord.ID)
	if err != nil {
		t.Fatalf("ListIntegrationSubmissions: %v", err)
	}
	if _, err := store.UpdateIntegrationSubmissionStatus(context.Background(), submissions[0].ID, "running", ""); err != nil {
		t.Fatalf("UpdateIntegrationSubmissionStatus: %v", err)
	}

	lockDir := filepath.Join(layout.GitDir, "mainline", "locks")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	payload, err := json.Marshal(state.LeaseMetadata{
		Domain:    state.IntegrationLock,
		RepoRoot:  layout.RepositoryRoot,
		Owner:     "crashed-worker",
		CreatedAt: time.Now().UTC().Add(-2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lockDir, state.IntegrationLock+".lock.json"), payload, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var doctorOut bytes.Buffer
	var doctorErr bytes.Buffer
	if err := runDoctor([]string{"--repo", repoRoot, "--fix", "--json"}, &doctorOut, &doctorErr); err != nil {
		t.Fatalf("runDoctor returned error: %v", err)
	}

	var result doctorResult
	if err := json.Unmarshal(doctorOut.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(result.StaleLocks) != 0 {
		t.Fatalf("expected stale locks cleared, got %+v", result.StaleLocks)
	}
	updated, err := store.GetIntegrationSubmission(context.Background(), submissions[0].ID)
	if err != nil {
		t.Fatalf("GetIntegrationSubmission: %v", err)
	}
	if updated.Status != "queued" {
		t.Fatalf("expected recovered submission to be queued, got %+v", updated)
	}
}

func TestDoctorFixFailsQueuedSubmissionWithMissingBranch(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	featurePath := filepath.Join(t.TempDir(), "feature-missing")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "feature/missing", featurePath)
	writeFileAndCommit(t, featurePath, "missing.txt", "missing\n", "feature missing")
	submitBranch(t, featurePath)
	runTestCommand(t, repoRoot, "git", "worktree", "remove", "--force", featurePath)
	runTestCommand(t, repoRoot, "git", "branch", "-D", "feature/missing")

	var doctorOut bytes.Buffer
	var doctorErr bytes.Buffer
	if err := runDoctor([]string{"--repo", repoRoot, "--fix", "--json"}, &doctorOut, &doctorErr); err != nil {
		t.Fatalf("runDoctor returned error: %v", err)
	}

	layout, err := git.DiscoverRepositoryLayout(repoRoot)
	if err != nil {
		t.Fatalf("DiscoverRepositoryLayout: %v", err)
	}
	store := state.NewStore(state.DefaultPath(layout.GitDir))
	repoRecord, err := store.GetRepositoryByPath(context.Background(), layout.RepositoryRoot)
	if err != nil {
		t.Fatalf("GetRepositoryByPath: %v", err)
	}
	submissions, err := store.ListIntegrationSubmissions(context.Background(), repoRecord.ID)
	if err != nil {
		t.Fatalf("ListIntegrationSubmissions: %v", err)
	}
	if submissions[0].Status != "failed" || !strings.Contains(submissions[0].LastError, "no longer exists") {
		t.Fatalf("expected missing branch submission to fail, got %+v", submissions[0])
	}
}

func TestDoctorFixAbortsProtectedMergeState(t *testing.T) {
	repoRoot, _ := createTestRepo(t)
	initRepoForWorker(t, repoRoot)

	topicPath := filepath.Join(t.TempDir(), "topic")
	runTestCommand(t, repoRoot, "git", "worktree", "add", "-b", "topic", topicPath)
	replaceFileAndCommit(t, topicPath, "README.md", "# topic\n", "topic change")
	replaceFileAndCommit(t, repoRoot, "README.md", "# main\n", "main change")
	mergeCmd := exec.Command("git", "merge", "topic")
	mergeCmd.Dir = repoRoot
	if err := mergeCmd.Run(); err == nil {
		t.Fatalf("expected merge conflict to leave protected worktree dirty")
	}

	engine := git.NewEngine(repoRoot)
	op, err := engine.InProgressOperation(repoRoot)
	if err != nil {
		t.Fatalf("InProgressOperation: %v", err)
	}
	if op != "merge" {
		t.Fatalf("expected merge state, got %q", op)
	}

	var doctorOut bytes.Buffer
	var doctorErr bytes.Buffer
	if err := runDoctor([]string{"--repo", repoRoot, "--fix"}, &doctorOut, &doctorErr); err != nil {
		t.Fatalf("runDoctor returned error: %v", err)
	}

	op, err = engine.InProgressOperation(repoRoot)
	if err != nil {
		t.Fatalf("InProgressOperation after doctor: %v", err)
	}
	if op != "" {
		t.Fatalf("expected merge state cleared, got %q", op)
	}
	clean, err := engine.WorktreeIsClean(repoRoot)
	if err != nil {
		t.Fatalf("WorktreeIsClean: %v", err)
	}
	if !clean {
		t.Fatalf("expected protected worktree clean after abort")
	}
}

func TestDoctorFixQueuesPublishForProtectedTipAheadOfUpstream(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	writeFileAndCommit(t, repoRoot, "ahead.txt", "ahead\n", "main ahead")
	head := strings.TrimSpace(runTestCommand(t, repoRoot, "git", "rev-parse", "HEAD"))

	var doctorOut bytes.Buffer
	var doctorErr bytes.Buffer
	if err := runDoctor([]string{"--repo", repoRoot, "--fix", "--json"}, &doctorOut, &doctorErr); err != nil {
		t.Fatalf("runDoctor returned error: %v", err)
	}

	layout, err := git.DiscoverRepositoryLayout(repoRoot)
	if err != nil {
		t.Fatalf("DiscoverRepositoryLayout: %v", err)
	}
	store := state.NewStore(state.DefaultPath(layout.GitDir))
	repoRecord, err := store.GetRepositoryByPath(context.Background(), layout.RepositoryRoot)
	if err != nil {
		t.Fatalf("GetRepositoryByPath: %v", err)
	}
	requests, err := store.ListPublishRequests(context.Background(), repoRecord.ID)
	if err != nil {
		t.Fatalf("ListPublishRequests: %v", err)
	}
	if len(requests) != 1 || requests[0].Status != "queued" || requests[0].TargetSHA != head {
		t.Fatalf("expected queued publish for protected tip %q, got %+v", head, requests)
	}
}

func TestDoctorFixDoesNotQueuePublishWhenProtectedWorktreeIsDirty(t *testing.T) {
	repoRoot, _ := createTestRepoWithRemote(t)
	initRepoForWorker(t, repoRoot)

	writeFileAndCommit(t, repoRoot, "ahead.txt", "ahead\n", "main ahead")
	if err := os.WriteFile(filepath.Join(repoRoot, "dirty.txt"), []byte("dirty"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var doctorOut bytes.Buffer
	var doctorErr bytes.Buffer
	if err := runDoctor([]string{"--repo", repoRoot, "--fix", "--json"}, &doctorOut, &doctorErr); err != nil {
		t.Fatalf("runDoctor returned error: %v", err)
	}

	layout, err := git.DiscoverRepositoryLayout(repoRoot)
	if err != nil {
		t.Fatalf("DiscoverRepositoryLayout: %v", err)
	}
	store := state.NewStore(state.DefaultPath(layout.GitDir))
	repoRecord, err := store.GetRepositoryByPath(context.Background(), layout.RepositoryRoot)
	if err != nil {
		t.Fatalf("GetRepositoryByPath: %v", err)
	}
	requests, err := store.ListPublishRequests(context.Background(), repoRecord.ID)
	if err != nil {
		t.Fatalf("ListPublishRequests: %v", err)
	}
	if len(requests) != 0 {
		t.Fatalf("expected no publish requests when protected worktree is dirty, got %+v", requests)
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
