package app

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/policy"
)

func runConfigEdit(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet("mainline config edit", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var repoPath string
	var editor string
	var printPath bool

	fs.StringVar(&repoPath, "repo", ".", "repository path")
	fs.StringVar(&editor, "editor", "", "editor binary to launch")
	fs.BoolVar(&printPath, "print-path", false, "print config path before opening editor")

	if err := fs.Parse(args); err != nil {
		return err
	}

	layout, err := git.DiscoverRepositoryLayout(repoPath)
	if err != nil {
		return err
	}
	repoRoot := layout.RepositoryRoot

	if err := ensureConfigScaffold(layout); err != nil {
		return err
	}

	configPath := policy.ConfigPath(repoRoot)
	if printPath {
		fmt.Fprintln(stdout, configPath)
	}

	editorBin, err := resolveEditorBinary(editor)
	if err != nil {
		return err
	}

	cmd := exec.Command(editorBin, configPath)
	cmd.Dir = repoRoot
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run editor %q: %w", editorBin, err)
	}

	fmt.Fprintf(stdout, "Edited %s\n", configPath)
	return nil
}

func ensureConfigScaffold(layout git.RepositoryLayout) error {
	repoRoot := layout.RepositoryRoot
	if _, err := os.Stat(policy.ConfigPath(repoRoot)); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	engine := git.NewEngine(layout.WorktreeRoot)
	currentBranch, err := engine.CurrentBranch()
	if err != nil {
		return err
	}

	cfg := policy.DefaultFile()
	if currentBranch != "" {
		cfg.Repo.ProtectedBranch = currentBranch
	}
	cfg.Repo.MainWorktree = filepath.Clean(layout.WorktreeRoot)
	return policy.SaveFile(repoRoot, cfg)
}

func resolveEditorBinary(flagValue string) (string, error) {
	for _, candidate := range []string{flagValue, os.Getenv("VISUAL"), os.Getenv("EDITOR")} {
		if candidate == "" {
			continue
		}
		path, err := exec.LookPath(candidate)
		if err != nil {
			return "", fmt.Errorf("resolve editor %q: %w", candidate, err)
		}
		return path, nil
	}

	return "", fmt.Errorf("no editor configured; pass --editor or set VISUAL/EDITOR")
}
