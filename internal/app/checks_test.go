package app

import (
	"testing"

	"github.com/recallnet/mainline/internal/policy"
)

func TestShouldBypassGitHooksWhenPublishChecksConfigured(t *testing.T) {
	cfg := policy.DefaultFile()
	cfg.Checks.PreparePublish = []string{"pnpm install --frozen-lockfile"}
	if !shouldBypassGitHooks(cfg) {
		t.Fatalf("expected explicit prepare publish checks to bypass inherited git hooks")
	}

	cfg = policy.DefaultFile()
	cfg.Checks.ValidatePublish = []string{"pnpm test"}
	if !shouldBypassGitHooks(cfg) {
		t.Fatalf("expected explicit validate publish checks to bypass inherited git hooks")
	}

	cfg = policy.DefaultFile()
	cfg.Checks.PrePublish = []string{"legacy"}
	if !shouldBypassGitHooks(cfg) {
		t.Fatalf("expected legacy pre-publish checks to bypass inherited git hooks")
	}
}
