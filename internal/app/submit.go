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

type submitValidationError struct {
	Code    string
	Message string
}

func (e *submitValidationError) Error() string {
	return e.Message
}

type submitOptions struct {
	repoPath     string
	branch       string
	worktreePath string
	requestedBy  string
}

type preparedSubmission struct {
	Layout       git.RepositoryLayout
	RepoRoot     string
	Config       policy.File
	Store        state.Store
	Branch       string
	WorktreePath string
	SourceSHA    string
	RequestedBy  string
}

type queuedSubmission struct {
	Layout     git.RepositoryLayout
	RepoRoot   string
	Config     policy.File
	Store      state.Store
	RepoRecord state.RepositoryRecord
	Submission state.IntegrationSubmission
}

type submitResult struct {
	OK             bool   `json:"ok"`
	Checked        bool   `json:"checked"`
	Queued         bool   `json:"queued"`
	SubmissionID   int64  `json:"submission_id,omitempty"`
	Branch         string `json:"branch,omitempty"`
	SourceWorktree string `json:"source_worktree,omitempty"`
	SourceSHA      string `json:"source_sha,omitempty"`
	RepositoryRoot string `json:"repository_root,omitempty"`
	RequestedBy    string `json:"requested_by,omitempty"`
	ErrorCode      string `json:"error_code,omitempty"`
	Error          string `json:"error,omitempty"`
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
	var asJSON bool
	var checkOnly bool

	fs.StringVar(&repoPath, "repo", ".", "repository path")
	fs.StringVar(&branch, "branch", "", "branch to submit")
	fs.StringVar(&worktreePath, "worktree", "", "source worktree path")
	fs.StringVar(&requestedBy, "requested-by", "", "submitter identity")
	fs.BoolVar(&asJSON, "json", false, "output json")
	fs.BoolVar(&checkOnly, "check", false, "validate submission without queueing it")

	if err := fs.Parse(args); err != nil {
		return err
	}

	opts := submitOptions{
		repoPath:     repoPath,
		branch:       branch,
		worktreePath: worktreePath,
		requestedBy:  requestedBy,
	}
	prepared, err := prepareSubmission(opts)
	if err != nil {
		if asJSON {
			return writeSubmitJSON(stdout, submitResult{
				OK:        false,
				Checked:   true,
				Queued:    false,
				ErrorCode: submitErrorCode(err),
				Error:     err.Error(),
			}, err)
		}
		return err
	}

	if checkOnly {
		result := submitResult{
			OK:             true,
			Checked:        true,
			Queued:         false,
			Branch:         prepared.Branch,
			SourceWorktree: prepared.WorktreePath,
			SourceSHA:      prepared.SourceSHA,
			RepositoryRoot: prepared.RepoRoot,
			RequestedBy:    prepared.RequestedBy,
		}
		if asJSON {
			return writeSubmitJSON(stdout, result, nil)
		}
		fmt.Fprintln(stdout, "Submission check passed")
		fmt.Fprintf(stdout, "Branch: %s\n", result.Branch)
		fmt.Fprintf(stdout, "Worktree: %s\n", result.SourceWorktree)
		fmt.Fprintf(stdout, "Source SHA: %s\n", result.SourceSHA)
		return nil
	}

	queued, err := queuePreparedSubmission(prepared)
	if err != nil {
		if asJSON {
			return writeSubmitJSON(stdout, submitResult{
				OK:             false,
				Checked:        true,
				Queued:         false,
				Branch:         prepared.Branch,
				SourceWorktree: prepared.WorktreePath,
				SourceSHA:      prepared.SourceSHA,
				RepositoryRoot: prepared.RepoRoot,
				RequestedBy:    prepared.RequestedBy,
				ErrorCode:      submitErrorCode(err),
				Error:          err.Error(),
			}, err)
		}
		return err
	}

	result := submitResult{
		OK:             true,
		Checked:        true,
		Queued:         true,
		SubmissionID:   queued.Submission.ID,
		Branch:         queued.Submission.BranchName,
		SourceWorktree: queued.Submission.SourceWorktree,
		SourceSHA:      queued.Submission.SourceSHA,
		RepositoryRoot: queued.RepoRoot,
		RequestedBy:    queued.Submission.RequestedBy,
	}
	if asJSON {
		return writeSubmitJSON(stdout, result, nil)
	}

	fmt.Fprintf(stdout, "Queued submission %d\n", queued.Submission.ID)
	fmt.Fprintf(stdout, "Branch: %s\n", queued.Submission.BranchName)
	fmt.Fprintf(stdout, "Worktree: %s\n", queued.Submission.SourceWorktree)
	fmt.Fprintf(stdout, "Source SHA: %s\n", queued.Submission.SourceSHA)
	return nil
}

func queueSubmission(opts submitOptions) (queuedSubmission, error) {
	prepared, err := prepareSubmission(opts)
	if err != nil {
		return queuedSubmission{}, err
	}
	return queuePreparedSubmission(prepared)
}

