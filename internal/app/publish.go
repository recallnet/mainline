package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/recallnet/mainline/internal/domain"
	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/policy"
	"github.com/recallnet/mainline/internal/state"
)

type publishResult struct {
	OK               bool                 `json:"ok"`
	PublishRequestID int64                `json:"publish_request_id"`
	RepositoryRoot   string               `json:"repository_root"`
	StatePath        string               `json:"state_path"`
	TargetSHA        string               `json:"target_sha"`
	Status           domain.PublishStatus `json:"status"`
	DrainAttempted   bool                 `json:"drain_attempted,omitempty"`
	DrainResult      string               `json:"drain_result,omitempty"`
}

func runPublish(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet(currentCLIProgramName()+" publish", flag.ContinueOnError)
	fs.SetOutput(stderr)
	setFlagUsage(fs, fmt.Sprintf(`Usage:
  %s publish [flags]

Queue publish of the current protected-branch tip.

Examples:
  mq publish --repo /path/to/repo-root
  mq publish --repo /path/to/repo-root --json

Flags:
`, currentCLIProgramName()))

	var repoPath string
	var asJSON bool
	fs.StringVar(&repoPath, "repo", ".", "repository path")
	fs.BoolVar(&asJSON, "json", false, "output json")

	if err := fs.Parse(args); err != nil {
		return err
	}

	layout, repoRoot, cfg, repoRecord, store, err := loadRepoContext(repoPath)
	if err != nil {
		return err
	}

	mainEngine := git.NewEngine(cfg.Repo.MainWorktree)
	if _, err := ensureProtectedRootHealthy(
		context.Background(),
		mainEngine,
		cfg,
		store,
		repoRecord,
		protectedRootRecoveryAllowQueued,
	); err != nil {
		return err
	}
	if !mainEngine.BranchExists(cfg.Repo.ProtectedBranch) {
		return fmt.Errorf("protected branch %q does not exist", cfg.Repo.ProtectedBranch)
	}

	targetSHA, err := mainEngine.BranchHeadSHA(cfg.Repo.ProtectedBranch)
	if err != nil {
		return err
	}

	ctx := context.Background()
	request, err := store.CreatePublishRequest(ctx, state.PublishRequest{
		RepoID:    repoRecord.ID,
		TargetSHA: targetSHA,
		Status:    domain.PublishStatusQueued,
	})
	if err != nil {
		return err
	}

	if err := appendStateEvent(ctx, store, state.EventRecord{
		RepoID:    repoRecord.ID,
		ItemType:  domain.ItemTypePublishRequest,
		ItemID:    state.NullInt64(request.ID),
		EventType: domain.EventTypePublishRequested,
		Payload: mustJSON(domain.PublishRequestedPayload{
			TargetSHA: targetSHA,
			Reason:    "manual",
		}),
	}); err != nil {
		return err
	}

	result := publishResult{
		OK:               true,
		PublishRequestID: request.ID,
		RepositoryRoot:   repoRoot,
		StatePath:        state.DefaultPath(layout.GitDir),
		TargetSHA:        targetSHA,
		Status:           request.Status,
	}

	if shouldTryDrainAfterMutation() {
		result.DrainAttempted = true
		drainResult, drainErr := drainRepoUntilSettled(cfg.Repo.MainWorktree)
		if drainResult != "" {
			result.DrainResult = drainResult
		}
		if drainErr != nil {
			return drainErr
		}
		if updated, loadErr := store.GetPublishRequest(ctx, request.ID); loadErr == nil {
			result.Status = updated.Status
		}
	}

	if asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	fmt.Fprintf(stdout, "Queued publish request %d\n", request.ID)
	fmt.Fprintf(stdout, "Target SHA: %s\n", targetSHA)
	fmt.Fprintf(stdout, "State path: %s\n", state.DefaultPath(layout.GitDir))
	if result.DrainResult != "" {
		fmt.Fprintf(stdout, "Drain result: %s\n", result.DrainResult)
	}
	return nil
}

