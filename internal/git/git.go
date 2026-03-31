package git

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

const noUpstream = "(no upstream)"

var ErrRebaseConflict = errors.New("git rebase reported conflicts")
var ErrFastForwardRejected = errors.New("fast-forward update was rejected")
var ErrPushRejected = errors.New("git push was rejected")
var ErrPushInterrupted = errors.New("git push was interrupted")

// Engine holds repository-local Git execution context.
type Engine struct {
	RepositoryRoot string
}

// PushHandle tracks an in-flight git push subprocess.
type PushHandle struct {
	cmd  *exec.Cmd
	done chan pushResult
}

type pushResult struct {
	output string
	err    error
}

// RepositoryLayout describes the shared storage and canonical worktree identity.
type RepositoryLayout struct {
	RepositoryRoot string `json:"repository_root"`
	WorktreeRoot   string `json:"worktree_root"`
	GitDir         string `json:"git_dir"`
}

// BranchStatus describes a branch and its upstream relationship.
type BranchStatus struct {
	Name              string `json:"name"`
	HeadSHA           string `json:"head_sha"`
	Upstream          string `json:"upstream"`
	AheadCount        int    `json:"ahead_count"`
	BehindCount       int    `json:"behind_count"`
	HasUpstream       bool   `json:"has_upstream"`
	IsProtectedBranch bool   `json:"is_protected_branch"`
}

// Worktree describes a git worktree.
type Worktree struct {
	Path       string `json:"path"`
	HeadSHA    string `json:"head_sha"`
	Branch     string `json:"branch"`
	IsBare     bool   `json:"is_bare"`
	IsDetached bool   `json:"is_detached"`
	IsCurrent  bool   `json:"is_current"`
}

// HealthReport captures repository health relevant to Milestone 1.
type HealthReport struct {
	RepositoryRoot        string   `json:"repository_root"`
	ProtectedBranch       string   `json:"protected_branch"`
	MainWorktreePath      string   `json:"main_worktree_path"`
	IsGitRepository       bool     `json:"is_git_repository"`
	ProtectedBranchExists bool     `json:"protected_branch_exists"`
	MainWorktreeExists    bool     `json:"main_worktree_exists"`
	ProtectedBranchClean  bool     `json:"protected_branch_clean"`
	HasUpstream           bool     `json:"has_upstream"`
	UpstreamRef           string   `json:"upstream_ref"`
	IsBehindUpstream      bool     `json:"is_behind_upstream"`
	IsAheadOfUpstream     bool     `json:"is_ahead_of_upstream"`
	HasDivergedUpstream   bool     `json:"has_diverged_upstream"`
	StaleLocks            []string `json:"stale_locks"`
	UnfinishedQueueItems  []string `json:"unfinished_queue_items"`
	Warnings              []string `json:"warnings"`
}

// NewEngine returns a Git engine rooted at the provided repository path.
func NewEngine(repositoryRoot string) Engine {
	return Engine{RepositoryRoot: repositoryRoot}
}

// DiscoverRepositoryRoot resolves the git repository root from a starting path.
func DiscoverRepositoryRoot(startPath string) (string, error) {
	layout, err := DiscoverRepositoryLayout(startPath)
	if err != nil {
		return "", err
	}

	return layout.RepositoryRoot, nil
}

// DiscoverRepositoryLayout resolves the canonical worktree root and shared git storage path.
func DiscoverRepositoryLayout(startPath string) (RepositoryLayout, error) {
	absPath, err := filepath.Abs(startPath)
	if err != nil {
		return RepositoryLayout{}, err
	}
	absPath = normalizePath(absPath)

	info, err := os.Stat(absPath)
	if err == nil && !info.IsDir() {
		absPath = filepath.Dir(absPath)
	}

	for current := absPath; ; current = filepath.Dir(current) {
		if !hasDotGitMarker(current) {
			if parent := filepath.Dir(current); parent == current {
				break
			}
			continue
		}

		if layout, err := resolveRepositoryLayoutFromWorktree(current); err == nil {
			return layout, nil
		}

		if parent := filepath.Dir(current); parent == current {
			break
		}
	}

	return RepositoryLayout{}, fmt.Errorf("%s is not a git repository", startPath)
}

// CurrentBranch returns the currently checked out branch name.
func (e Engine) CurrentBranch() (string, error) {
	repo, err := e.open()
	if err != nil {
		return "", err
	}

	head, err := repo.Head()
	if err != nil {
		return "", err
	}

	if !head.Name().IsBranch() {
		return "", fmt.Errorf("repository is in detached HEAD state")
	}

	return head.Name().Short(), nil
}

