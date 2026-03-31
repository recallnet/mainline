package app

import (
	"bytes"
	"strings"
	"testing"
)

func TestCLIHelp(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := runCLI([]string{"--help"}, &stdout, &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	if !strings.Contains(stdout.String(), "Usage:") {
		t.Fatalf("expected help output, got %q", stdout.String())
	}
}

func TestDaemonHelp(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := runDaemon([]string{"--help"}, &stdout, &stderr); err != nil {
		t.Fatalf("runDaemon returned error: %v", err)
	}

	if !strings.Contains(stdout.String(), "mainlined runs the background worker loop") {
		t.Fatalf("expected daemon help output, got %q", stdout.String())
	}
}