func shouldTryDrainAfterMutation() bool {
	return os.Getenv("MAINLINE_DISABLE_MUTATION_DRAIN") == ""
}

func protectedWorktreeDirtyError(mainWorktree string, dirtyPaths []string) error {
	queueBlockedGuidance := "; mainline is blocked until the protected root checkout is clean. Take ownership of the protected root checkout, run `mq doctor --repo " + mainWorktree + "`, and if the queue is idle use `mq doctor --repo " + mainWorktree + " --fix` to restore shipped main. Otherwise save, clean, or resolve the dirty state before retrying"
	if len(dirtyPaths) == 0 {
		return fmt.Errorf("protected branch worktree %s is dirty%s", mainWorktree, queueBlockedGuidance)
	}
	preview := dirtyPaths
	if len(preview) > 5 {
		preview = dirtyPaths[:5]
	}
	message := fmt.Sprintf("protected branch worktree %s is dirty (%s)", mainWorktree, strings.Join(preview, ", "))
	if len(dirtyPaths) > len(preview) {
		message = fmt.Sprintf("%s and %d more", message, len(dirtyPaths)-len(preview))
	}
	return fmt.Errorf("%s%s", message, queueBlockedGuidance)
}

func loadRepoContext(repoPath string) (git.RepositoryLayout, string, policy.File, state.RepositoryRecord, state.Store, error) {
	layout, err := git.DiscoverRepositoryLayout(repoPath)
	if err != nil {
		return git.RepositoryLayout{}, "", policy.File{}, state.RepositoryRecord{}, state.Store{}, err
	}
	repoRoot := layout.RepositoryRoot

	cfg, _, err := policy.LoadOrDefault(repoRoot)
	if err != nil {
		return git.RepositoryLayout{}, "", policy.File{}, state.RepositoryRecord{}, state.Store{}, err
	}
	if cfg.Repo.MainWorktree == "" {
		cfg.Repo.MainWorktree = layout.WorktreeRoot
	}

	store := state.NewStore(state.DefaultPath(layout.GitDir))
	if !store.Exists() {
		return git.RepositoryLayout{}, "", policy.File{}, state.RepositoryRecord{}, state.Store{}, fmt.Errorf("repository is not initialized; run `mainline repo init` first")
	}
	if err := store.EnsureSchema(context.Background()); err != nil {
		return git.RepositoryLayout{}, "", policy.File{}, state.RepositoryRecord{}, state.Store{}, err
	}

	repoRecord, _, err := ensureRepositoryRecord(context.Background(), store, repoRoot, cfg)
	if err != nil {
		return git.RepositoryLayout{}, "", policy.File{}, state.RepositoryRecord{}, state.Store{}, err
	}

	return layout, repoRoot, cfg, repoRecord, store, nil
}

func ensureRepositoryRecord(ctx context.Context, store state.Store, repoRoot string, cfg policy.File) (state.RepositoryRecord, bool, error) {
	record, err := store.GetRepositoryByPath(ctx, repoRoot)
	if err == nil {
		return record, false, nil
	}
	if !errors.Is(err, state.ErrNotFound) {
		return state.RepositoryRecord{}, false, err
	}
	if cfg.Repo.ProtectedBranch == "" {
		return state.RepositoryRecord{}, false, fmt.Errorf("repository state exists at %s but no repo record matches %s; run `mq repo init --repo %s` to repair it", store.Path, repoRoot, repoRoot)
	}

	record, err = store.UpsertRepository(ctx, state.RepositoryRecord{
		CanonicalPath:   repoRoot,
		ProtectedBranch: cfg.Repo.ProtectedBranch,
		RemoteName:      cfg.Repo.RemoteName,
		MainWorktree:    cfg.Repo.MainWorktree,
		PolicyVersion:   "v1",
	})
	if err != nil {
		return state.RepositoryRecord{}, false, err
	}
	return record, true, nil
}
