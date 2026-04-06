package app

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/recallnet/mainline/internal/domain"
	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/state"
)

const (
	submissionOutcomeIntegrated = domain.SubmissionOutcomeIntegrated
	submissionOutcomeLanded     = domain.SubmissionOutcomeLanded
)

type submissionPublishInfo struct {
	ProtectedSHA     string
	PublishRequestID int64
	PublishStatus    string
	Outcome          domain.SubmissionOutcome
	Failure          publishFailureInfo
}

type publishFailureInfo struct {
	Cause            string
	Summary          string
	Error            string
	RetryHint        string
	ResubmitRequired bool
}

func resolveSubmissionPublishInfo(ctx context.Context, store state.Store, repoID int64, submission state.IntegrationSubmission, mainEngine git.Engine) (submissionPublishInfo, error) {
	info := submissionPublishInfo{}
	if submission.Status != domain.SubmissionStatusSucceeded {
		return info, nil
	}

	events, err := store.ListEventsForItem(ctx, repoID, string(domain.ItemTypeIntegrationSubmission), submission.ID, 20)
	if err != nil {
		return info, err
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].EventType != domain.EventTypeIntegrationSucceeded {
			continue
		}
		var payload struct {
			ProtectedSHA string `json:"protected_sha"`
		}
		if len(events[i].Payload) > 0 {
			if err := json.Unmarshal(events[i].Payload, &payload); err != nil {
				return info, err
			}
		}
		info.ProtectedSHA = payload.ProtectedSHA
		break
	}
	if info.ProtectedSHA == "" {
		info.Outcome = submissionOutcomeIntegrated
		return info, nil
	}

	requests, err := store.ListPublishRequests(ctx, repoID)
	if err != nil {
		return info, err
	}
	for i := len(requests) - 1; i >= 0; i-- {
		if requests[i].TargetSHA != info.ProtectedSHA {
			continue
		}
		info.PublishRequestID = requests[i].ID
		info.PublishStatus = string(requests[i].Status)
		break
	}

	if info.PublishStatus == string(domain.PublishStatusSucceeded) {
		info.Outcome = submissionOutcomeLanded
	} else {
		info.Outcome = submissionOutcomeIntegrated
	}
	if info.PublishRequestID != 0 && info.PublishStatus == string(domain.PublishStatusFailed) {
		failure, err := resolvePublishFailureInfo(ctx, store, repoID, info.PublishRequestID, mainEngine)
		if err != nil {
			return info, err
		}
		info.Failure = failure
	}
	return info, nil
}

func resolvePublishFailureInfo(ctx context.Context, store state.Store, repoID int64, publishRequestID int64, mainEngine git.Engine) (publishFailureInfo, error) {
	events, err := store.ListEventsForItem(ctx, repoID, string(domain.ItemTypePublishRequest), publishRequestID, 10)
	if err != nil {
		return publishFailureInfo{}, err
	}

	var info publishFailureInfo
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].EventType != domain.EventTypePublishFailed {
			continue
		}
		var payload struct {
			Error string `json:"error"`
		}
		if len(events[i].Payload) > 0 {
			if err := json.Unmarshal(events[i].Payload, &payload); err != nil {
				return publishFailureInfo{}, err
			}
		}
		info.Error = strings.TrimSpace(payload.Error)
		break
	}

	if dirtyPaths, err := mainEngine.WorktreeDirtyPaths(mainEngine.RepositoryRoot); err == nil && len(dirtyPaths) > 0 {
		info.Cause = "protected_root_dirty"
		info.Summary = "publish failed because the protected root checkout is dirty"
		info.RetryHint = "clean-protected-root-then-retry-publish"
		info.ResubmitRequired = false
		return info, nil
	}

	if info.Error != "" {
		info.Cause = "publish_rejected"
		info.Summary = summarizePublishFailure(info.Error)
		info.RetryHint = "inspect-publish-failure-and-retry"
		info.ResubmitRequired = false
	}
	return info, nil
}

func summarizePublishFailure(message string) string {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return ""
	}
	lines := strings.Split(trimmed, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, "error: failed to push some refs") {
			continue
		}
		return line
	}
	return trimmed
}
