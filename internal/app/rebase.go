package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/state"
)

type rebaseResult struct {
	OK                bool     `json:"ok"`
	SubmissionID      int64    `json:"submission_id,omitempty"`
	Branch            string   `json:"branch,omitempty"`
	SourceWorktree    string   `json:"source_worktree,omitempty"`
	RepositoryRoot    string   `json:"repository_root,omitempty"`
	ProtectedBranch   string   `json:"protected_branch,omitempty"`
	ProtectedSHA      string   `json:"protected_sha,omitempty"`
	ProtectedUpstream string   `json:"protected_upstream,omitempty"`
	SyncedProtected   bool     `json:"synced_protected,omitempty"`
	Rebased           bool     `json:"rebased,omitempty"`
	AbortedOperation  string   `json:"aborted_operation,omitempty"`
	ConflictFiles     []string `json:"conflict_files,omitempty"`
	RetryCommand      string   `json:"retry_command,omitempty"`
	Status            string   `json:"status"`
	Error             string   `json:"error,omitempty"`
}

func runRebase(args []string, stdout *stepPrinter, stderr io.Writer) error {
	fs := flag.NewFlagSet(currentCLIProgramName()+" rebase", flag.ContinueOnError)
	fs.SetOutput(stderr)
	setFlagUsage(fs, fmt.Sprintf(`Usage:
  %s rebase [flags]

Rebase a topic branch onto the local protected branch using repo-aware mainline
rules.

Examples:
  mq rebase --repo /path/to/topic-worktree --branch feature/login
  mq rebase --repo /path/to/main --submission 23 --json

Flags:
`, currentCLIProgramName()))

	var repoPath string
	var submissionID int64
	var branch string
	var worktreePath string
	var asJSON bool

	fs.StringVar(&repoPath, "repo", ".", "repository or worktree path")
	fs.Int64Var(&submissionID, "submission", 0, "blocked submission id to rebase")
	fs.StringVar(&branch, "branch", "", "branch to rebase")
	fs.StringVar(&worktreePath, "worktree", "", "source worktree path override")
	fs.BoolVar(&asJSON, "json", false, "output json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	targets := 0
	if submissionID != 0 {
		targets++
	}
	if branch != "" {
		targets++
	}
	if targets != 1 {
		return fmt.Errorf("exactly one of --submission or --branch is required")
	}

	result, err := performRebase(repoPath, submissionID, branch, worktreePath)
	if asJSON {
		encoder := json.NewEncoder(stdout.Raw())
		encoder.SetIndent("", "  ")
		if encodeErr := encoder.Encode(result); encodeErr != nil {
			return encodeErr
		}
		return err
	}

	printer := stdout
	printer.Section("Rebase")
	if result.SubmissionID != 0 {
		printer.Line("Submission: %d", result.SubmissionID)
	}
	printer.Line("Branch: %s", result.Branch)
	printer.Line("Source worktree: %s", result.SourceWorktree)
	printer.Line("Protected branch: %s", result.ProtectedBranch)
	if result.ProtectedSHA != "" {
		printer.Line("Protected SHA: %s", result.ProtectedSHA)
	}
	if result.ProtectedUpstream != "" {
		printer.Line("Protected upstream: %s", result.ProtectedUpstream)
	}
	if result.AbortedOperation != "" {
		printer.Line("Aborted in-progress operation: %s", result.AbortedOperation)
	}
	printer.Line("Status: %s", result.Status)
	if len(result.ConflictFiles) > 0 {
		printer.Line("Conflict files: %s", stringsJoin(result.ConflictFiles))
	}
	if result.RetryCommand != "" {
		printer.Line("Next command: %s", result.RetryCommand)
	}
	if result.Error != "" {
		printer.Warning("Error: %s", result.Error)
	}
	return err
}

func performRebase(repoPath string, submissionID int64, branch string, worktreeOverride string) (rebaseResult, error) {
	layout, repoRoot, cfg, repoRecord, store, err := loadRepoContext(repoPath)
	if err != nil {
		return rebaseResult{}, err
	}
	targetBranch, sourceWorktree, retryCommand, submissionErr := resolveRebaseTarget(context.Background(), store, repoRecord.ID, submissionID, branch, worktreeOverride, layout.WorktreeRoot)
	if submissionErr != nil {
		return rebaseResult{}, submissionErr
	}

	result := rebaseResult{
		OK:              false,
		SubmissionID:    submissionID,
		Branch:          targetBranch,
		SourceWorktree:  sourceWorktree,
		RepositoryRoot:  repoRoot,
		ProtectedBranch: cfg.Repo.ProtectedBranch,
		RetryCommand:    retryCommand,
		Status:          "pending",
	}

	sourceEngine := git.NewEngine(sourceWorktree)
	if op, opErr := sourceEngine.InProgressOperation(sourceWorktree); opErr != nil {
		result.Status = "failed"
		result.Error = opErr.Error()
		return result, opErr
	} else if op != "" {
		err := fmt.Errorf("source worktree %s already has %s in progress; continue or abort it before rerunning mq rebase", sourceWorktree, op)
		result.Status = "failed"
		result.Error = err.Error()
		return result, exitWithCode(1, err)
	}

	protectedEngine := git.NewEngine(cfg.Repo.MainWorktree)
	syncResult, err := syncProtectedBranch(protectedEngine, cfg)
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		return result, err
	}
	result.SyncedProtected = syncResult.Synced
	if status, statusErr := protectedEngine.BranchStatus(cfg.Repo.ProtectedBranch, cfg.Repo.ProtectedBranch); statusErr == nil {
		result.ProtectedUpstream = status.Upstream
	}
	if protectedSHA, shaErr := protectedEngine.BranchHeadSHA(cfg.Repo.ProtectedBranch); shaErr == nil {
		result.ProtectedSHA = protectedSHA
	}

	currentBranch, err := sourceEngine.CurrentBranch()
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		return result, err
	}
	if currentBranch != targetBranch {
		err = fmt.Errorf("source worktree %s is on branch %q, expected %q", sourceWorktree, currentBranch, targetBranch)
		result.Status = "failed"
		result.Error = err.Error()
		return result, exitWithCode(1, err)
	}
	clean, err := sourceEngine.WorktreeIsClean(sourceWorktree)
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		return result, err
	}
	if !clean {
		err = fmt.Errorf("source worktree %s has uncommitted changes; clean, stash, or commit them before rebasing", sourceWorktree)
		result.Status = "failed"
		result.Error = err.Error()
		return result, exitWithCode(1, err)
	}

	beforeSHA, err := sourceEngine.BranchHeadSHA(targetBranch)
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		return result, err
	}
	err = sourceEngine.RebaseCurrentBranch(sourceWorktree, cfg.Repo.ProtectedBranch)
	if err != nil {
		switch {
		case errors.Is(err, git.ErrRebaseConflict):
			conflicts, _ := sourceEngine.ConflictedFiles(sourceWorktree)
			result.Status = "conflict"
			result.ConflictFiles = conflicts
			result.Error = err.Error()
			return result, exitWithCode(1, err)
		case errors.Is(err, git.ErrRebaseEmpty):
			result.Status = "empty"
			result.Error = err.Error()
			return result, exitWithCode(1, err)
		default:
			result.Status = "failed"
			result.Error = err.Error()
			return result, err
		}
	}
	afterSHA, err := sourceEngine.BranchHeadSHA(targetBranch)
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		return result, err
	}
	result.Rebased = beforeSHA != afterSHA
	result.OK = true
	if result.Rebased {
		result.Status = "rebased"
	} else {
		result.Status = "up_to_date"
	}
	return result, nil
}

