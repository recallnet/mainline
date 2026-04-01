package app

import (
	"fmt"

	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/policy"
	"github.com/recallnet/mainline/internal/state"
)

func verifySubmissionReachable(engine git.Engine, cfg policy.File, submission state.IntegrationSubmission) (string, error) {
	protectedSHA, err := engine.BranchHeadSHA(cfg.Repo.ProtectedBranch)
	if err != nil {
		return "", err
	}

	reachable, err := engine.IsAncestor(submission.SourceSHA, cfg.Repo.ProtectedBranch)
	if err != nil {
		return protectedSHA, err
	}
	if !reachable {
		return protectedSHA, fmt.Errorf("submission %d marked succeeded but source sha %s is not reachable from protected branch %q at %s", submission.ID, submission.SourceSHA, cfg.Repo.ProtectedBranch, protectedSHA)
	}

	return protectedSHA, nil
}
