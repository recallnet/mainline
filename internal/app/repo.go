package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
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
	RootCheckout   rootCheckoutInfo `json:"root_checkout,omitempty"`
	Warnings       []string         `json:"warnings,omitempty"`
}

type repoRootResult struct {
	RepositoryRoot     string           `json:"repository_root"`
	ConfigPath         string           `json:"config_path"`
	ProtectedBranch    string           `json:"protected_branch"`
	MainWorktree       string           `json:"main_worktree"`
	RootCheckout       rootCheckoutInfo `json:"root_checkout"`
	Trustworthy        bool             `json:"trustworthy"`
	CanAdoptRoot       bool             `json:"can_adopt_root"`
	RecommendedActions []string         `json:"recommended_actions,omitempty"`
	Warnings           []string         `json:"warnings,omitempty"`
	FixesApplied       []string         `json:"fixes_applied,omitempty"`
}

type unmergedBranch struct {
	Branch       string `json:"branch"`
	HeadSHA      string `json:"head_sha"`
	WorktreePath string `json:"worktree_path,omitempty"`
	IsCurrent    bool   `json:"is_current,omitempty"`
}

type repoAuditResult struct {
	RepositoryRoot  string           `json:"repository_root"`
	ProtectedBranch string           `json:"protected_branch"`
	ProtectedSHA    string           `json:"protected_sha,omitempty"`
	Unmerged        []unmergedBranch `json:"unmerged"`
}

type rootCheckoutInfo struct {
	Path        string `json:"path,omitempty"`
	Exists      bool   `json:"exists"`
	Branch      string `json:"branch,omitempty"`
	Clean       bool   `json:"clean"`
	IsCanonical bool   `json:"is_canonical"`
}

