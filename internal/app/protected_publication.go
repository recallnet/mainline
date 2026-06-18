package app

import (
	"context"
	"fmt"

	"github.com/recallnet/mainline/internal/domain"
	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/policy"
	"github.com/recallnet/mainline/internal/state"
)

type protectedPublicationStatus struct {
	ProtectedSHA            string `json:"protected_sha,omitempty"`
	LatestPublishedSHA      string `json:"latest_published_sha,omitempty"`
	LatestPublishRequestID  int64  `json:"latest_publish_request_id,omitempty"`
	Unpublished             bool   `json:"unpublished"`
	PublishQueuedOrRunning  bool   `json:"publish_queued_or_running,omitempty"`
	QueuedPublishRequestID  int64  `json:"queued_publish_request_id,omitempty"`
	RunningPublishRequestID int64  `json:"running_publish_request_id,omitempty"`
	Command                 string `json:"command,omitempty"`
	Message                 string `json:"message,omitempty"`
}

func inspectProtectedPublication(ctx context.Context, store state.Store, repoRecord state.RepositoryRecord, cfg policy.File, protectedSHA string, protectedStatus git.BranchStatus) (*protectedPublicationStatus, error) {
	if cfg.Repo.RemoteName == "" || protectedSHA == "" {
		return nil, nil
	}
	requests, err := store.ListPublishRequests(ctx, repoRecord.ID)
	if err != nil {
		return nil, err
	}

	result := &protectedPublicationStatus{ProtectedSHA: protectedSHA}
	for i := len(requests) - 1; i >= 0; i-- {
		request := requests[i]
		if request.Status == domain.PublishStatusSucceeded && result.LatestPublishedSHA == "" {
			result.LatestPublishedSHA = request.TargetSHA
			result.LatestPublishRequestID = request.ID
		}
		if request.TargetSHA != protectedSHA {
			continue
		}
		switch request.Status {
		case domain.PublishStatusQueued:
			result.PublishQueuedOrRunning = true
			result.QueuedPublishRequestID = request.ID
		case domain.PublishStatusRunning:
			result.PublishQueuedOrRunning = true
			result.RunningPublishRequestID = request.ID
		}
	}

	result.Unpublished = protectedStatus.HasUpstream && protectedStatus.AheadCount > 0
	if result.LatestPublishedSHA != "" && result.LatestPublishedSHA != protectedSHA {
		result.Unpublished = true
	}
	if !result.Unpublished {
		return nil, nil
	}

	if result.PublishQueuedOrRunning {
		result.Message = fmt.Sprintf("protected %s has unpublished commits at %s; publish is already queued or running for this tip", cfg.Repo.ProtectedBranch, protectedSHA)
		return result, nil
	}
	repoPath := cfg.Repo.MainWorktree
	if repoPath == "" {
		repoPath = repoRecord.CanonicalPath
	}
	result.Command = fmt.Sprintf("mq publish --repo %s", repoPath)
	result.Message = fmt.Sprintf("protected %s has unpublished commits at %s; run %s", cfg.Repo.ProtectedBranch, protectedSHA, result.Command)
	return result, nil
}

func inspectDoctorProtectedPublication(ctx context.Context, engine git.Engine, store state.Store, repoRecord state.RepositoryRecord, cfg policy.File) (*protectedPublicationStatus, error) {
	protectedSHA, err := engine.BranchHeadSHA(cfg.Repo.ProtectedBranch)
	if err != nil {
		return nil, err
	}
	protectedStatus, err := protectedBranchStatus(engine, cfg)
	if err != nil {
		return nil, err
	}
	return inspectProtectedPublication(ctx, store, repoRecord, cfg, protectedSHA, protectedStatus)
}

func protectedPublicationAlert(status *protectedPublicationStatus) string {
	if status == nil || !status.Unpublished {
		return ""
	}
	if status.Message != "" {
		return status.Message
	}
	return "protected main has unpublished commits"
}
