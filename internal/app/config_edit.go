package app

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/kballard/go-shellquote"
	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/policy"
)

func runConfigEdit(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet(currentCLIProgramName()+" config edit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	setFlagUsage(fs, fmt.Sprintf(`Usage:
  %s config edit [flags]

Open the shared repo config, even when invoked from a linked worktree.

Examples:
  mq config edit --repo /path/to/repo-root
  mq config edit --print-path

Flags:
`, currentCLIProgramName()))

	var repoPath string
	var editor string
	var printPath bool
	var asJSON bool

	fs.StringVar(&repoPath, "repo", ".", "repository path")
	fs.StringVar(&editor, "editor", "", "editor binary to launch")
	fs.BoolVar(&printPath, "print-path", false, "print config path before opening editor")
	fs.BoolVar(&asJSON, "json", false, "output json")

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
	if printPath && !asJSON {
		fmt.Fprintln(stdout, configPath)
	}

	editorCommand, err := resolveEditorCommand(editor)
	if err != nil {
		return err
	}

	cmd := exec.Command(editorCommand[0], append(editorCommand[1:], configPath)...)
	cmd.Dir = repoRoot
	cmd.Stdin = os.Stdin
	if asJSON {
		cmd.Stdout = stderr
	} else {
		cmd.Stdout = stdout
	}
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run editor %q: %w", editorCommand[0], err)
	}

	if asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(map[string]any{
			"ok":              true,
			"config_path":     configPath,
			"repository_root": repoRoot,
			"editor":          editorCommand[0],
			"printed_path":    printPath,
		})
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

func resolveEditorCommand(flagValue string) ([]string, error) {
	for _, candidate := range []string{flagValue, os.Getenv("VISUAL"), os.Getenv("EDITOR")} {
		if candidate == "" {
			continue
		}
		command, err := shellquote.Split(candidate)
		if err != nil {
			return nil, fmt.Errorf("parse editor %q: %w", candidate, err)
		}
		if len(command) == 0 {
			continue
		}
		path, err := exec.LookPath(command[0])
		if err != nil {
			return nil, fmt.Errorf("resolve editor %q: %w", command[0], err)
		}
		command[0] = path
		return command, nil
	}

	return nil, fmt.Errorf("no editor configured; pass --editor or set VISUAL/EDITOR")
}