func handleCommand(command string, args []string, stdout io.Writer, stderr io.Writer) error {
	switch command {
	case "land":
		return runLand(args, stdout, stderr)
	case "submit":
		return runSubmit(args, stdout, stderr)
	case "status":
		return runStatus(args, stdout, stderr)
	case "confidence":
		return runConfidence(args, stdout, stderr)
	case "run-once":
		return runRunOnce(args, stdout, stderr)
	case "wait":
		return runWait(args, stdout, stderr)
	case "retry":
		return runRetry(args, stdout, stderr)
	case "cancel":
		return runCancel(args, stdout, stderr)
	case "publish":
		return runPublish(args, stdout, stderr)
	case "logs":
		return runLogs(args, stdout, stderr)
	case "watch":
		return runWatch(args, stdout, stderr)
	case "events":
		return runEvents(args, stdout, stderr)
	case "completion":
		return runCompletion(args, stdout, stderr)
	case "config edit":
		return runConfigEdit(args, stdout, stderr)
	case "repo audit":
		return runRepoAudit(args, stdout, stderr)
	case "repo init":
		return runRepoInit(args, stdout, stderr)
	case "repo root":
		return runRepoRoot(args, stdout, stderr)
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
	fs := flag.NewFlagSet(currentCLIProgramName()+" repo init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	setFlagUsage(fs, fmt.Sprintf(`Usage:
  %s repo init [flags]

Initialize durable mq state for the current repo and scaffold mainline.toml.

Recommended first commit:
  git add mainline.toml
  git commit -m "Initialize mainline repo policy"

Then install hooks:
  ./scripts/install-hooks.sh

Flags:
`, currentCLIProgramName()))

	var repoPath string
	var protectedBranch string
	var remote string
	var mainWorktree string
	var asJSON bool

	fs.StringVar(&repoPath, "repo", ".", "repository path")
	fs.StringVar(&protectedBranch, "protected-branch", "", "protected branch name")
	fs.StringVar(&remote, "remote", "", "default remote name")
	fs.StringVar(&mainWorktree, "main-worktree", "", "canonical protected-branch worktree path")
	fs.BoolVar(&asJSON, "json", false, "output json")

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
		mainWorktree = defaultMainWorktree(layout)
	}

	cfg.Repo.ProtectedBranch = protectedBranch
	cfg.Repo.RemoteName = remote
	cfg.Repo.MainWorktree = canonicalRegistryPath(mainWorktree)

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
	if err := registerRepo(cfg.Repo.MainWorktree, repoRoot, state.DefaultPath(layout.GitDir)); err != nil {
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

	if asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(map[string]any{
			"ok":                         true,
			"config_path":                policy.ConfigPath(repoRoot),
			"protected_branch":           cfg.Repo.ProtectedBranch,
			"main_worktree":              cfg.Repo.MainWorktree,
			"state_path":                 state.DefaultPath(layout.GitDir),
			"repository_root":            repoRoot,
			"global_registry_path":       mustGlobalRegistryPath(),
			"recommended_commit_message": "Initialize mainline repo policy",
			"next_steps": []string{
				"git add mainline.toml",
				"git commit -m \"Initialize mainline repo policy\"",
				"./scripts/install-hooks.sh",
				"./scripts/install-launch-agent.sh",
				"mq submit --check-only --json",
				"mq submit --wait --timeout 15m --json",
			},
		})
	}

	fmt.Fprintf(stdout, "Initialized %s\n", policy.ConfigPath(repoRoot))
	fmt.Fprintf(stdout, "Protected branch: %s\n", cfg.Repo.ProtectedBranch)
	fmt.Fprintf(stdout, "Main worktree: %s\n", cfg.Repo.MainWorktree)
	fmt.Fprintf(stdout, "State path: %s\n", state.DefaultPath(layout.GitDir))
	fmt.Fprintf(stdout, "Global registry: %s\n", mustGlobalRegistryPath())
	fmt.Fprintln(stdout, "Next:")
	fmt.Fprintln(stdout, "  git add mainline.toml")
	fmt.Fprintln(stdout, "  git commit -m \"Initialize mainline repo policy\"")
	fmt.Fprintln(stdout, "  ./scripts/install-hooks.sh")
	fmt.Fprintln(stdout, "  ./scripts/install-launch-agent.sh")
	fmt.Fprintln(stdout, "  mq submit --check-only --json")
	fmt.Fprintln(stdout, "  mq submit --wait --timeout 15m --json")
	return nil
}

func runRepoShow(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet(currentCLIProgramName()+" repo show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	setFlagUsage(fs, fmt.Sprintf(`Usage:
  %s repo show [flags]

Show the stored repo config, protected-branch status, and discovered worktrees.

Examples:
  mq repo show --repo /path/to/protected-main
  mq repo show --json

Flags:
`, currentCLIProgramName()))

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
		if found, _, err := ensureRepositoryRecord(ctx, store, repoRoot, cfg); err == nil {
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
	rootInfo, warnings := inspectCanonicalRootCheckout(cfg, layout)
	result.RootCheckout = rootInfo
	result.Warnings = warnings

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
	if result.RootCheckout.Path != "" {
		fmt.Fprintf(stdout, "Root checkout: %s\n", result.RootCheckout.Path)
		fmt.Fprintf(stdout, "Root checkout canonical: %s\n", yesNo(result.RootCheckout.IsCanonical))
		if result.RootCheckout.Branch != "" {
			fmt.Fprintf(stdout, "Root checkout branch: %s\n", result.RootCheckout.Branch)
		}
		fmt.Fprintf(stdout, "Root checkout clean: %s\n", yesNo(result.RootCheckout.Clean))
	}
	if record.ID != 0 {
		fmt.Fprintf(stdout, "State path: %s\n", state.DefaultPath(layout.GitDir))
	}
	if result.BranchStatus.HasUpstream {
		fmt.Fprintf(stdout, "Protected upstream: %s (ahead %d, behind %d)\n", result.BranchStatus.Upstream, result.BranchStatus.AheadCount, result.BranchStatus.BehindCount)
	} else {
		fmt.Fprintln(stdout, "Protected upstream: none")
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(stdout, "Warning: %s\n", warning)
	}
	return nil
}

func runRepoRoot(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet(currentCLIProgramName()+" repo root", flag.ContinueOnError)
	fs.SetOutput(stderr)
	setFlagUsage(fs, fmt.Sprintf(`Usage:
  %s repo root [flags]

Inspect whether the repository root checkout is trustworthy as the canonical
protected main, and optionally adopt it as the configured main worktree.

Examples:
  mq repo root --repo .
  mq repo root --repo . --json
  mq repo root --repo . --adopt-root --json

Flags:
`, currentCLIProgramName()))

	var repoPath string
	var asJSON bool
	var adoptRoot bool

	fs.StringVar(&repoPath, "repo", ".", "repository path")
	fs.BoolVar(&asJSON, "json", false, "output json")
	fs.BoolVar(&adoptRoot, "adopt-root", false, "set the repository root checkout as the canonical main worktree when it is already clean and on the protected branch")

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

	fixesApplied := []string{}
	if adoptRoot {
		if err := adoptRepositoryRoot(repoRoot, layout, &cfg, present); err != nil {
			return err
		}
		fixesApplied = append(fixesApplied, fmt.Sprintf("set canonical main worktree to repository root %s", filepath.Clean(layout.RepositoryRoot)))
	}

	result := buildRepoRootResult(repoRoot, cfg, layout)
	result.FixesApplied = fixesApplied

	if asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	fmt.Fprintf(stdout, "Repository root: %s\n", result.RepositoryRoot)
	fmt.Fprintf(stdout, "Config path: %s\n", result.ConfigPath)
	fmt.Fprintf(stdout, "Protected branch: %s\n", result.ProtectedBranch)
	fmt.Fprintf(stdout, "Main worktree: %s\n", result.MainWorktree)
	fmt.Fprintf(stdout, "Root checkout: %s\n", result.RootCheckout.Path)
	fmt.Fprintf(stdout, "Root checkout canonical: %s\n", yesNo(result.RootCheckout.IsCanonical))
	if result.RootCheckout.Branch != "" {
		fmt.Fprintf(stdout, "Root checkout branch: %s\n", result.RootCheckout.Branch)
	}
	fmt.Fprintf(stdout, "Root checkout clean: %s\n", yesNo(result.RootCheckout.Clean))
	fmt.Fprintf(stdout, "Root trustworthy: %s\n", yesNo(result.Trustworthy))
	fmt.Fprintf(stdout, "Can adopt root: %s\n", yesNo(result.CanAdoptRoot))
	for _, warning := range result.Warnings {
		fmt.Fprintf(stdout, "Warning: %s\n", warning)
	}
	for _, action := range result.RecommendedActions {
		fmt.Fprintf(stdout, "Next: %s\n", action)
	}
	for _, applied := range result.FixesApplied {
		fmt.Fprintf(stdout, "Fixed: %s\n", applied)
	}
	return nil
}

func adoptRepositoryRoot(repoRoot string, layout git.RepositoryLayout, cfg *policy.File, configPresent bool) error {
	if !configPresent {
		return fmt.Errorf("repository is not initialized; run `mq repo init --repo %s` before adopting the root checkout", repoRoot)
	}

	store := state.NewStore(state.DefaultPath(layout.GitDir))
	if !store.Exists() {
		return fmt.Errorf("repository is not initialized; run `mq repo init --repo %s` before adopting the root checkout", repoRoot)
	}

	result := buildRepoRootResult(repoRoot, *cfg, layout)
	if !result.CanAdoptRoot {
		return fmt.Errorf("cannot adopt repository root as canonical main worktree yet; run `mq repo root --repo %s --json` and complete the recommended actions first", repoRoot)
	}

	cfg.Repo.MainWorktree = canonicalRegistryPath(layout.RepositoryRoot)
	if err := policy.SaveFile(repoRoot, *cfg); err != nil {
		return err
	}

	ctx := context.Background()
	if err := store.EnsureSchema(ctx); err != nil {
		return err
	}
	if _, _, err := ensureRepositoryRecord(ctx, store, repoRoot, *cfg); err != nil {
		return err
	}
	if _, err := store.UpsertRepository(ctx, state.RepositoryRecord{
		CanonicalPath:   repoRoot,
		ProtectedBranch: cfg.Repo.ProtectedBranch,
		RemoteName:      cfg.Repo.RemoteName,
		MainWorktree:    cfg.Repo.MainWorktree,
		PolicyVersion:   "v1",
	}); err != nil {
		return err
	}
	return registerRepo(cfg.Repo.MainWorktree, repoRoot, state.DefaultPath(layout.GitDir))
}

func buildRepoRootResult(repoRoot string, cfg policy.File, layout git.RepositoryLayout) repoRootResult {
	rootInfo, warnings := inspectCanonicalRootCheckout(cfg, layout)
	recommended := recommendedRootActions(repoRoot, cfg, rootInfo)
	canAdopt := canAdoptRootCheckout(cfg, rootInfo)
	return repoRootResult{
		RepositoryRoot:     repoRoot,
		ConfigPath:         policy.ConfigPath(repoRoot),
		ProtectedBranch:    cfg.Repo.ProtectedBranch,
		MainWorktree:       cfg.Repo.MainWorktree,
		RootCheckout:       rootInfo,
		Trustworthy:        rootInfo.Exists && rootInfo.IsCanonical && rootInfo.Clean && rootInfo.Branch == cfg.Repo.ProtectedBranch,
		CanAdoptRoot:       canAdopt,
		RecommendedActions: recommended,
		Warnings:           warnings,
	}
}

func canAdoptRootCheckout(cfg policy.File, rootInfo rootCheckoutInfo) bool {
	return rootInfo.Exists && !rootInfo.IsCanonical && rootInfo.Clean && rootInfo.Branch == cfg.Repo.ProtectedBranch
}

func recommendedRootActions(repoRoot string, cfg policy.File, rootInfo rootCheckoutInfo) []string {
	var actions []string
	if rootInfo.Path == "" {
		return actions
	}
	if !rootInfo.Exists {
		actions = append(actions, fmt.Sprintf("restore the repository root checkout at %s before trusting local docs or wrappers", rootInfo.Path))
		return actions
	}
	if rootInfo.Branch != "" && rootInfo.Branch != cfg.Repo.ProtectedBranch {
		actions = append(actions, fmt.Sprintf("checkout %s in %s", cfg.Repo.ProtectedBranch, rootInfo.Path))
	}
	if !rootInfo.Clean {
		actions = append(actions, fmt.Sprintf("commit, stash, or discard local changes in %s so the root checkout matches shipped main", rootInfo.Path))
	}
	if canAdoptRootCheckout(cfg, rootInfo) {
		actions = append(actions, fmt.Sprintf("run `mq repo root --repo %s --adopt-root` to make the repository root the canonical protected main", repoRoot))
	}
	if rootInfo.IsCanonical && rootInfo.Clean && rootInfo.Branch == cfg.Repo.ProtectedBranch {
		actions = append(actions, "no action needed; the repository root checkout is trustworthy")
	}
	return actions
}

func runRepoAudit(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet(currentCLIProgramName()+" repo audit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	setFlagUsage(fs, fmt.Sprintf(`Usage:
  %s repo audit [flags]

List local branches and worktree refs not yet merged into the protected branch.

Examples:
  mq repo audit --repo /path/to/protected-main
  mq repo audit --json

Flags:
`, currentCLIProgramName()))

	var repoPath string
	var asJSON bool

	fs.StringVar(&repoPath, "repo", ".", "repository path")
	fs.BoolVar(&asJSON, "json", false, "output json")

	if err := fs.Parse(args); err != nil {
		return err
	}

	result, err := collectRepoAudit(repoPath)
	if err != nil {
		return err
	}

	if asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	fmt.Fprintf(stdout, "Repository root: %s\n", result.RepositoryRoot)
	fmt.Fprintf(stdout, "Protected branch: %s\n", result.ProtectedBranch)
	if result.ProtectedSHA != "" {
		fmt.Fprintf(stdout, "Protected SHA: %s\n", result.ProtectedSHA)
	}
	if len(result.Unmerged) == 0 {
		fmt.Fprintln(stdout, "Unmerged branches: none")
		return nil
	}

	fmt.Fprintln(stdout, "Unmerged branches:")
	for _, branch := range result.Unmerged {
		if branch.WorktreePath != "" {
			fmt.Fprintf(stdout, "  %s %s (%s)\n", branch.Branch, branch.HeadSHA, branch.WorktreePath)
			continue
		}
		fmt.Fprintf(stdout, "  %s %s\n", branch.Branch, branch.HeadSHA)
	}
	return nil
}

func collectRepoAudit(repoPath string) (repoAuditResult, error) {
	layout, repoRoot, cfg, _, _, err := loadRepoContext(repoPath)
	if err != nil {
		return repoAuditResult{}, err
	}

	engine := git.NewEngine(layout.WorktreeRoot)
	protectedSHA, err := engine.BranchHeadSHA(cfg.Repo.ProtectedBranch)
	if err != nil && engine.BranchExists(cfg.Repo.ProtectedBranch) {
		return repoAuditResult{}, err
	}

	worktrees, err := engine.ListWorktrees()
	if err != nil {
		return repoAuditResult{}, err
	}

	attachedWorktree := make(map[string]git.Worktree, len(worktrees))
	candidates := map[string]unmergedBranch{}
	for _, wt := range worktrees {
		if wt.Branch == "" || wt.IsDetached {
			continue
		}
		attachedWorktree[wt.Branch] = wt
		if wt.Branch == cfg.Repo.ProtectedBranch {
			continue
		}
		merged, err := engine.IsAncestor(wt.Branch, cfg.Repo.ProtectedBranch)
		if err != nil {
			return repoAuditResult{}, err
		}
		if merged {
			continue
		}
		candidates[wt.Branch] = unmergedBranch{
			Branch:       wt.Branch,
			HeadSHA:      wt.HeadSHA,
			WorktreePath: wt.Path,
			IsCurrent:    wt.IsCurrent,
		}
	}

	output, err := captureGit(layout.WorktreeRoot, "branch", "--format=%(refname:short)")
	if err != nil {
		return repoAuditResult{}, err
	}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		branch := strings.TrimSpace(line)
		if branch == "" || branch == cfg.Repo.ProtectedBranch {
			continue
		}
		if _, ok := candidates[branch]; ok {
			continue
		}
		merged, err := engine.IsAncestor(branch, cfg.Repo.ProtectedBranch)
		if err != nil {
			return repoAuditResult{}, err
		}
		if merged {
			continue
		}
		headSHA, err := engine.BranchHeadSHA(branch)
		if err != nil {
			return repoAuditResult{}, err
		}
		entry := unmergedBranch{
			Branch:  branch,
			HeadSHA: headSHA,
		}
		if wt, ok := attachedWorktree[branch]; ok {
			entry.WorktreePath = wt.Path
			entry.IsCurrent = wt.IsCurrent
		}
		candidates[branch] = entry
	}

	unmerged := make([]unmergedBranch, 0, len(candidates))
	for _, entry := range candidates {
		unmerged = append(unmerged, entry)
	}
	sort.Slice(unmerged, func(i, j int) bool {
		return unmerged[i].Branch < unmerged[j].Branch
	})

	return repoAuditResult{
		RepositoryRoot:  repoRoot,
		ProtectedBranch: cfg.Repo.ProtectedBranch,
		ProtectedSHA:    protectedSHA,
		Unmerged:        unmerged,
	}, nil
}

func captureGit(worktreePath string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = filepath.Clean(worktreePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func runDoctor(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet(currentCLIProgramName()+" doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	setFlagUsage(fs, fmt.Sprintf(`Usage:
  %s doctor [flags]

Inspect repo health and optionally apply safe automatic recovery steps.

Examples:
  mq doctor --repo /path/to/protected-main
  mq doctor --repo /path/to/protected-main --fix --json

Flags:
`, currentCLIProgramName()))

	var repoPath string
	var asJSON bool
	var fix bool

	fs.StringVar(&repoPath, "repo", ".", "repository path")
	fs.BoolVar(&asJSON, "json", false, "output json")
	fs.BoolVar(&fix, "fix", false, "apply safe automatic fixes")

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

	engine := git.NewEngine(layout.WorktreeRoot)
	report, err := engine.InspectHealth(cfg.Repo.ProtectedBranch, cfg.Repo.MainWorktree)
	if err != nil {
		return err
	}
	rootInfo, rootWarnings := inspectCanonicalRootCheckout(cfg, layout)
	report.Warnings = append(report.Warnings, rootWarnings...)

	if cfg.Repo.WorktreeLayoutPolicy == "enforce-prefix" && cfg.Repo.WorktreeRootPrefix != "" {
		engine := git.NewEngine(layout.WorktreeRoot)
		worktrees, err := engine.ListWorktrees()
		if err != nil {
			return err
		}
		prefixPath, err := filepath.EvalSymlinks(filepath.Clean(cfg.Repo.WorktreeRootPrefix))
		if err != nil {
			prefixPath = filepath.Clean(cfg.Repo.WorktreeRootPrefix)
		}
		prefix := filepath.Clean(prefixPath) + string(filepath.Separator)
		mainWorktree, err := filepath.EvalSymlinks(filepath.Clean(cfg.Repo.MainWorktree))
		if err != nil {
			mainWorktree = filepath.Clean(cfg.Repo.MainWorktree)
		}
		for _, wt := range worktrees {
			cleanPath := wt.Path
			if cleanPath == mainWorktree {
				continue
			}
			if !strings.HasPrefix(cleanPath+string(filepath.Separator), prefix) {
				report.Warnings = append(report.Warnings, "worktree outside policy prefix: "+cleanPath)
			}
		}
	}

	store := state.NewStore(state.DefaultPath(layout.GitDir))
	ctx := context.Background()
	var repoRecord state.RepositoryRecord
	var hasRepoRecord bool
	if store.Exists() {
		if record, _, err := ensureRepositoryRecord(ctx, store, repoRoot, cfg); err == nil {
			repoRecord = record
			hasRepoRecord = true
			count, err := store.CountUnfinishedItems(ctx, record.ID)
			if err != nil {
				return err
			}
			report.UnfinishedQueueItems = make([]string, count)
		}
	}

	lockManager := state.NewLockManager(repoRoot, layout.GitDir)
	result := doctorResult{HealthReport: report, RootCheckout: rootInfo}
	if fix {
		applied, skipped, err := runDoctorFix(ctx, engine, cfg, lockManager, store, repoRecord, hasRepoRecord)
		if err != nil {
			return err
		}
		result.FixesApplied = applied
		result.FixesSkipped = skipped
		report, err = engine.InspectHealth(cfg.Repo.ProtectedBranch, cfg.Repo.MainWorktree)
		if err != nil {
			return err
		}
		rootInfo, rootWarnings = inspectCanonicalRootCheckout(cfg, layout)
		report.Warnings = append(report.Warnings, rootWarnings...)
		result.HealthReport = report
		result.RootCheckout = rootInfo
		if store.Exists() && hasRepoRecord {
			count, err := store.CountUnfinishedItems(ctx, repoRecord.ID)
			if err != nil {
				return err
			}
			result.UnfinishedQueueItems = make([]string, count)
		}
	}
	staleLocks, err := lockManager.InspectStale(time.Hour)
	if err != nil {
		return err
	}
	result.StaleLocks = result.StaleLocks[:0]
	for _, stale := range staleLocks {
		result.StaleLocks = append(result.StaleLocks, stale.Domain+":"+stale.Owner)
	}

	if asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	fmt.Fprintf(stdout, "Repository root: %s\n", result.RepositoryRoot)
	fmt.Fprintf(stdout, "Protected branch: %s\n", result.ProtectedBranch)
	fmt.Fprintf(stdout, "Main worktree: %s\n", result.MainWorktreePath)
	if result.RootCheckout.Path != "" {
		fmt.Fprintf(stdout, "Root checkout: %s\n", result.RootCheckout.Path)
		fmt.Fprintf(stdout, "Root checkout canonical: %s\n", yesNo(result.RootCheckout.IsCanonical))
		if result.RootCheckout.Branch != "" {
			fmt.Fprintf(stdout, "Root checkout branch: %s\n", result.RootCheckout.Branch)
		}
		fmt.Fprintf(stdout, "Root checkout clean: %s\n", yesNo(result.RootCheckout.Clean))
	}
	fmt.Fprintf(stdout, "Git repository: %s\n", yesNo(result.IsGitRepository))
	fmt.Fprintf(stdout, "Protected branch exists: %s\n", yesNo(result.ProtectedBranchExists))
	fmt.Fprintf(stdout, "Main worktree exists: %s\n", yesNo(result.MainWorktreeExists))
	fmt.Fprintf(stdout, "Protected branch clean: %s\n", yesNo(result.ProtectedBranchClean))
	if result.HasUpstream {
		fmt.Fprintf(stdout, "Upstream: %s\n", result.UpstreamRef)
		fmt.Fprintf(stdout, "Behind upstream: %s\n", yesNo(result.IsBehindUpstream))
		fmt.Fprintf(stdout, "Ahead of upstream: %s\n", yesNo(result.IsAheadOfUpstream))
		fmt.Fprintf(stdout, "Diverged from upstream: %s\n", yesNo(result.HasDivergedUpstream))
	} else {
		fmt.Fprintln(stdout, "Upstream: none")
	}
	fmt.Fprintf(stdout, "Stale locks: %d\n", len(result.StaleLocks))
	for _, stale := range result.StaleLocks {
		fmt.Fprintf(stdout, "Stale lock: %s\n", stale)
	}
	fmt.Fprintf(stdout, "Unfinished queue items: %d\n", len(result.UnfinishedQueueItems))
	fmt.Fprintf(stdout, "Warnings: %d\n", len(result.Warnings))
	for _, warning := range result.Warnings {
		fmt.Fprintf(stdout, "Warning: %s\n", warning)
	}
	if fix {
		fmt.Fprintf(stdout, "Fixes applied: %d\n", len(result.FixesApplied))
		for _, applied := range result.FixesApplied {
			fmt.Fprintf(stdout, "Fixed: %s\n", applied)
		}
		fmt.Fprintf(stdout, "Fixes skipped: %d\n", len(result.FixesSkipped))
		for _, skipped := range result.FixesSkipped {
			fmt.Fprintf(stdout, "Skipped: %s\n", skipped)
		}
	}
	return nil
}

type doctorResult struct {
	git.HealthReport
	RootCheckout rootCheckoutInfo `json:"root_checkout,omitempty"`
	FixesApplied []string         `json:"fixes_applied,omitempty"`
	FixesSkipped []string         `json:"fixes_skipped,omitempty"`
}

func defaultMainWorktree(layout git.RepositoryLayout) string {
	if hasGitWorktreeMarker(layout.RepositoryRoot) {
		return layout.RepositoryRoot
	}
	return layout.WorktreeRoot
}

func hasGitWorktreeMarker(path string) bool {
	info, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil && (info.IsDir() || info.Mode().IsRegular())
}

func inspectCanonicalRootCheckout(cfg policy.File, layout git.RepositoryLayout) (rootCheckoutInfo, []string) {
	if !hasGitWorktreeMarker(layout.RepositoryRoot) {
		return rootCheckoutInfo{}, nil
	}

	rootPath := canonicalRegistryPath(layout.RepositoryRoot)
	mainWorktreePath := canonicalRegistryPath(cfg.Repo.MainWorktree)
	info := rootCheckoutInfo{
		Path:        rootPath,
		Exists:      true,
		IsCanonical: mainWorktreePath == rootPath,
	}
	var warnings []string
	if !info.IsCanonical {
		warnings = append(warnings, fmt.Sprintf("configured main worktree %s differs from repository root %s; keep the root checkout as the canonical protected main", mainWorktreePath, rootPath))
	}

	rootEngine := git.NewEngine(rootPath)
	if branch, err := rootEngine.CurrentBranch(); err == nil {
		info.Branch = branch
		if branch != cfg.Repo.ProtectedBranch {
			warnings = append(warnings, fmt.Sprintf("repository root checkout is on %s, expected protected branch %s", branch, cfg.Repo.ProtectedBranch))
		}
	} else {
		warnings = append(warnings, fmt.Sprintf("repository root checkout is not on a branch: %v", err))
	}

	clean, err := rootEngine.WorktreeIsClean(rootPath)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("could not inspect repository root checkout cleanliness: %v", err))
		return info, warnings
	}
	info.Clean = clean
	if !clean {
		warnings = append(warnings, fmt.Sprintf("repository root checkout %s is dirty; humans and wrappers will see stale local drift until it is cleaned", rootPath))
	}

	return info, warnings
}

func runDoctorFix(ctx context.Context, engine git.Engine, cfg policy.File, lockManager state.LockManager, store state.Store, repoRecord state.RepositoryRecord, hasRepoRecord bool) ([]string, []string, error) {
	var applied []string
	var skipped []string
	protectedWorktreeCleanForPublish := false

	staleLocks, err := lockManager.InspectStale(time.Hour)
	if err != nil {
		return nil, nil, err
	}
	for _, stale := range staleLocks {
		lease, err := lockManager.Acquire(stale.Domain, "doctor-fix")
		if err == nil {
			if releaseErr := lease.Release(); releaseErr != nil {
				return nil, nil, releaseErr
			}
			applied = append(applied, fmt.Sprintf("cleared stale %s lock metadata", stale.Domain))
			continue
		}
		if errors.Is(err, state.ErrLockHeld) {
			skipped = append(skipped, fmt.Sprintf("left %s lock in place because it is still actively held", stale.Domain))
			continue
		}
		return nil, nil, err
	}

	if !hasRepoRecord {
		return applied, skipped, nil
	}

	integrationLease, err := lockManager.Acquire(state.IntegrationLock, "doctor-fix")
	if err == nil {
		recovered, recoverErr := recoverInterruptedIntegrationSubmissions(ctx, store, repoRecord.ID)
		releaseErr := integrationLease.Release()
		if recoverErr != nil {
			return nil, nil, recoverErr
		}
		if releaseErr != nil {
			return nil, nil, releaseErr
		}
		if recovered > 0 {
			applied = append(applied, fmt.Sprintf("recovered %d interrupted integration submissions", recovered))
		}
	} else if !errors.Is(err, state.ErrLockHeld) {
		return nil, nil, err
	}

	publishLease, err := lockManager.Acquire(state.PublishLock, "doctor-fix")
	if err == nil {
		recovered, recoverErr := recoverInterruptedPublishRequests(ctx, store, repoRecord.ID)
		releaseErr := publishLease.Release()
		if recoverErr != nil {
			return nil, nil, recoverErr
		}
		if releaseErr != nil {
			return nil, nil, releaseErr
		}
		if recovered > 0 {
			applied = append(applied, fmt.Sprintf("recovered %d interrupted publish requests", recovered))
		}
	} else if !errors.Is(err, state.ErrLockHeld) {
		return nil, nil, err
	}

	queued, err := store.ListIntegrationSubmissionsByStatus(ctx, repoRecord.ID, "queued")
	if err != nil {
		return nil, nil, err
	}
	for _, submission := range queued {
		if submission.RefKind != submissionRefKindBranch {
			continue
		}
		if engine.BranchExists(submission.SourceRef) {
			continue
		}
		if _, err := store.UpdateIntegrationSubmissionStatus(ctx, submission.ID, "failed", fmt.Sprintf("source branch %q no longer exists; resubmit from a live worktree", submission.SourceRef)); err != nil {
			return nil, nil, err
		}
		if err := appendSubmissionEvent(ctx, store, repoRecord.ID, submission.ID, "integration.failed", map[string]string{
			"branch":          submissionDisplayRef(submission),
			"source_ref":      submission.SourceRef,
			"ref_kind":        submission.RefKind,
			"source_sha":      submission.SourceSHA,
			"source_worktree": submission.SourceWorktree,
			"error":           fmt.Sprintf("source branch %q no longer exists; resubmit from a live worktree", submission.SourceRef),
			"reason":          "source_branch_missing",
		}); err != nil {
			return nil, nil, err
		}
		applied = append(applied, fmt.Sprintf("failed queued submission %d because branch %q no longer exists", submission.ID, submission.SourceRef))
	}

	if cfg.Repo.MainWorktree != "" && engine.BranchExists(cfg.Repo.ProtectedBranch) {
		if _, err := os.Stat(cfg.Repo.MainWorktree); err == nil {
			operation, err := engine.InProgressOperation(cfg.Repo.MainWorktree)
			if err != nil {
				return nil, nil, err
			}
			if operation != "" {
				aborted, err := engine.AbortInProgressOperation(cfg.Repo.MainWorktree)
				if err != nil {
					skipped = append(skipped, fmt.Sprintf("could not abort %s on protected branch worktree: %v", operation, err))
				} else if aborted != "" {
					applied = append(applied, fmt.Sprintf("aborted %s on protected branch worktree", aborted))
				}
			} else {
				clean, err := engine.WorktreeIsClean(cfg.Repo.MainWorktree)
				if err != nil {
					return nil, nil, err
				}
				if !clean {
					skipped = append(skipped, "left protected branch worktree dirty because no safe auto-fix was available")
				} else {
					protectedWorktreeCleanForPublish = true
				}
			}
			if operation != "" {
				clean, err := engine.WorktreeIsClean(cfg.Repo.MainWorktree)
				if err != nil {
					return nil, nil, err
				}
				protectedWorktreeCleanForPublish = clean
			}
		}
	}

	if cfg.Repo.RemoteName != "" && cfg.Repo.MainWorktree != "" && protectedWorktreeCleanForPublish {
		if _, err := os.Stat(cfg.Repo.MainWorktree); err == nil {
			if err := engine.FetchRemote(cfg.Repo.MainWorktree, cfg.Repo.RemoteName); err != nil {
				skipped = append(skipped, fmt.Sprintf("could not refresh upstream state: %v", err))
			} else {
				branchStatus, err := engine.BranchStatus(cfg.Repo.ProtectedBranch, cfg.Repo.ProtectedBranch)
				if err != nil {
					return nil, nil, err
				}
				if branchStatus.HasUpstream && branchStatus.AheadCount > 0 && branchStatus.BehindCount == 0 {
					protectedSHA, err := engine.BranchHeadSHA(cfg.Repo.ProtectedBranch)
					if err != nil {
						return nil, nil, err
					}
					request, created, err := ensureLatestPublishRequestRecord(ctx, store, repoRecord.ID, protectedSHA)
					if err != nil {
						return nil, nil, err
					}
					if created {
						applied = append(applied, fmt.Sprintf("queued publish request %d for protected tip %s", request.ID, protectedSHA))
					}
				}
			}
		}
	}
	return applied, skipped, nil
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
