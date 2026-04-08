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

	"github.com/recallnet/mainline/internal/domain"
	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/policy"
	"github.com/recallnet/mainline/internal/state"
)

type repoShowResult struct {
	RepositoryRoot   string                 `json:"repository_root"`
	ConfigPresent    bool                   `json:"config_present"`
	ConfigPath       string                 `json:"config_path"`
	Config           policy.File            `json:"config"`
	PublishExecution publishExecutionPolicy `json:"publish_execution"`
	Worktrees        []git.Worktree         `json:"worktrees"`
	Branch           string                 `json:"branch"`
	BranchStatus     git.BranchStatus       `json:"branch_status"`
	RootCheckout     rootCheckoutInfo       `json:"root_checkout,omitempty"`
	Warnings         []string               `json:"warnings,omitempty"`
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
	Topology    string `json:"topology,omitempty"`
}

func runRepoInit(args []string, stdout *stepPrinter, stderr io.Writer) error {
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
	cfg := policy.DefaultFile()
	defaultProtectedWorktree := defaultMainWorktree(layout)
	if mainWorktree == "" {
		mainWorktree = defaultProtectedWorktree
	} else if !filepath.IsAbs(mainWorktree) {
		mainWorktree = filepath.Join(repoRoot, mainWorktree)
	}
	mainWorktree = canonicalRegistryPath(mainWorktree)

	engine := git.NewEngine(layout.WorktreeRoot)
	currentBranch, err := engine.CurrentBranch()
	if err != nil {
		if protectedBranch == "" && mainWorktree == canonicalRegistryPath(layout.WorktreeRoot) {
			return fmt.Errorf("repo init requires the protected worktree %s to be attached to local branch %q; current checkout is detached HEAD. Run `git checkout --ignore-other-worktrees %s` in %s, then retry `mq repo init --repo %s --protected-branch %s --main-worktree %s`", mainWorktree, cfg.Repo.ProtectedBranch, cfg.Repo.ProtectedBranch, mainWorktree, layout.WorktreeRoot, cfg.Repo.ProtectedBranch, mainWorktree)
		}
		currentBranch = ""
	}

	if protectedBranch == "" {
		if mainWorktree == canonicalRegistryPath(layout.WorktreeRoot) && currentBranch != cfg.Repo.ProtectedBranch {
			return fmt.Errorf("repo init must run from local branch %q or pass --protected-branch explicitly; current branch is %q. For a normal repo, switch %s to %q and rerun `mq repo init --repo %s`. If you intentionally want to protect %q instead, rerun `mq repo init --repo %s --protected-branch %s --main-worktree %s`", cfg.Repo.ProtectedBranch, currentBranch, mainWorktree, cfg.Repo.ProtectedBranch, layout.WorktreeRoot, currentBranch, layout.WorktreeRoot, currentBranch, mainWorktree)
		}
		protectedBranch = cfg.Repo.ProtectedBranch
	}
	if remote == "" {
		remote = cfg.Repo.RemoteName
	}

	cfg.Repo.ProtectedBranch = protectedBranch
	cfg.Repo.RemoteName = remote
	cfg.Repo.MainWorktree = mainWorktree

	configRoot := resolveConfigAuthorityRoot(context.Background(), layout, state.NewStore(state.DefaultPath(layout.GitDir)), mainWorktree)
	if err := saveConfigAuthority(configRoot, cfg); err != nil {
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
		encoder := json.NewEncoder(stdout.Raw())
		encoder.SetIndent("", "  ")
		return encoder.Encode(map[string]any{
			"ok":                         true,
			"config_path":                policy.ConfigPath(configRoot),
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
				"# for agent/factory repos, set [publish].Mode = 'auto' before relying on submit --wait",
				"mq submit --queue-only --json",
				"mq submit --check-only --json",
				"mq submit --wait --timeout 15m --json",
				"mq land --json --timeout 30m",
			},
		})
	}

	printer := stdout
	printer.Section("Initialized %s", policy.ConfigPath(configRoot))
	printer.Line("Protected branch: %s", cfg.Repo.ProtectedBranch)
	printer.Line("Main worktree: %s", cfg.Repo.MainWorktree)
	printer.Line("State path: %s", state.DefaultPath(layout.GitDir))
	printer.Line("Global registry: %s", mustGlobalRegistryPath())
	printer.Section("Next")
	printer.Bullet(
		"git add mainline.toml",
		"git commit -m \"Initialize mainline repo policy\"",
		"./scripts/install-hooks.sh",
		"# for agent/factory repos, set [publish].Mode = 'auto' before relying on submit --wait",
		"mq submit --queue-only --json",
		"mq submit --check-only --json",
		"mq submit --wait --timeout 15m --json",
		"mq land --json --timeout 30m",
	)
	return nil
}

