package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/policy"
	"github.com/recallnet/mainline/internal/state"
)

type repoShowResult struct {
	RepositoryRoot string           `json:"repository_root"`
	ConfigPresent  bool             `json:"config_present"`
	ConfigPath     string           `json:"config_path"`
	Config         policy.File      `json:"config"`
	Worktrees      []git.Worktree   `json:"worktrees"`
	Branch         string           `json:"branch"`
	BranchStatus   git.BranchStatus `json:"branch_status"`
}

func handleCommand(command string, args []string, stdout io.Writer, stderr io.Writer) error {
	switch command {
	case "submit":
		return runSubmit(args, stdout, stderr)
	case "run-once":
		return runRunOnce(args, stdout, stderr)
	case "publish":
		return runPublish(args, stdout, stderr)
	case "repo init":
		return runRepoInit(args, stdout, stderr)
	case "repo show":
		return runRepoShow(args, stdout, stderr)
	case "doctor":
		return runDoctor(args, stdout, stderr)
	default:
		return runPlaceholderCommand(command, args, stdout)
	}
}

func runPlaceholderCommand(command string, args []string, stdout io.Writer) error {
	wiring := bootstrap()
	fmt.Fprintf(stdout, "%s is not implemented yet.\n", command)
	if len(args) > 0 {
		fmt.Fprintf(stdout, "Received %d trailing argument(s) for future subcommand handling.\n", len(args))
	}
	fmt.Fprintf(stdout, "Protected branch default: %s\n", wiring.Policy.Repo.ProtectedBranch)
	fmt.Fprintf(stdout, "Repository root: %s\n", wiring.Git.RepositoryRoot)
	return nil
}

func runRepoInit(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet("mainline repo init", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var repoPath string
	var protectedBranch string
	var remote string
	var mainWorktree string

	fs.StringVar(&repoPath, "repo", ".", "repository path")
	fs.StringVar(&protectedBranch, "protected-branch", "", "protected branch name")
	fs.StringVar(&remote, "remote", "", "default remote name")
	fs.StringVar(&mainWorktree, "main-worktree", "", "canonical protected-branch worktree path")

	if err := fs.Parse(args); err != nil {
		return err
	}

	layout, err := git.DiscoverRepositoryLayout(repoPath)
	if err != nil {
		return err
	}
	repoRoot := layout.RepositoryRoot

	engine := git.NewEngine(layout.WorktreeRoot)
	currentBranch, err := engine.CurrentBranch()
	if err != nil {
		return err
	}

	cfg := policy.DefaultFile()
	if protectedBranch == "" {
		protectedBranch = currentBranch
	}
	if remote == "" {
		remote = cfg.Repo.RemoteName
	}
	if mainWorktree == "" {
		mainWorktree = layout.WorktreeRoot
	}

	cfg.Repo.ProtectedBranch = protectedBranch
	cfg.Repo.RemoteName = remote
	cfg.Repo.MainWorktree = filepath.Clean(mainWorktree)

	if err := policy.SaveFile(repoRoot, cfg); err != nil {
		return err
	}

	store := state.NewStore(state.DefaultPath(layout.GitDir))
	ctx := context.Background()
	if err := store.EnsureSchema(ctx); err != nil {
		return err
	}

	record, err := store.UpsertRepository(ctx, state.RepositoryRecord{
		CanonicalPath:   repoRoot,
		ProtectedBranch: cfg.Repo.ProtectedBranch,
		RemoteName:      cfg.Repo.RemoteName,
		MainWorktree:    cfg.Repo.MainWorktree,
		PolicyVersion:   "v1",
	})
	if err != nil {
		return err
	}

	payload, err := json.Marshal(map[string]string{
		"protected_branch": cfg.Repo.ProtectedBranch,
		"main_worktree":    cfg.Repo.MainWorktree,
	})
	if err != nil {
		return err
	}
	if _, err := store.AppendEvent(ctx, state.EventRecord{
		RepoID:    record.ID,
		ItemType:  "repository",
		EventType: "repository.initialized",
		Payload:   payload,
	}); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "Initialized %s\n", policy.ConfigPath(repoRoot))
	fmt.Fprintf(stdout, "Protected branch: %s\n", cfg.Repo.ProtectedBranch)
	fmt.Fprintf(stdout, "Main worktree: %s\n", cfg.Repo.MainWorktree)
	fmt.Fprintf(stdout, "State path: %s\n", state.DefaultPath(layout.GitDir))
	return nil
}

