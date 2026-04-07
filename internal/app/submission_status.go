package app

import (
	"context"
	"encoding/json"
	"fmt"
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
		var payload publishFailurePayload
		if len(events[i].Payload) > 0 {
			if err := json.Unmarshal(events[i].Payload, &payload); err != nil {
				return publishFailureInfo{}, err
			}
		}
		info.Error = strings.TrimSpace(payload.Error)
		switch payload.Kind {
		case publishFailureKindPrepareFailed:
			info.Cause = publishFailureKindPrepareFailed
			info.Summary = "publish prepare step failed"
			info.RetryHint = "fix-prepare-step-then-retry-publish"
		case publishFailureKindPrepareDirtiedProtectedRoot:
			info.Cause = publishFailureKindPrepareDirtiedProtectedRoot
			info.Summary = "publish prepare step left tracked or non-ignored drift in the protected root checkout"
			info.RetryHint = "fix-prepare-step-then-retry-publish"
		case publishFailureKindValidateFailed:
			info.Cause = publishFailureKindValidateFailed
			info.Summary = "publish validation step failed"
			info.RetryHint = "fix-validation-then-retry-publish"
		case publishFailureKindInheritedHookFailed:
			info.Cause = publishFailureKindInheritedHookFailed
			info.Summary = "publish failed in an inherited git pre-push hook"
			info.RetryHint = "inspect-inherited-hook-and-retry"
		case publishFailureKindGitPushFailed:
			info.Cause = publishFailureKindGitPushFailed
			info.Summary = "git push failed during publish"
			info.RetryHint = "inspect-push-failure-and-retry"
		}
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
		if info.Cause == "" {
			info.Cause = "publish_rejected"
			info.Summary = summarizePublishFailure(info.Error)
			info.RetryHint = "inspect-publish-failure-and-retry"
		} else if info.Summary != "" {
			info.Summary = fmt.Sprintf("%s: %s", info.Summary, summarizePublishFailure(info.Error))
		}
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