type registryPruneResult struct {
	RegistryPath   string   `json:"registry_path"`
	PrunedCount    int      `json:"pruned_count"`
	RemainingCount int      `json:"remaining_count"`
	PrunedRepos    []string `json:"pruned_repositories,omitempty"`
}

func runRegistryPrune(args []string, stdout *stepPrinter, stderr io.Writer) error {
	fs := flag.NewFlagSet(currentCLIProgramName()+" registry prune", flag.ContinueOnError)
	fs.SetOutput(stderr)
	setFlagUsage(fs, fmt.Sprintf(`Usage:
  %s registry prune [flags]

Remove stale repo entries from the global registry used by optional multi-repo
helper mode.

Examples:
  mq registry prune
  mq registry prune --json

Flags:
`, currentCLIProgramName()))

	var asJSON bool
	var registryPath string
	fs.BoolVar(&asJSON, "json", false, "output json")
	fs.StringVar(&registryPath, "registry", "", "registry path override")
	if err := fs.Parse(args); err != nil {
		return err
	}

	path := registryPath
	if path == "" {
		var err error
		path, err = globalRegistryPath()
		if err != nil {
			return err
		}
	}
	pruned, remaining, err := pruneRegisteredReposFromPath(path)
	if err != nil {
		return err
	}
	result := registryPruneResult{
		RegistryPath:   path,
		PrunedCount:    len(pruned),
		RemainingCount: len(remaining),
		PrunedRepos:    pruned,
	}
	if asJSON {
		encoder := json.NewEncoder(stdout.Raw())
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	printer := stdout
	printer.Section("Registry prune")
	printer.Line("Registry: %s", result.RegistryPath)
	printer.Line("Pruned: %d", result.PrunedCount)
	printer.Line("Remaining: %d", result.RemainingCount)
	for _, repo := range result.PrunedRepos {
		printer.Line("removed: %s", repo)
	}
	return nil
}

func runRepoShow(args []string, stdout *stepPrinter, stderr io.Writer) error {
	fs := flag.NewFlagSet(currentCLIProgramName()+" repo show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	setFlagUsage(fs, fmt.Sprintf(`Usage:
  %s repo show [flags]

Show the stored repo config, protected-branch status, and discovered worktrees.

Examples:
  mq repo show --repo /path/to/repo-root
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

	store := state.NewStore(state.DefaultPath(layout.GitDir))
	ctx := context.Background()
	cfgAuthority, err := loadConfigAuthority(ctx, layout, store, "")
	if err != nil {
		return err
	}
	cfg := cfgAuthority.File
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
		RepositoryRoot:   repoRoot,
		ConfigPresent:    cfgAuthority.Present,
		ConfigPath:       cfgAuthority.Path,
		Config:           cfg,
		PublishExecution: buildPublishExecutionPolicy(cfg),
		Worktrees:        worktrees,
		Branch:           branch,
		BranchStatus:     branchStatus,
	}
	rootInfo, warnings := inspectCanonicalRootCheckout(cfg, layout)
	warnings = appendMainWorktreeWarnings(engine, cfg, warnings)
	result.RootCheckout = rootInfo
	result.Warnings = warnings

	if asJSON {
		encoder := json.NewEncoder(stdout.Raw())
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	printer := stdout
	printer.Section("Repository")
	printer.Line("Repository root: %s", result.RepositoryRoot)
	printer.Line("Config path: %s", result.ConfigPath)
	printer.Line("Config present: %t", result.ConfigPresent)
	printer.Line("Current branch: %s", result.Branch)
	printer.Line("Protected branch: %s", result.Config.Repo.ProtectedBranch)
	printer.Line("Main worktree: %s", result.Config.Repo.MainWorktree)
	printer.Line("Remote: %s", result.Config.Repo.RemoteName)
	printer.Line("Publish execution: configured_hook_policy=%s effective_hook_policy=%s hooks_bypassed_for_push=%t prepare=%t validate=%t",
		result.PublishExecution.ConfiguredHookPolicy,
		result.PublishExecution.EffectiveHookPolicy,
		result.PublishExecution.HooksBypassedForPush,
		result.PublishExecution.PreparePublishEnabled,
		result.PublishExecution.ValidatePublishEnabled,
	)
	printer.Line("Worktrees: %d", len(result.Worktrees))
	if result.RootCheckout.Path != "" {
		printer.Line("Root checkout: %s", result.RootCheckout.Path)
		printer.Line("Root checkout canonical: %s", yesNo(result.RootCheckout.IsCanonical))
		if result.RootCheckout.Branch != "" {
			printer.Line("Root checkout branch: %s", result.RootCheckout.Branch)
		}
		printer.Line("Root checkout clean: %s", yesNo(result.RootCheckout.Clean))
	}
	if record.ID != 0 {
		printer.Line("State path: %s", state.DefaultPath(layout.GitDir))
	}
	if result.BranchStatus.HasUpstream {
		printer.Line("Protected upstream: %s (ahead %d, behind %d)", result.BranchStatus.Upstream, result.BranchStatus.AheadCount, result.BranchStatus.BehindCount)
	} else {
		printer.Line("Protected upstream: none")
	}
	for _, warning := range result.Warnings {
		printer.Warning("%s", warning)
	}
	return nil
}

func runRepoRoot(args []string, stdout *stepPrinter, stderr io.Writer) error {
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
	store := state.NewStore(state.DefaultPath(layout.GitDir))
	cfgAuthority, err := loadConfigAuthority(context.Background(), layout, store, "")
	if err != nil {
		return err
	}
	cfg := cfgAuthority.File

	fixesApplied := []string{}
	if adoptRoot {
		if err := adoptRepositoryRoot(repoRoot, layout, cfgAuthority.Root, &cfg, cfgAuthority.Present); err != nil {
			return err
		}
		fixesApplied = append(fixesApplied, fmt.Sprintf("set canonical main worktree to repository root %s", filepath.Clean(layout.RepositoryRoot)))
	}

	result := buildRepoRootResult(repoRoot, cfgAuthority.Path, cfg, layout)
	result.FixesApplied = fixesApplied

	if asJSON {
		encoder := json.NewEncoder(stdout.Raw())
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	printer := stdout
	printer.Section("Canonical root")
	printer.Line("Repository root: %s", result.RepositoryRoot)
	printer.Line("Config path: %s", result.ConfigPath)
	printer.Line("Protected branch: %s", result.ProtectedBranch)
	printer.Line("Main worktree: %s", result.MainWorktree)
	printer.Line("Root checkout: %s", result.RootCheckout.Path)
	if result.RootCheckout.Topology != "" {
		printer.Line("Root checkout topology: %s", result.RootCheckout.Topology)
	}
	printer.Line("Root checkout canonical: %s", yesNo(result.RootCheckout.IsCanonical))
	if result.RootCheckout.Branch != "" {
		printer.Line("Root checkout branch: %s", result.RootCheckout.Branch)
	}
	printer.Line("Root checkout clean: %s", yesNo(result.RootCheckout.Clean))
	printer.Line("Root trustworthy: %s", yesNo(result.Trustworthy))
	printer.Line("Can adopt root: %s", yesNo(result.CanAdoptRoot))
	for _, warning := range result.Warnings {
		printer.Warning("%s", warning)
	}
	for _, action := range result.RecommendedActions {
		printer.Info("Next: %s", action)
	}
	for _, applied := range result.FixesApplied {
		printer.Success("Fixed: %s", applied)
	}
	return nil
}

func adoptRepositoryRoot(repoRoot string, layout git.RepositoryLayout, configRoot string, cfg *policy.File, configPresent bool) error {
	if !configPresent {
		return fmt.Errorf("repository is not initialized; run `mq repo init --repo %s` before adopting the root checkout", repoRoot)
	}

	store := state.NewStore(state.DefaultPath(layout.GitDir))
	if !store.Exists() {
		return fmt.Errorf("repository is not initialized; run `mq repo init --repo %s` before adopting the root checkout", repoRoot)
	}

	result := buildRepoRootResult(repoRoot, policy.ConfigPath(configRoot), *cfg, layout)
	if !result.CanAdoptRoot {
		return fmt.Errorf("cannot adopt repository root as canonical main worktree yet; run `mq repo root --repo %s --json` and complete the recommended actions first", repoRoot)
	}

	cfg.Repo.MainWorktree = canonicalRegistryPath(layout.RepositoryRoot)
	if err := saveConfigAuthority(configRoot, *cfg); err != nil {
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

func buildRepoRootResult(repoRoot string, configPath string, cfg policy.File, layout git.RepositoryLayout) repoRootResult {
	rootInfo, warnings := inspectCanonicalRootCheckout(cfg, layout)
	warnings = appendMainWorktreeWarnings(git.NewEngine(layout.WorktreeRoot), cfg, warnings)
	recommended := recommendedRootActions(repoRoot, cfg, rootInfo)
	canAdopt := canAdoptRootCheckout(cfg, rootInfo)
	return repoRootResult{
		RepositoryRoot:     repoRoot,
		ConfigPath:         configPath,
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

func runRepoAudit(args []string, stdout *stepPrinter, stderr io.Writer) error {
	fs := flag.NewFlagSet(currentCLIProgramName()+" repo audit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	setFlagUsage(fs, fmt.Sprintf(`Usage:
  %s repo audit [flags]

List local branches and worktree refs not yet merged into the protected branch.

Examples:
  mq repo audit --repo /path/to/repo-root
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
		encoder := json.NewEncoder(stdout.Raw())
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	printer := stdout
	printer.Section("Repo audit")
	printer.Line("Repository root: %s", result.RepositoryRoot)
	printer.Line("Protected branch: %s", result.ProtectedBranch)
	if result.ProtectedSHA != "" {
		printer.Line("Protected SHA: %s", result.ProtectedSHA)
	}
	if len(result.Unmerged) == 0 {
		printer.Line("Unmerged branches: none")
		return nil
	}

	printer.Section("Unmerged branches")
	for _, branch := range result.Unmerged {
		if branch.WorktreePath != "" {
			printer.Line("%s %s (%s)", branch.Branch, branch.HeadSHA, branch.WorktreePath)
			continue
		}
		printer.Line("%s %s", branch.Branch, branch.HeadSHA)
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

func runDoctor(args []string, stdout *stepPrinter, stderr io.Writer) error {
	fs := flag.NewFlagSet(currentCLIProgramName()+" doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	setFlagUsage(fs, fmt.Sprintf(`Usage:
  %s doctor [flags]

Inspect repo health and optionally apply safe automatic recovery steps.

Examples:
  mq doctor --repo /path/to/repo-root
  mq doctor --repo /path/to/repo-root --fix --json

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
	store := state.NewStore(state.DefaultPath(layout.GitDir))
	cfgAuthority, err := loadConfigAuthority(context.Background(), layout, store, "")
	if err != nil {
		return err
	}
	cfg := cfgAuthority.File

	engine := git.NewEngine(layout.WorktreeRoot)
	report, err := engine.InspectHealth(cfg.Repo.ProtectedBranch, cfg.Repo.MainWorktree)
	if err != nil {
		return err
	}
	rootInfo, rootWarnings := inspectCanonicalRootCheckout(cfg, layout)
	rootWarnings = appendMainWorktreeWarnings(engine, cfg, rootWarnings)
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

	ctx := context.Background()
	var repoRecord state.RepositoryRecord
	var hasRepoRecord bool
	if store.Exists() {
		if record, _, err := ensureRepositoryRecord(ctx, store, repoRoot, cfg); err == nil {
			repoRecord = record
			hasRepoRecord = true
		}
	}

	lockManager := state.NewLockManager(repoRoot, layout.GitDir)
	result := doctorResult{HealthReport: report, RootCheckout: rootInfo}
	if hasRepoRecord {
		snapshot, err := loadRepoStatusSnapshot(ctx, store, repoRecord, cfg, 0)
		if err != nil {
			return err
		}
		result.UnfinishedQueueItems = snapshot.UnfinishedQueueItems
		result.QueueSummary = snapshot.QueueSummary
		result.ActiveSubmissions = snapshot.ActiveSubmissions
		result.ActivePublishes = snapshot.ActivePublishes
		result.IntegrationWorker = snapshot.IntegrationWorker
		result.PublishWorker = snapshot.PublishWorker
		result.ProtectedWorktreeActivity = snapshot.ProtectedWorktreeActivity
	}
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
		rootWarnings = appendMainWorktreeWarnings(engine, cfg, rootWarnings)
		report.Warnings = append(report.Warnings, rootWarnings...)
		result.HealthReport = report
		result.RootCheckout = rootInfo
		if store.Exists() && hasRepoRecord {
			snapshot, err := loadRepoStatusSnapshot(ctx, store, repoRecord, cfg, 0)
			if err != nil {
				return err
			}
			result.UnfinishedQueueItems = snapshot.UnfinishedQueueItems
			result.QueueSummary = snapshot.QueueSummary
			result.ActiveSubmissions = snapshot.ActiveSubmissions
			result.ActivePublishes = snapshot.ActivePublishes
			result.IntegrationWorker = snapshot.IntegrationWorker
			result.PublishWorker = snapshot.PublishWorker
			result.ProtectedWorktreeActivity = snapshot.ProtectedWorktreeActivity
		}
	}
	result.QueueBlocked = !result.ProtectedBranchClean || result.MainWorktreeDetached || (result.MainWorktreeBranch != "" && result.MainWorktreeBranch != result.ProtectedBranch)
	result.NextActions = doctorNextActions(result)
	staleLocks, err := lockManager.InspectStale(time.Hour)
	if err != nil {
		return err
	}
	result.StaleLocks = result.StaleLocks[:0]
	for _, stale := range staleLocks {
		result.StaleLocks = append(result.StaleLocks, stale.Domain+":"+stale.Owner)
	}

	if asJSON {
		encoder := json.NewEncoder(stdout.Raw())
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	printer := stdout
	printer.Section("Doctor")
	printer.Line("Repository root: %s", result.RepositoryRoot)
	printer.Line("Protected branch: %s", result.ProtectedBranch)
	printer.Line("Main worktree: %s", result.MainWorktreePath)
	if result.RootCheckout.Path != "" {
		printer.Line("Root checkout: %s", result.RootCheckout.Path)
		printer.Line("Root checkout canonical: %s", yesNo(result.RootCheckout.IsCanonical))
		if result.RootCheckout.Branch != "" {
			printer.Line("Root checkout branch: %s", result.RootCheckout.Branch)
		}
		printer.Line("Root checkout clean: %s", yesNo(result.RootCheckout.Clean))
	}
	printer.Line("Git repository: %s", yesNo(result.IsGitRepository))
	printer.Line("Protected branch exists: %s", yesNo(result.ProtectedBranchExists))
	printer.Line("Main worktree exists: %s", yesNo(result.MainWorktreeExists))
	printer.Line("Protected branch clean: %s", yesNo(result.ProtectedBranchClean))
	for _, dirtyPath := range result.ProtectedDirtyPaths {
		printer.Warning("Protected dirty path: %s", dirtyPath)
	}
	if result.QueueBlocked {
		printer.Warning("Queue blocked: yes")
		for _, action := range result.NextActions {
			printer.Info("Next action: %s", action)
		}
	}
	if result.HasUpstream {
		printer.Line("Upstream: %s", result.UpstreamRef)
		printer.Line("Behind upstream: %s", yesNo(result.IsBehindUpstream))
		printer.Line("Ahead of upstream: %s", yesNo(result.IsAheadOfUpstream))
		printer.Line("Diverged from upstream: %s", yesNo(result.HasDivergedUpstream))
	} else {
		printer.Line("Upstream: none")
	}
	printer.Line("Stale locks: %d", len(result.StaleLocks))
	for _, stale := range result.StaleLocks {
		printer.Line("Stale lock: %s", stale)
	}
	printer.Line("Unfinished queue items: %d", len(result.UnfinishedQueueItems))
	printer.Line("Active submissions: %d", len(result.ActiveSubmissions))
	printer.Line("Active publishes: %d", len(result.ActivePublishes))
	if result.IntegrationWorker != nil {
		printer.Line("Integration worker: %s (%s)", result.IntegrationWorker.Owner, result.IntegrationWorker.Stage)
	}
	if result.PublishWorker != nil {
		printer.Line("Publish worker: %s (%s)", result.PublishWorker.Owner, result.PublishWorker.Stage)
	}
	if result.ProtectedWorktreeActivity != nil {
		printer.Line("Protected worktree activity: %s", result.ProtectedWorktreeActivity.Summary)
	}
	printer.Line("Warnings: %d", len(result.Warnings))
	for _, warning := range result.Warnings {
		printer.Warning("%s", warning)
	}
	if fix {
		printer.Line("Fixes applied: %d", len(result.FixesApplied))
		for _, applied := range result.FixesApplied {
			printer.Success("Fixed: %s", applied)
		}
		printer.Line("Fixes skipped: %d", len(result.FixesSkipped))
		for _, skipped := range result.FixesSkipped {
			printer.Warning("Skipped: %s", skipped)
		}
	}
	return nil
}

type doctorResult struct {
	git.HealthReport
	RootCheckout              rootCheckoutInfo           `json:"root_checkout,omitempty"`
	QueueSummary              queueSummary               `json:"queue_summary,omitempty"`
	ActiveSubmissions         []statusSubmission         `json:"active_submissions,omitempty"`
	ActivePublishes           []statusPublish            `json:"active_publishes,omitempty"`
	IntegrationWorker         *state.LeaseMetadata       `json:"integration_worker,omitempty"`
	PublishWorker             *state.LeaseMetadata       `json:"publish_worker,omitempty"`
	ProtectedWorktreeActivity *protectedWorktreeActivity `json:"protected_worktree_activity,omitempty"`
	QueueBlocked              bool                       `json:"queue_blocked,omitempty"`
	NextActions               []string                   `json:"next_actions,omitempty"`
	FixesApplied              []string                   `json:"fixes_applied,omitempty"`
	FixesSkipped              []string                   `json:"fixes_skipped,omitempty"`
}

func doctorNextActions(result doctorResult) []string {
	if result.ProtectedBranchClean && !result.MainWorktreeDetached && (result.MainWorktreeBranch == "" || result.MainWorktreeBranch == result.ProtectedBranch) {
		return nil
	}
	doctorRepo := result.MainWorktreePath
	if doctorRepo == "" {
		doctorRepo = result.RepositoryRoot
	}
	if result.MainWorktreeDetached {
		return []string{
			"mainline is blocked until the protected root checkout is back on the protected branch",
			"take ownership of the protected root checkout and switch it with `git checkout --ignore-other-worktrees " + result.ProtectedBranch + "` in " + result.MainWorktreePath,
			"re-run `mq doctor --repo " + doctorRepo + "` after the protected root checkout is back on " + result.ProtectedBranch,
			"retry the blocked operation after the protected root checkout is valid again",
		}
	}
	if result.MainWorktreeBranch != "" && result.MainWorktreeBranch != result.ProtectedBranch {
		return []string{
			"mainline is blocked until the protected root checkout is back on the protected branch",
			"take ownership of the protected root checkout and switch it with `git checkout --ignore-other-worktrees " + result.ProtectedBranch + "` in " + result.MainWorktreePath,
			"re-run `mq doctor --repo " + doctorRepo + "` after the protected root checkout is back on " + result.ProtectedBranch,
			"retry the blocked operation after the protected root checkout is valid again",
		}
	}
	return []string{
		"mainline is blocked until the protected root checkout is clean",
		"take ownership of the protected root checkout and inspect it with `mq doctor --repo " + doctorRepo + "`",
		"if the queue is idle and the root only needs to be restored to shipped main, run `mq doctor --repo " + doctorRepo + " --fix`",
		"otherwise save, clean, or commit local changes, or resolve any abnormal git state on the protected root checkout",
		"retry the blocked operation after the protected root checkout is clean",
	}
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
		mainWorktree := canonicalRegistryPath(cfg.Repo.MainWorktree)
		repoPath := canonicalRegistryPath(layout.WorktreeRoot)
		info := rootCheckoutInfo{
			Path:        canonicalRegistryPath(layout.RepositoryRoot),
			Exists:      false,
			IsCanonical: false,
			Topology:    "bare-repository-storage",
		}
		return info, []string{
			fmt.Sprintf("repository root %s is bare Git storage, not a checkout; `root_checkout.exists = false` is expected here. Trust the canonical protected worktree %s instead", info.Path, mainWorktree),
			fmt.Sprintf("for bare-storage repos, initialize and operate from a clean checkout on %s, for example: `mq repo init --repo %s --protected-branch %s --main-worktree %s`", cfg.Repo.ProtectedBranch, repoPath, cfg.Repo.ProtectedBranch, mainWorktree),
		}
	}

	rootPath := canonicalRegistryPath(layout.RepositoryRoot)
	mainWorktreePath := canonicalRegistryPath(cfg.Repo.MainWorktree)
	info := rootCheckoutInfo{
		Path:        rootPath,
		Exists:      true,
		IsCanonical: mainWorktreePath == rootPath,
		Topology:    "root-checkout",
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

func appendMainWorktreeWarnings(engine git.Engine, cfg policy.File, resultWarnings []string) []string {
	mainWorktreePath := canonicalRegistryPath(cfg.Repo.MainWorktree)
	if mainWorktreePath == "" {
		return resultWarnings
	}
	if _, err := os.Stat(mainWorktreePath); err != nil {
		return append(resultWarnings, fmt.Sprintf("configured main worktree %s is missing; re-run `mq repo init --repo %s --protected-branch %s --main-worktree %s` from a clean checkout on %s", mainWorktreePath, mainWorktreePath, cfg.Repo.ProtectedBranch, mainWorktreePath, cfg.Repo.ProtectedBranch))
	}
	worktrees, err := engine.ListWorktrees()
	if err != nil {
		return append(resultWarnings, fmt.Sprintf("could not inspect configured main worktree %s: %v", mainWorktreePath, err))
	}
	for _, wt := range worktrees {
		if wt.Path != mainWorktreePath {
			continue
		}
		if wt.IsDetached {
			return append(resultWarnings, fmt.Sprintf("configured main worktree %s is detached; run `git checkout --ignore-other-worktrees %s` in %s, then re-run `mq repo init --repo %s --protected-branch %s --main-worktree %s`", mainWorktreePath, cfg.Repo.ProtectedBranch, mainWorktreePath, mainWorktreePath, cfg.Repo.ProtectedBranch, mainWorktreePath))
		}
		if wt.Branch != cfg.Repo.ProtectedBranch {
			return append(resultWarnings, fmt.Sprintf("configured main worktree %s is on branch %s, expected %s; switch it with `git checkout --ignore-other-worktrees %s` or re-run `mq repo init --repo %s --protected-branch %s --main-worktree %s`", mainWorktreePath, wt.Branch, cfg.Repo.ProtectedBranch, cfg.Repo.ProtectedBranch, mainWorktreePath, cfg.Repo.ProtectedBranch, mainWorktreePath))
		}
		return resultWarnings
	}
	return append(resultWarnings, fmt.Sprintf("configured main worktree %s is not registered in the repository worktree list; re-run `mq repo init --repo %s --protected-branch %s --main-worktree %s` from a clean checkout on %s", mainWorktreePath, mainWorktreePath, cfg.Repo.ProtectedBranch, mainWorktreePath, cfg.Repo.ProtectedBranch))
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
		if _, err := store.UpdateIntegrationSubmissionStatus(ctx, submission.ID, domain.SubmissionStatusFailed, fmt.Sprintf("source branch %q no longer exists; resubmit from a live worktree", submission.SourceRef)); err != nil {
			return nil, nil, err
		}
		if err := appendSubmissionLifecycleEvent(ctx, store, repoRecord.ID, submission.ID, domain.EventTypeIntegrationFailed, domain.SubmissionLifecyclePayload{
			Branch:         submissionDisplayRef(submission),
			SourceRef:      submission.SourceRef,
			RefKind:        submission.RefKind,
			SourceWorktree: submission.SourceWorktree,
			SourceSHA:      submission.SourceSHA,
			Error:          fmt.Sprintf("source branch %q no longer exists; resubmit from a live worktree", submission.SourceRef),
		}); err != nil {
			return nil, nil, err
		}
		applied = append(applied, fmt.Sprintf("failed queued submission %d because branch %q no longer exists", submission.ID, submission.SourceRef))
	}

	allSubmissions, err := store.ListIntegrationSubmissions(ctx, repoRecord.ID)
	if err != nil {
		return nil, nil, err
	}
	for _, landed := range allSubmissions {
		if landed.Status != domain.SubmissionStatusSucceeded {
			continue
		}
		if err := supersedeObsoleteSubmissions(ctx, store, repoRecord.ID, engine, landed, ""); err != nil {
			return nil, nil, err
		}
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

	if !protectedWorktreeCleanForPublish {
		repaired, repairNote, repairErr := tryRepairCanonicalProtectedRoot(ctx, engine, cfg, store, repoRecord, true)
		if repairErr != nil {
			skipped = append(skipped, repairNote)
		} else if repaired {
			applied = append(applied, repairNote)
			protectedWorktreeCleanForPublish = true
		} else if repairNote != "" {
			skipped = append(skipped, repairNote)
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
					request, created, err := ensureLatestPublishRequestRecord(ctx, store, repoRecord.ID, protectedSHA, submissionPriorityNormal)
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

func tryRepairCanonicalProtectedRoot(ctx context.Context, engine git.Engine, cfg policy.File, store state.Store, repoRecord state.RepositoryRecord, requireIdle bool) (bool, string, error) {
	if cfg.Repo.MainWorktree == "" {
		return false, "", nil
	}
	layout, err := git.DiscoverRepositoryLayout(cfg.Repo.MainWorktree)
	if err != nil {
		return false, fmt.Sprintf("left protected root dirty because main worktree %s could not be inspected", cfg.Repo.MainWorktree), nil
	}
	rootPath := canonicalRegistryPath(layout.RepositoryRoot)
	mainPath := canonicalRegistryPath(cfg.Repo.MainWorktree)
	if rootPath != mainPath {
		return false, "left protected worktree dirty because doctor only auto-repairs the canonical repository root checkout", nil
	}
	branch, err := git.NewEngine(rootPath).CurrentBranch()
	if err != nil {
		return false, fmt.Sprintf("left protected root dirty because repository root is not on a local branch: %v", err), nil
	}
	if branch != cfg.Repo.ProtectedBranch {
		return false, fmt.Sprintf("left protected root dirty because repository root is on %s, expected %s", branch, cfg.Repo.ProtectedBranch), nil
	}
	if requireIdle {
		count, err := store.CountUnfinishedItems(ctx, repoRecord.ID)
		if err != nil {
			return false, "", err
		}
		if count != 0 {
			return false, fmt.Sprintf("left protected root dirty because %d unfinished queue item(s) still need operator attention", count), nil
		}
	}
	if cfg.Repo.RemoteName == "" {
		return false, "left protected root dirty because no remote is configured for safe repair", nil
	}
	if err := engine.FetchRemote(cfg.Repo.MainWorktree, cfg.Repo.RemoteName); err != nil {
		return false, fmt.Sprintf("left protected root dirty because upstream fetch failed: %v", err), nil
	}
	branchStatus, err := engine.BranchStatus(cfg.Repo.ProtectedBranch, cfg.Repo.ProtectedBranch)
	if err != nil {
		return false, "", err
	}
	if !branchStatus.HasUpstream {
		return false, "left protected root dirty because the protected branch has no upstream to restore from", nil
	}
	if branchStatus.HasUpstream && branchStatus.AheadCount > 0 && branchStatus.BehindCount > 0 {
		return false, fmt.Sprintf("left protected root dirty because %s has diverged from %s", cfg.Repo.ProtectedBranch, branchStatus.Upstream), nil
	}
	if branchStatus.AheadCount > 0 {
		return false, fmt.Sprintf("left protected root dirty because %s is ahead of %s; publish or reconcile it first", cfg.Repo.ProtectedBranch, branchStatus.Upstream), nil
	}
	if err := engine.ResetHardClean(cfg.Repo.MainWorktree, branchStatus.Upstream); err != nil {
		return false, "", err
	}
	return true, fmt.Sprintf("restored canonical protected root checkout %s to %s", cfg.Repo.MainWorktree, branchStatus.Upstream), nil
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