func runRepoShow(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet("mainline repo show", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var repoPath string
	var asJSON bool

	fs.StringVar(&repoPath, "repo", ".", "repository path")
	fs.BoolVar(&asJSON, "json", false, "output json")

	if err := fs.Parse(args); err != nil {
		return err
	}

	layout, err := git.DiscoverRepositoryLayout(repoPath)
	if err != nil {
		return err
	}
	repoRoot := layout.RepositoryRoot

	cfg, present, err := policy.LoadOrDefault(repoRoot)
	if err != nil {
		return err
	}

	if cfg.Repo.MainWorktree == "" {
		cfg.Repo.MainWorktree = layout.WorktreeRoot
	}

	store := state.NewStore(state.DefaultPath(layout.GitDir))
	ctx := context.Background()
	var record state.RepositoryRecord
	if store.Exists() {
		if found, err := store.GetRepositoryByPath(ctx, repoRoot); err == nil {
			record = found
		}
	}

	engine := git.NewEngine(layout.WorktreeRoot)
	branch, err := engine.CurrentBranch()
	if err != nil {
		return err
	}

	worktrees, err := engine.ListWorktrees()
	if err != nil {
		return err
	}

	branchStatus, err := engine.BranchStatus(cfg.Repo.ProtectedBranch, cfg.Repo.ProtectedBranch)
	if err != nil && !errors.Is(err, flag.ErrHelp) {
		if !engine.BranchExists(cfg.Repo.ProtectedBranch) {
			branchStatus = git.BranchStatus{Name: cfg.Repo.ProtectedBranch}
		} else {
			return err
		}
	}

	result := repoShowResult{
		RepositoryRoot: repoRoot,
		ConfigPresent:  present,
		ConfigPath:     policy.ConfigPath(repoRoot),
		Config:         cfg,
		Worktrees:      worktrees,
		Branch:         branch,
		BranchStatus:   branchStatus,
	}

	if asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	fmt.Fprintf(stdout, "Repository root: %s\n", result.RepositoryRoot)
	fmt.Fprintf(stdout, "Config path: %s\n", result.ConfigPath)
	fmt.Fprintf(stdout, "Config present: %t\n", result.ConfigPresent)
	fmt.Fprintf(stdout, "Current branch: %s\n", result.Branch)
	fmt.Fprintf(stdout, "Protected branch: %s\n", result.Config.Repo.ProtectedBranch)
	fmt.Fprintf(stdout, "Main worktree: %s\n", result.Config.Repo.MainWorktree)
	fmt.Fprintf(stdout, "Remote: %s\n", result.Config.Repo.RemoteName)
	fmt.Fprintf(stdout, "Worktrees: %d\n", len(result.Worktrees))
	if record.ID != 0 {
		fmt.Fprintf(stdout, "State path: %s\n", state.DefaultPath(layout.GitDir))
	}
	if result.BranchStatus.HasUpstream {
		fmt.Fprintf(stdout, "Protected upstream: %s (ahead %d, behind %d)\n", result.BranchStatus.Upstream, result.BranchStatus.AheadCount, result.BranchStatus.BehindCount)
	} else {
		fmt.Fprintln(stdout, "Protected upstream: none")
	}
	return nil
}

func runDoctor(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet("mainline doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var repoPath string
	var asJSON bool

	fs.StringVar(&repoPath, "repo", ".", "repository path")
	fs.BoolVar(&asJSON, "json", false, "output json")

	if err := fs.Parse(args); err != nil {
		return err
	}

	layout, err := git.DiscoverRepositoryLayout(repoPath)
	if err != nil {
		return err
	}
	repoRoot := layout.RepositoryRoot

	cfg, _, err := policy.LoadOrDefault(repoRoot)
	if err != nil {
		return err
	}

	if cfg.Repo.MainWorktree == "" {
		cfg.Repo.MainWorktree = layout.WorktreeRoot
	}

	report, err := git.NewEngine(layout.WorktreeRoot).InspectHealth(cfg.Repo.ProtectedBranch, cfg.Repo.MainWorktree)
	if err != nil {
		return err
	}

	store := state.NewStore(state.DefaultPath(layout.GitDir))
	ctx := context.Background()
	if store.Exists() {
		if record, err := store.GetRepositoryByPath(ctx, repoRoot); err == nil {
			count, err := store.CountUnfinishedItems(ctx, record.ID)
			if err != nil {
				return err
			}
			report.UnfinishedQueueItems = make([]string, count)
		}
	}

	lockManager := state.NewLockManager(repoRoot, layout.GitDir)
	staleLocks, err := lockManager.InspectStale(time.Hour)
	if err != nil {
		return err
	}
	for _, stale := range staleLocks {
		report.StaleLocks = append(report.StaleLocks, stale.Domain+":"+stale.Owner)
	}

	if asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}

	fmt.Fprintf(stdout, "Repository root: %s\n", report.RepositoryRoot)
	fmt.Fprintf(stdout, "Protected branch: %s\n", report.ProtectedBranch)
	fmt.Fprintf(stdout, "Main worktree: %s\n", report.MainWorktreePath)
	fmt.Fprintf(stdout, "Git repository: %s\n", yesNo(report.IsGitRepository))
	fmt.Fprintf(stdout, "Protected branch exists: %s\n", yesNo(report.ProtectedBranchExists))
	fmt.Fprintf(stdout, "Main worktree exists: %s\n", yesNo(report.MainWorktreeExists))
	fmt.Fprintf(stdout, "Protected branch clean: %s\n", yesNo(report.ProtectedBranchClean))
	if report.HasUpstream {
		fmt.Fprintf(stdout, "Upstream: %s\n", report.UpstreamRef)
		fmt.Fprintf(stdout, "Behind upstream: %s\n", yesNo(report.IsBehindUpstream))
		fmt.Fprintf(stdout, "Ahead of upstream: %s\n", yesNo(report.IsAheadOfUpstream))
		fmt.Fprintf(stdout, "Diverged from upstream: %s\n", yesNo(report.HasDivergedUpstream))
	} else {
		fmt.Fprintln(stdout, "Upstream: none")
	}
	fmt.Fprintf(stdout, "Stale locks: %d\n", len(report.StaleLocks))
	fmt.Fprintf(stdout, "Unfinished queue items: %d\n", len(report.UnfinishedQueueItems))
	return nil
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
