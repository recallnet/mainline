package app

import (
	"testing"
	"time"
)

func TestResolveCommandTimeoutClampsToHardCeiling(t *testing.T) {
	requested, effective, err := resolveCommandTimeout("30m")
	if err != nil {
		t.Fatalf("resolveCommandTimeout: %v", err)
	}
	if requested != 30*time.Minute {
		t.Fatalf("expected requested timeout 30m, got %s", requested)
	}
	if effective != maxCommandTimeout {
		t.Fatalf("expected effective timeout %s, got %s", maxCommandTimeout, effective)
	}
}