// BranchHeadSHA returns the commit SHA for the provided ref.
func (e Engine) BranchHeadSHA(ref string) (string, error) {
	repo, err := e.open()
	if err != nil {
		return "", err
	}

	revision := plumbing.Revision(ref)
	hash, err := repo.ResolveRevision(revision)
	if err != nil && !strings.HasPrefix(ref, "refs/") {
		revision = plumbing.Revision(plumbing.NewBranchReferenceName(ref).String())
		hash, err = repo.ResolveRevision(revision)
	}
	if err != nil {
		return "", err
	}

	return hash.String(), nil
}

// IsAncestor reports whether ancestorRef is an ancestor of descendantRef.
func (e Engine) IsAncestor(ancestorRef string, descendantRef string) (bool, error) {
	repo, err := e.open()
	if err != nil {
		return false, err
	}

	resolveCommit := func(ref string) (*object.Commit, error) {
		revision := plumbing.Revision(ref)
		hash, err := repo.ResolveRevision(revision)
		if err != nil && !strings.HasPrefix(ref, "refs/") {
			revision = plumbing.Revision(plumbing.NewBranchReferenceName(ref).String())
			hash, err = repo.ResolveRevision(revision)
		}
		if err != nil {
			return nil, err
		}
		return repo.CommitObject(*hash)
	}

	ancestorCommit, err := resolveCommit(ancestorRef)
	if err != nil {
		return false, err
	}
	descendantCommit, err := resolveCommit(descendantRef)
	if err != nil {
		return false, err
	}
	if ancestorCommit.Hash == descendantCommit.Hash {
		return true, nil
	}
	return ancestorCommit.IsAncestor(descendantCommit)
}

// BranchExists reports whether a local branch exists.
func (e Engine) BranchExists(branch string) bool {
	repo, err := e.open()
	if err != nil {
		return false
	}

	_, err = repo.Reference(plumbing.NewBranchReferenceName(branch), true)
	return err == nil
}

// ListWorktrees returns the main and linked worktrees associated with the repository.
func (e Engine) ListWorktrees() ([]Worktree, error) {
	layout, err := DiscoverRepositoryLayout(e.RepositoryRoot)
	if err != nil {
		return nil, err
	}

	worktreePaths, err := discoverWorktreePaths(layout)
	if err != nil {
		return nil, err
	}

	worktrees := make([]Worktree, 0, len(worktreePaths))
	for _, wtPath := range worktreePaths {
		repo, err := openRepository(wtPath)
		if err != nil {
			return nil, err
		}

		head, err := repo.Head()
		if err != nil {
			return nil, err
		}

		wt := Worktree{
			Path:       normalizePath(wtPath),
			HeadSHA:    head.Hash().String(),
			IsDetached: !head.Name().IsBranch(),
			IsCurrent:  normalizePath(wtPath) == layout.WorktreeRoot,
		}
		if head.Name().IsBranch() {
			wt.Branch = head.Name().Short()
		}

		worktrees = append(worktrees, wt)
	}

	return worktrees, nil
}

// StatusPorcelain returns the repository status in a stable text format.
func (e Engine) StatusPorcelain(path string) (string, error) {
	repo, err := openRepository(path)
	if err != nil {
		return "", err
	}

	wt, err := repo.Worktree()
	if err != nil {
		return "", err
	}

	status, err := wt.Status()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(status.String()), nil
}

// WorktreeIsClean reports whether a worktree has no staged or unstaged changes.
func (e Engine) WorktreeIsClean(path string) (bool, error) {
	status, err := e.StatusPorcelain(path)
	if err != nil {
		return false, err
	}

	return status == "", nil
}

// CurrentBranchAtPath returns the checked-out branch for a specific worktree path.
func (e Engine) CurrentBranchAtPath(path string) (string, error) {
	repo, err := openRepository(path)
	if err != nil {
		return "", err
	}

	head, err := repo.Head()
	if err != nil {
		return "", err
	}
	if !head.Name().IsBranch() {
		return "", fmt.Errorf("worktree is in detached HEAD state")
	}

	return head.Name().Short(), nil
}

// ResolveWorktree finds a known worktree by path.
func (e Engine) ResolveWorktree(path string) (Worktree, error) {
	worktrees, err := e.ListWorktrees()
	if err != nil {
		return Worktree{}, err
	}

	cleanPath := normalizePath(path)
	for _, wt := range worktrees {
		if normalizePath(wt.Path) == cleanPath {
			return wt, nil
		}
	}

	return Worktree{}, fmt.Errorf("worktree %s is not part of this repository", path)
}

