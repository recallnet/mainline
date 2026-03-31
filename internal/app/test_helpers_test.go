package app

import (
	"bytes"
	"os/exec"
	"testing"
)

func runTestCommand(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()

	cmd := exec.Command(name, args...)
	cmd.Dir = dir

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %v failed: %v: %s", name, args, err, stderr.String())
	}

	return stdout.String()
}
