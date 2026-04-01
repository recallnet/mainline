package app

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/recallnet/mainline/internal/policy"
)

const maxCommandTimeout = 15 * time.Minute

type checkTimeoutError struct {
	Command          string
	RequestedTimeout time.Duration
	EffectiveTimeout time.Duration
}

func (e *checkTimeoutError) Error() string {
	if e == nil {
		return ""
	}
	if e.RequestedTimeout != e.EffectiveTimeout {
		return fmt.Sprintf("check %q timed out after %s (capped from %s)", e.Command, e.EffectiveTimeout, e.RequestedTimeout)
	}
	return fmt.Sprintf("check %q timed out after %s", e.Command, e.EffectiveTimeout)
}

func isCheckTimeoutError(err error) (*checkTimeoutError, bool) {
	var timeoutErr *checkTimeoutError
	if !errors.As(err, &timeoutErr) {
		return nil, false
	}
	return timeoutErr, true
}

func runConfiguredChecks(checks []string, workdir string, timeoutSetting string) error {
	if len(checks) == 0 {
		return nil
	}
	if err := applyAppTestFault("checks.start"); err != nil {
		return err
	}

	timeout, effectiveTimeout, err := resolveCommandTimeout(timeoutSetting)
	if err != nil {
		return err
	}

	workdir = filepath.Clean(workdir)
	for _, check := range checks {
		command := strings.TrimSpace(check)
		if command == "" {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), effectiveTimeout)
		cmd := exec.CommandContext(ctx, "/bin/sh", "-lc", command)
		cmd.Dir = workdir
		output, err := cmd.CombinedOutput()
		cancel()
		if ctx.Err() == context.DeadlineExceeded {
			return &checkTimeoutError{
				Command:          command,
				RequestedTimeout: timeout,
				EffectiveTimeout: effectiveTimeout,
			}
		}
		if err != nil {
			return fmt.Errorf("check %q failed: %s", command, strings.TrimSpace(string(output)))
		}
	}

	return nil
}

func resolveCommandTimeout(timeoutSetting string) (time.Duration, time.Duration, error) {
	timeout, err := time.ParseDuration(timeoutSetting)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid command timeout %q: %w", timeoutSetting, err)
	}
	if timeout <= 0 {
		return 0, 0, fmt.Errorf("command timeout must be positive")
	}
	effectiveTimeout := timeout
	if effectiveTimeout > maxCommandTimeout {
		effectiveTimeout = maxCommandTimeout
	}
	return timeout, effectiveTimeout, nil
}

func shouldBypassGitHooks(cfg policy.File) bool {
	switch cfg.Repo.HookPolicy {
	case "replace-with-mainline-checks", "bypass-with-explicit-command":
		return true
	default:
		return false
	}
}
