package app

import (
	"bytes"
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
		output, err := runTimedCheck(ctx, workdir, command)
		cancel()
		if timeoutErr, ok := isCheckTimeoutError(err); ok {
			timeoutErr.RequestedTimeout = timeout
			timeoutErr.EffectiveTimeout = effectiveTimeout
			return timeoutErr
		}
		if err != nil {
			return fmt.Errorf("check %q failed: %s", command, strings.TrimSpace(string(output)))
		}
	}

	return nil
}

func runTimedCheck(ctx context.Context, workdir string, command string) ([]byte, error) {
	cmd := exec.Command("/bin/sh", "-lc", command)
	cmd.SysProcAttr = checkSysProcAttr()
	cmd.Dir = workdir
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	resultCh := make(chan error, 1)
	go func() {
		resultCh <- cmd.Wait()
	}()

	select {
	case err := <-resultCh:
		return output.Bytes(), err
	case <-ctx.Done():
		_ = interruptCheckProcess(cmd.Process.Pid)
		err := <-resultCh
		if ctx.Err() == context.DeadlineExceeded {
			return nil, &checkTimeoutError{
				Command: command,
			}
		}
		return output.Bytes(), err
	}
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