// CommitCount returns the number of commits reachable from a branch.
func (e Engine) CommitCount(branch string) (int, error) {
	layout, err := DiscoverRepositoryLayout(e.RepositoryRoot)
	if err != nil {
		return 0, err
	}

	cmd := exec.Command("git", "rev-list", "--count", branch)
	cmd.Dir = layout.WorktreeRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("count commits for %s: %w: %s", branch, err, strings.TrimSpace(string(output)))
	}

	var count int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(output)), "%d", &count); err != nil {
		return 0, fmt.Errorf("parse commit count for %s: %w", branch, err)
	}

	return count, nil
}

// FetchRemote updates remote tracking refs for the configured remote.
func (e Engine) FetchRemote(worktreePath string, remote string) error {
	if remote == "" {
		return nil
	}

	_, err := e.runGit(worktreePath, "fetch", remote)
	return err
}

// RebaseCurrentBranch rebases the checked-out branch in a worktree onto upstreamRef.
func (e Engine) RebaseCurrentBranch(worktreePath string, upstreamRef string) error {
	output, err := e.runGit(worktreePath, "rebase", upstreamRef)
	if err == nil {
		return nil
	}
	if strings.Contains(output, "CONFLICT") || strings.Contains(output, "Resolve all conflicts manually") {
		return fmt.Errorf("%w: %s", ErrRebaseConflict, strings.TrimSpace(output))
	}
	return err
}

// ConflictedFiles returns the current unmerged paths in a worktree.
func (e Engine) ConflictedFiles(worktreePath string) ([]string, error) {
	output, err := e.runGit(worktreePath, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	files := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		files = append(files, line)
	}
	return files, nil
}

// FastForwardCurrentBranch fast-forwards the checked-out branch in a worktree to targetRef.
func (e Engine) FastForwardCurrentBranch(worktreePath string, targetRef string) error {
	output, err := e.runGit(worktreePath, "merge", "--ff-only", targetRef)
	if err == nil {
		return nil
	}
	if strings.Contains(output, "Not possible to fast-forward") || strings.Contains(output, "fatal: Not possible to fast-forward") {
		return fmt.Errorf("%w: %s", ErrFastForwardRejected, strings.TrimSpace(output))
	}
	return err
}

// PushBranch pushes a local branch ref to the configured remote branch.
func (e Engine) PushBranch(worktreePath string, remote string, branch string, noVerify bool) error {
	handle, err := e.StartPushBranch(worktreePath, remote, branch, noVerify)
	if err != nil {
		return err
	}
	_, err = handle.Wait()
	return err
}

// StartPushBranch starts a local git push and returns a handle for monitoring.
func (e Engine) StartPushBranch(worktreePath string, remote string, branch string, noVerify bool) (*PushHandle, error) {
	if remote == "" {
		return nil, fmt.Errorf("remote name is empty")
	}
	args := []string{"push"}
	if noVerify {
		args = append(args, "--no-verify")
	}
	args = append(args, remote, branch+":"+branch)
	cmd := exec.Command("git", args...)
	cmd.Dir = filepath.Clean(worktreePath)
	cmd.SysProcAttr = pushSysProcAttr()

	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}

	handle := &PushHandle{
		cmd:  cmd,
		done: make(chan pushResult, 1),
	}
	go func() {
		err := cmd.Wait()
		text := output.String()
		if err != nil {
			if cmd.ProcessState != nil && !cmd.ProcessState.Success() && strings.Contains(err.Error(), "signal: killed") {
				handle.done <- pushResult{output: text, err: fmt.Errorf("%w: %s", ErrPushInterrupted, strings.TrimSpace(text))}
				return
			}
			if strings.Contains(text, "[rejected]") || strings.Contains(text, "failed to push some refs") {
				handle.done <- pushResult{output: text, err: fmt.Errorf("%w: %s", ErrPushRejected, strings.TrimSpace(text))}
				return
			}
			handle.done <- pushResult{output: text, err: fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(text))}
			return
		}
		handle.done <- pushResult{output: text, err: nil}
	}()

	return handle, nil
}

// PID reports the subprocess pid for an active push.
func (h *PushHandle) PID() int {
	if h == nil || h.cmd == nil || h.cmd.Process == nil {
		return 0
	}
	return h.cmd.Process.Pid
}

// Interrupt terminates the in-flight push subprocess.
func (h *PushHandle) Interrupt() error {
	if h == nil || h.cmd == nil || h.cmd.Process == nil {
		return nil
	}
	return InterruptProcess(h.cmd.Process.Pid)
}