func resolveRebaseTarget(ctx context.Context, store state.Store, repoID int64, submissionID int64, branch string, worktreeOverride string, currentWorktree string) (string, string, string, error) {
	if submissionID != 0 {
		submission, err := store.GetIntegrationSubmission(ctx, submissionID)
		if err != nil {
			return "", "", "", err
		}
		if submission.RepoID != repoID {
			return "", "", "", fmt.Errorf("submission %d does not belong to this repository", submissionID)
		}
		if submission.BranchName == "" {
			return "", "", "", fmt.Errorf("submission %d is not attached to a branch", submissionID)
		}
		return submission.BranchName, submission.SourceWorktree, fmt.Sprintf("mq retry --submission %d --repo %s", submission.ID, currentWorktree), nil
	}
	if worktreeOverride != "" {
		return branch, worktreeOverride, "", nil
	}
	engine := git.NewEngine(currentWorktree)
	if worktree, err := engine.ResolveWorktree(currentWorktree); err == nil && worktree.Branch == branch {
		return branch, currentWorktree, "", nil
	}
	worktrees, err := engine.ListWorktrees()
	if err != nil {
		return "", "", "", err
	}
	for _, wt := range worktrees {
		if wt.Branch == branch {
			return branch, wt.Path, "", nil
		}
	}
	return "", "", "", fmt.Errorf("could not find a worktree for branch %q; pass --worktree explicitly", branch)
}

func stringsJoin(items []string) string {
	if len(items) == 0 {
		return ""
	}
	return strings.Join(items, ", ")
}
