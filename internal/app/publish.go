package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/policy"
	"github.com/recallnet/mainline/internal/state"
)

type publishResult struct {
	OK               bool   `json:"ok"`
	PublishRequestID int64  `json:"publish_request_id"`
	RepositoryRoot   string `json:"repository_root"`
	StatePath        string `json:"state_path"`
	TargetSHA        string `json:"target_sha"`
	Status           string `json:"status"`
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
		Status:    "queued",
	})
	if err != nil {
		return err
	}

	if err := appendStateEvent(ctx, store, state.EventRecord{
		RepoID:    repoRecord.ID,
		ItemType:  "publish_request",
		ItemID:    state.NullInt64(request.ID),
		EventType: "publish.requested",
		Payload: mustJSON(map[string]string{
			"target_sha": targetSHA,
			"reason":     "manual",
			"repo_root":  repoRoot,
		}),
	}); err != nil {
		return err
	}

	if asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(publishResult{
			OK:               true,
			PublishRequestID: request.ID,
			RepositoryRoot:   repoRoot,
			StatePath:        state.DefaultPath(layout.GitDir),
			TargetSHA:        targetSHA,
			Status:           request.Status,
		})
	}

	fmt.Fprintf(stdout, "Queued publish request %d\n", request.ID)
	fmt.Fprintf(stdout, "Target SHA: %s\n", targetSHA)
	fmt.Fprintf(stdout, "State path: %s\n", state.DefaultPath(layout.GitDir))
	return nil
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
