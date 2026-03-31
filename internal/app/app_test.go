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

func TestCLIAcceptsSubcommandFlagsForPlannedCommands(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := runCLI([]string{"status", "--json"}, &stdout, &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "status is not implemented yet") {
		t.Fatalf("expected status placeholder output, got %q", output)
	}

	if !strings.Contains(output, "trailing argument") {
		t.Fatalf("expected trailing args note, got %q", output)
	}
}

func TestCLIRepoSubcommandsRemainReachable(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	repoRoot, _ := createTestRepo(t)

	if err := runCLI([]string{"repo", "init", "--repo", repoRoot}, &stdout, &stderr); err != nil {
		t.Fatalf("runCLI returned error: %v", err)
	}

	if !strings.Contains(stdout.String(), "Initialized ") {
		t.Fatalf("expected repo init output, got %q", stdout.String())
	}
}
