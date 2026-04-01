//go:build unix

package app

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunConfiguredChecksTimeoutKillsChildProcessGroup(t *testing.T) {
	workdir := t.TempDir()
	pidFile := filepath.Join(workdir, "child.pid")

	err := runConfiguredChecks([]string{
		"sleep 30 & child=$!; echo $child > child.pid; wait $child",
	}, workdir, "100ms")
	timeoutErr, ok := isCheckTimeoutError(err)
	if !ok {
		t.Fatalf("expected check timeout error, got %v", err)
	}
	if timeoutErr == nil {
		t.Fatalf("expected timeout error details")
	}

	var pid int
	deadline := time.Now().Add(2 * time.Second)
	for {
		data, readErr := os.ReadFile(pidFile)
		if readErr == nil {
			parsedPID, convErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if convErr != nil {
				t.Fatalf("parse child pid: %v", convErr)
			}
			pid = parsedPID
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for child pid file: %v", readErr)
		}
		time.Sleep(10 * time.Millisecond)
	}

	deadline = time.Now().Add(2 * time.Second)
	for {
		killErr := syscall.Kill(pid, 0)
		if killErr != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for child pid %d to exit", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
