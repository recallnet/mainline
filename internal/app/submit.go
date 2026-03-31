package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/user"
	"path/filepath"

	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/policy"
	"github.com/recallnet/mainline/internal/state"
)

type submitOptions struct {
	repoPath     string
	branch       string
	worktreePath string
	requestedBy  string
}

type queuedSubmission struct {
	Layout     git.RepositoryLayout
	RepoRoot   string
	Config     policy.File
	Store      state.Store
	RepoRecord state.RepositoryRecord
	Submission state.IntegrationSubmission
}

func runSubmit(args []string, stdout io.Writer, stderr io.Writer) error {
	if err := applyAppTestFault("submit.start"); err != nil {
		return err
	}

	fs := flag.NewFlagSet("mainline submit", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var repoPath string
	var branch string
	var worktreePath string
	var requestedBy string

	fs.StringVar(&repoPath, "repo", ".", "repository path")
	fs.StringVar(&branch, "branch", "", "branch to submit")
	fs.StringVar(&worktreePath, "worktree", "", "source worktree path")
	fs.StringVar(&requestedBy, "requested-by", "", "submitter identity")

	if err := fs.Parse(args); err != nil {
		return err
	}

	queued, err := queueSubmission(submitOptions{
		repoPath:     repoPath,
		branch:       branch,
		worktreePath: worktreePath,
		requestedBy:  requestedBy,
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "Queued submission %d\n", queued.Submission.ID)
	fmt.Fprintf(stdout, "Branch: %s\n", queued.Submission.BranchName)
	fmt.Fprintf(stdout, "Worktree: %s\n", queued.Submission.SourceWorktree)
	fmt.Fprintf(stdout, "Source SHA: %s\n", queued.Submission.SourceSHA)
	return nil
}

func queueSubmission(opts submitOptions) (queuedSubmission, error) {
	layout, err := git.DiscoverRepositoryLayout(opts.repoPath)
	if err != nil {
		return queuedSubmission{}, err
	}
	repoRoot := layout.RepositoryRoot

	cfg, _, err := policy.LoadOrDefault(repoRoot)
	if err != nil {
		return queuedSubmission{}, err
	}
	if cfg.Repo.MainWorktree == "" {
		cfg.Repo.MainWorktree = layout.WorktreeRoot
	}

	worktreePath := opts.worktreePath
	if worktreePath == "" {
		worktreePath = layout.WorktreeRoot
	}
	worktreePath = filepath.Clean(worktreePath)

	worktreeLayout, err := git.DiscoverRepositoryLayout(worktreePath)
	if err != nil {
		return queuedSubmission{}, err
	}
	if filepath.Clean(worktreeLayout.GitDir) != filepath.Clean(layout.GitDir) {
		return queuedSubmission{}, fmt.Errorf("worktree %s does not belong to repository %s", worktreePath, repoRoot)
	}

	engine := git.NewEngine(worktreePath)
	worktree, err := engine.ResolveWorktree(worktreePath)
	if err != nil {
		return queuedSubmission{}, err
	}

	branch := opts.branch
	if branch == "" {
		if worktree.IsDetached {
			return queuedSubmission{}, fmt.Errorf("cannot submit from detached HEAD; pass --branch with a checked-out branch worktree")
		}
		branch = worktree.Branch
	}

	currentBranch, err := engine.CurrentBranchAtPath(worktreePath)
	if err != nil {
		return queuedSubmission{}, err
	}
	if currentBranch != branch {
		return queuedSubmission{}, fmt.Errorf("branch %q is not checked out in worktree %s", branch, worktreePath)
	}

	if branch == cfg.Repo.ProtectedBranch {
		return queuedSubmission{}, fmt.Errorf("cannot submit protected branch %q", branch)
	}
	if !engine.BranchExists(branch) {
		return queuedSubmission{}, fmt.Errorf("branch %q does not exist", branch)
	}

	clean, err := engine.WorktreeIsClean(worktreePath)
	if err != nil {
		return queuedSubmission{}, err
	}
	if !clean {
		return queuedSubmission{}, fmt.Errorf("source worktree %s is dirty; clean it before submission", worktreePath)
	}

	commitCount, err := engine.CommitCount(branch)
	if err != nil {
		return queuedSubmission{}, err
	}
	if commitCount == 0 {
		return queuedSubmission{}, fmt.Errorf("branch %q has no commits", branch)
	}

	headSHA, err := engine.BranchHeadSHA(branch)
	if err != nil {
		return queuedSubmission{}, fmt.Errorf("resolve branch head for %q: %w", branch, err)
	}

	requestedBy := opts.requestedBy
	if requestedBy == "" {
		currentUser, err := user.Current()
		if err == nil {
			requestedBy = currentUser.Username
		} else {
			requestedBy = "unknown"
		}
	}

	store := state.NewStore(state.DefaultPath(layout.GitDir))
	if !store.Exists() {
		return queuedSubmission{}, fmt.Errorf("repository is not initialized; run `mainline repo init` first")
	}

	ctx := context.Background()
	repoRecord, err := store.GetRepositoryByPath(ctx, repoRoot)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			return queuedSubmission{}, err
		}

		repoRecord, err = store.UpsertRepository(ctx, state.RepositoryRecord{
			CanonicalPath:   repoRoot,
			ProtectedBranch: cfg.Repo.ProtectedBranch,
			RemoteName:      cfg.Repo.RemoteName,
			MainWorktree:    cfg.Repo.MainWorktree,
			PolicyVersion:   "v1",
		})
		if err != nil {
			return queuedSubmission{}, err
		}
	}

	submission, err := store.CreateIntegrationSubmission(ctx, state.IntegrationSubmission{
		RepoID:         repoRecord.ID,
		BranchName:     branch,
		SourceWorktree: worktree.Path,
		SourceSHA:      headSHA,
		RequestedBy:    requestedBy,
		Status:         "queued",
	})
	if err != nil {
		return queuedSubmission{}, err
	}

	payload, err := json.Marshal(map[string]string{
		"branch":          branch,
		"source_worktree": worktree.Path,
		"source_sha":      headSHA,
		"requested_by":    requestedBy,
	})
	if err != nil {
		return queuedSubmission{}, err
	}
	if _, err := store.AppendEvent(ctx, state.EventRecord{
		RepoID:    repoRecord.ID,
		ItemType:  "integration_submission",
		ItemID:    state.NullInt64(submission.ID),
		EventType: "submission.created",
		Payload:   payload,
	}); err != nil {
		return queuedSubmission{}, err
	}

	return queuedSubmission{
		Layout:     layout,
		RepoRoot:   repoRoot,
		Config:     cfg,
		Store:      store,
		RepoRecord: repoRecord,
		Submission: submission,
	}, nil
}
