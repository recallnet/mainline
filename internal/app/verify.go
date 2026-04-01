package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/policy"
	"github.com/recallnet/mainline/internal/state"
)

func verifySubmissionReachable(ctx context.Context, store state.Store, repoID int64, engine git.Engine, cfg policy.File, submission state.IntegrationSubmission) (string, error) {
	landedSHA, err := latestIntegratedProtectedSHA(ctx, store, repoID, submission.ID)
	if err != nil {
		return "", err
	}
	if landedSHA == "" {
		return "", fmt.Errorf("submission %d marked succeeded but has no integration.succeeded event with protected_sha", submission.ID)
	}

	currentProtectedSHA, err := engine.BranchHeadSHA(cfg.Repo.ProtectedBranch)
	if err != nil {
		return "", err
	}

	reachable, err := engine.IsAncestor(landedSHA, cfg.Repo.ProtectedBranch)
	if err != nil {
		return currentProtectedSHA, err
	}
	if !reachable {
		return currentProtectedSHA, fmt.Errorf("submission %d marked succeeded but landed sha %s is not reachable from protected branch %q at %s", submission.ID, landedSHA, cfg.Repo.ProtectedBranch, currentProtectedSHA)
	}

	return currentProtectedSHA, nil
}

func latestIntegratedProtectedSHA(ctx context.Context, store state.Store, repoID int64, submissionID int64) (string, error) {
	events, err := store.ListEventsForItem(ctx, repoID, "integration_submission", submissionID, 10)
	if err != nil {
		return "", err
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].EventType != "integration.succeeded" {
			continue
		}
		if len(events[i].Payload) == 0 {
			return "", nil
		}
		var payload struct {
			ProtectedSHA string `json:"protected_sha"`
		}
		if err := json.Unmarshal(events[i].Payload, &payload); err != nil {
			return "", err
		}
		return payload.ProtectedSHA, nil
	}
	return "", nil
}
