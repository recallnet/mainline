package app

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/recallnet/mainline/internal/policy"
)

func runConfiguredChecks(checks []string, workdir string, timeoutSetting string) error {
	if len(checks) == 0 {
		return nil
	}
	if err := applyAppTestFault("checks.start"); err != nil {
		return err
	}

	timeout, err := time.ParseDuration(timeoutSetting)
	if err != nil {
		return fmt.Errorf("invalid command timeout %q: %w", timeoutSetting, err)
	}
	if timeout <= 0 {
		return fmt.Errorf("command timeout must be positive")
	}

	workdir = filepath.Clean(workdir)
	for _, check := range checks {
		command := strings.TrimSpace(check)
		if command == "" {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		cmd := exec.CommandContext(ctx, "/bin/sh", "-lc", command)
		cmd.Dir = workdir
		output, err := cmd.CombinedOutput()
		cancel()
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("check %q timed out after %s", command, timeout)
		}
		if err != nil {
			return fmt.Errorf("check %q failed: %s", command, strings.TrimSpace(string(output)))
		}
	}

	return nil
}

func shouldBypassGitHooks(cfg policy.File) bool {
	switch cfg.Repo.HookPolicy {
	case "replace-with-mainline-checks", "bypass-with-explicit-command":
		return true
	default:
		return false
	}
}