func prepareSubmission(opts submitOptions) (preparedSubmission, error) {
	layout, err := git.DiscoverRepositoryLayout(opts.repoPath)
	if err != nil {
		return preparedSubmission{}, &submitValidationError{
			Code:    "not_git_repository",
			Message: err.Error(),
		}
	}
	repoRoot := layout.RepositoryRoot

	cfg, _, err := policy.LoadOrDefault(repoRoot)
	if err != nil {
		return preparedSubmission{}, err
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
		return preparedSubmission{}, &submitValidationError{
			Code:    "invalid_worktree",
			Message: err.Error(),
		}
	}
	if filepath.Clean(worktreeLayout.GitDir) != filepath.Clean(layout.GitDir) {
		return preparedSubmission{}, &submitValidationError{
			Code:    "foreign_worktree",
			Message: fmt.Sprintf("worktree %s does not belong to repository %s", worktreePath, repoRoot),
		}
	}

	engine := git.NewEngine(worktreePath)
	worktree, err := engine.ResolveWorktree(worktreePath)
	if err != nil {
		return preparedSubmission{}, err
	}

	branch := opts.branch
	if branch == "" {
		if worktree.IsDetached {
			return preparedSubmission{}, &submitValidationError{
				Code:    "detached_head",
				Message: "cannot submit from detached HEAD; pass --branch with a checked-out branch worktree",
			}
		}
		branch = worktree.Branch
	}

	currentBranch, err := engine.CurrentBranchAtPath(worktreePath)
	if err != nil {
		return preparedSubmission{}, err
	}
	if currentBranch != branch {
		return preparedSubmission{}, &submitValidationError{
			Code:    "branch_not_checked_out",
			Message: fmt.Sprintf("branch %q is not checked out in worktree %s", branch, worktreePath),
		}
	}

	if branch == cfg.Repo.ProtectedBranch {
		return preparedSubmission{}, &submitValidationError{
			Code:    "protected_branch",
			Message: fmt.Sprintf("cannot submit protected branch %q", branch),
		}
	}
	if !engine.BranchExists(branch) {
		return preparedSubmission{}, &submitValidationError{
			Code:    "branch_missing",
			Message: fmt.Sprintf("branch %q does not exist", branch),
		}
	}

	clean, err := engine.WorktreeIsClean(worktreePath)
	if err != nil {
		return preparedSubmission{}, err
	}
	if !clean {
		return preparedSubmission{}, &submitValidationError{
			Code:    "dirty_worktree",
			Message: fmt.Sprintf("source worktree %s is dirty; clean it before submission", worktreePath),
		}
	}

	commitCount, err := engine.CommitCount(branch)
	if err != nil {
		return preparedSubmission{}, err
	}
	if commitCount == 0 {
		return preparedSubmission{}, &submitValidationError{
			Code:    "branch_has_no_commits",
			Message: fmt.Sprintf("branch %q has no commits", branch),
		}
	}

	headSHA, err := engine.BranchHeadSHA(branch)
	if err != nil {
		return preparedSubmission{}, fmt.Errorf("resolve branch head for %q: %w", branch, err)
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
		return preparedSubmission{}, &submitValidationError{
			Code:    "repository_not_initialized",
			Message: "repository is not initialized; run `mainline repo init` first",
		}
	}

	return preparedSubmission{
		Layout:       layout,
		RepoRoot:     repoRoot,
		Config:       cfg,
		Store:        store,
		Branch:       branch,
		WorktreePath: worktree.Path,
		SourceSHA:    headSHA,
		RequestedBy:  requestedBy,
	}, nil
}

func queuePreparedSubmission(prepared preparedSubmission) (queuedSubmission, error) {
	ctx := context.Background()
	repoRecord, err := prepared.Store.GetRepositoryByPath(ctx, prepared.RepoRoot)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			return queuedSubmission{}, err
		}

		repoRecord, err = prepared.Store.UpsertRepository(ctx, state.RepositoryRecord{
			CanonicalPath:   prepared.RepoRoot,
			ProtectedBranch: prepared.Config.Repo.ProtectedBranch,
			RemoteName:      prepared.Config.Repo.RemoteName,
			MainWorktree:    prepared.Config.Repo.MainWorktree,
			PolicyVersion:   "v1",
		})
		if err != nil {
			return queuedSubmission{}, err
		}
	}

	submission, err := prepared.Store.CreateIntegrationSubmission(ctx, state.IntegrationSubmission{
		RepoID:         repoRecord.ID,
		BranchName:     prepared.Branch,
		SourceWorktree: prepared.WorktreePath,
		SourceSHA:      prepared.SourceSHA,
		RequestedBy:    prepared.RequestedBy,
		Status:         "queued",
	})
	if err != nil {
		return queuedSubmission{}, err
	}

	payload, err := json.Marshal(map[string]string{
		"branch":          prepared.Branch,
		"source_worktree": prepared.WorktreePath,
		"source_sha":      prepared.SourceSHA,
		"requested_by":    prepared.RequestedBy,
	})
	if err != nil {
		return queuedSubmission{}, err
	}
	if _, err := prepared.Store.AppendEvent(ctx, state.EventRecord{
		RepoID:    repoRecord.ID,
		ItemType:  "integration_submission",
		ItemID:    state.NullInt64(submission.ID),
		EventType: "submission.created",
		Payload:   payload,
	}); err != nil {
		return queuedSubmission{}, err
	}

	return queuedSubmission{
		Layout:     prepared.Layout,
		RepoRoot:   prepared.RepoRoot,
		Config:     prepared.Config,
		Store:      prepared.Store,
		RepoRecord: repoRecord,
		Submission: submission,
	}, nil
}

func writeSubmitJSON(stdout io.Writer, result submitResult, cmdErr error) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return err
	}
	return cmdErr
}

func submitErrorCode(err error) string {
	var validationErr *submitValidationError
	if errors.As(err, &validationErr) {
		return validationErr.Code
	}
	return "submit_failed"
}
