package app

import (
	"context"
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
	"github.com/recallnet/mainline/internal/state"
)

func runConfigEdit(args []string, stdout *stepPrinter, stderr io.Writer) error {
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

	if err := ensureConfigScaffold(layout); err != nil {
		return err
	}

	store := state.NewStore(state.DefaultPath(layout.GitDir))
	cfgAuthority, err := loadConfigAuthority(context.Background(), layout, store, "")
	if err != nil {
		return err
	}
	configPath := cfgAuthority.Path
	printer := stdout
	if printPath && !asJSON {
		printer.Line("%s", configPath)
	}

	editorCommand, err := resolveEditorCommand(editor)
	if err != nil {
		return err
	}

	cmd := exec.Command(editorCommand[0], append(editorCommand[1:], configPath)...)
	cmd.Dir = filepath.Dir(configPath)
	cmd.Stdin = os.Stdin
	if asJSON {
		cmd.Stdout = stderr
	} else {
		cmd.Stdout = stdout.Raw()
	}
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run editor %q: %w", editorCommand[0], err)
	}

	if asJSON {
		encoder := json.NewEncoder(stdout.Raw())
		encoder.SetIndent("", "  ")
		return encoder.Encode(map[string]any{
			"ok":              true,
			"config_path":     configPath,
			"repository_root": layout.RepositoryRoot,
			"editor":          editorCommand[0],
			"printed_path":    printPath,
		})
	}

	printer.Success("Edited %s", configPath)
	return nil
}

func ensureConfigScaffold(layout git.RepositoryLayout) error {
	configRoot := resolveConfigAuthorityRoot(context.Background(), layout, state.Store{}, "")
	if _, err := os.Stat(policy.ConfigPath(configRoot)); err == nil {
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
	cfg.Repo.MainWorktree = canonicalRegistryPath(layout.WorktreeRoot)
	return saveConfigAuthority(configRoot, cfg)
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