// Wait waits for the subprocess and returns captured output.
func (h *PushHandle) Wait() (string, error) {
	if h == nil {
		return "", nil
	}
	result := <-h.done
	return result.output, result.err
}

// InterruptProcess terminates an in-flight git push subprocess and its children when supported.
func InterruptProcess(pid int) error {
	if pid == 0 {
		return nil
	}
	return interruptProcess(pid)
}

// BranchStatus returns the branch status including upstream relationship.
func (e Engine) BranchStatus(branch string, protectedBranch string) (BranchStatus, error) {
	repo, err := e.open()
	if err != nil {
		return BranchStatus{}, err
	}

	branchRef, err := repo.Reference(plumbing.NewBranchReferenceName(branch), true)
	if err != nil {
		return BranchStatus{}, err
	}

	status := BranchStatus{
		Name:              branch,
		HeadSHA:           branchRef.Hash().String(),
		Upstream:          noUpstream,
		IsProtectedBranch: branch == protectedBranch,
	}

	branchConfig, err := repo.Branch(branch)
	if err != nil {
		return status, nil
	}

	upstreamRefName, upstreamLabel, ok := upstreamReferenceName(branchConfig)
	if !ok {
		return status, nil
	}

	upstreamRef, err := repo.Reference(upstreamRefName, true)
	if err != nil {
		return status, nil
	}

	status.Upstream = upstreamLabel
	status.HasUpstream = true

	branchCommit, err := repo.CommitObject(branchRef.Hash())
	if err != nil {
		return BranchStatus{}, err
	}
	upstreamCommit, err := repo.CommitObject(upstreamRef.Hash())
	if err != nil {
		return BranchStatus{}, err
	}

	if branchCommit.Hash == upstreamCommit.Hash {
		return status, nil
	}

	branchBehind, err := branchCommit.IsAncestor(upstreamCommit)
	if err != nil {
		return BranchStatus{}, err
	}
	upstreamBehind, err := upstreamCommit.IsAncestor(branchCommit)
	if err != nil {
		return BranchStatus{}, err
	}

	if branchBehind {
		status.BehindCount = 1
	}
	if upstreamBehind {
		status.AheadCount = 1
	}
	if !branchBehind && !upstreamBehind {
		status.AheadCount = 1
		status.BehindCount = 1
	}

	return status, nil
}

// InspectHealth reports doctor-level repository health.
func (e Engine) InspectHealth(protectedBranch string, mainWorktreePath string) (HealthReport, error) {
	layout, err := DiscoverRepositoryLayout(e.RepositoryRoot)
	if err != nil {
		return HealthReport{RepositoryRoot: filepath.Clean(e.RepositoryRoot)}, err
	}
	repoRoot := layout.RepositoryRoot

	engine := NewEngine(layout.WorktreeRoot)
	report := HealthReport{
		RepositoryRoot:       repoRoot,
		ProtectedBranch:      protectedBranch,
		MainWorktreePath:     normalizePath(mainWorktreePath),
		IsGitRepository:      true,
		StaleLocks:           []string{},
		UnfinishedQueueItems: []string{},
		Warnings:             []string{},
	}

	report.ProtectedBranchExists = engine.BranchExists(protectedBranch)

	worktrees, err := engine.ListWorktrees()
	if err != nil {
		return HealthReport{}, err
	}

	for _, wt := range worktrees {
		if filepath.Clean(wt.Path) != report.MainWorktreePath {
			continue
		}

		report.MainWorktreeExists = true
		if wt.Branch == protectedBranch {
			status, err := engine.StatusPorcelain(wt.Path)
			if err != nil {
				return HealthReport{}, err
			}
			report.ProtectedBranchClean = status == ""
		}
		break
	}

	if report.ProtectedBranchExists {
		branchStatus, err := engine.BranchStatus(protectedBranch, protectedBranch)
		if err != nil {
			return HealthReport{}, err
		}

		report.HasUpstream = branchStatus.HasUpstream
		report.UpstreamRef = branchStatus.Upstream
		report.IsAheadOfUpstream = branchStatus.AheadCount > 0
		report.IsBehindUpstream = branchStatus.BehindCount > 0
		report.HasDivergedUpstream = report.IsAheadOfUpstream && report.IsBehindUpstream
	}

	return report, nil
}

// IsNotRepositoryError reports whether an error indicates the path is not a git repository.
func IsNotRepositoryError(err error) bool {
	if err == nil {
		return false
	}

	return strings.Contains(err.Error(), "not a git repository")
}

func (e Engine) open() (*gogit.Repository, error) {
	layout, err := DiscoverRepositoryLayout(e.RepositoryRoot)
	if err != nil {
		return nil, err
	}

	return openRepository(layout.WorktreeRoot)
}

func (e Engine) runGit(worktreePath string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = filepath.Clean(worktreePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func openRepository(path string) (*gogit.Repository, error) {
	return gogit.PlainOpenWithOptions(path, &gogit.PlainOpenOptions{
		DetectDotGit:          true,
		EnableDotGitCommonDir: true,
	})
}

func discoverWorktreePaths(layout RepositoryLayout) ([]string, error) {
	seen := map[string]struct{}{}
	paths := make([]string, 0, 4)
	appendPath := func(path string) {
		clean := normalizePath(path)
		if _, ok := seen[clean]; ok {
			return
		}
		seen[clean] = struct{}{}
		paths = append(paths, clean)
	}

	appendPath(layout.WorktreeRoot)
	worktreesDir := filepath.Join(layout.GitDir, "worktrees")

	entries, err := os.ReadDir(worktreesDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return paths, nil
		}
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		gitdirBytes, err := os.ReadFile(filepath.Join(worktreesDir, entry.Name(), "gitdir"))
		if err != nil {
			return nil, err
		}

		gitdirPath := strings.TrimSpace(string(gitdirBytes))
		if !filepath.IsAbs(gitdirPath) {
			gitdirPath = filepath.Clean(filepath.Join(worktreesDir, entry.Name(), gitdirPath))
		}

		appendPath(filepath.Dir(gitdirPath))
	}

	return paths, nil
}

func hasDotGitMarker(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return true
	}
	return false
}

func resolveRepositoryLayoutFromWorktree(worktreePath string) (RepositoryLayout, error) {
	dotGitPath := filepath.Join(worktreePath, ".git")
	info, err := os.Stat(dotGitPath)
	if err != nil {
		return RepositoryLayout{}, err
	}

	if info.IsDir() {
		if _, err := openRepository(worktreePath); err != nil {
			return RepositoryLayout{}, err
		}
		return RepositoryLayout{
			RepositoryRoot: normalizePath(worktreePath),
			WorktreeRoot:   normalizePath(worktreePath),
			GitDir:         normalizePath(filepath.Join(filepath.Clean(worktreePath), ".git")),
		}, nil
	}

	gitDir, err := resolveGitDirFromFile(worktreePath, dotGitPath)
	if err != nil {
		return RepositoryLayout{}, err
	}

	commonDir, err := resolveCommonDir(gitDir)
	if err != nil {
		return RepositoryLayout{}, err
	}

	repositoryRoot := commonDir
	if filepath.Base(commonDir) == ".git" {
		repositoryRoot = filepath.Dir(commonDir)
	}

	return RepositoryLayout{
		RepositoryRoot: normalizePath(repositoryRoot),
		WorktreeRoot:   normalizePath(worktreePath),
		GitDir:         normalizePath(commonDir),
	}, nil
}

func resolveGitDirFromFile(worktreePath string, dotGitPath string) (string, error) {
	data, err := os.ReadFile(dotGitPath)
	if err != nil {
		return "", err
	}

	line := strings.TrimSpace(string(bytes.TrimSpace(data)))
	const prefix = "gitdir:"
	if !strings.HasPrefix(line, prefix) {
		return "", fmt.Errorf("invalid .git file in %s", worktreePath)
	}

	gitDir := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Clean(filepath.Join(worktreePath, gitDir))
	}

	return gitDir, nil
}

func resolveCommonDir(gitDir string) (string, error) {
	commonDirPath := filepath.Join(gitDir, "commondir")
	data, err := os.ReadFile(commonDirPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return gitDir, nil
		}
		return "", err
	}

	commonDir := strings.TrimSpace(string(bytes.TrimSpace(data)))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Clean(filepath.Join(gitDir, commonDir))
	}

	return commonDir, nil
}

func upstreamReferenceName(branch *config.Branch) (plumbing.ReferenceName, string, bool) {
	if branch == nil || branch.Remote == "" || branch.Merge == "" {
		return "", "", false
	}

	short := branch.Merge.Short()
	return plumbing.NewRemoteReferenceName(branch.Remote, short), branch.Remote + "/" + short, true
}

func commitFromRef(repo *gogit.Repository, refName plumbing.ReferenceName) (*object.Commit, error) {
	ref, err := repo.Reference(refName, true)
	if err != nil {
		return nil, err
	}

	return repo.CommitObject(ref.Hash())
}

func normalizePath(path string) string {
	clean := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		return filepath.Clean(resolved)
	}
	return clean
}
