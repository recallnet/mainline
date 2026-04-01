package app

import (
	"context"
	"encoding/json"

	"github.com/recallnet/mainline/internal/state"
)

const (
	submissionOutcomeIntegrated = "integrated"
	submissionOutcomeLanded     = "landed"
)

type submissionPublishInfo struct {
	ProtectedSHA     string
	PublishRequestID int64
	PublishStatus    string
	Outcome          string
}

func resolveSubmissionPublishInfo(ctx context.Context, store state.Store, repoID int64, submission state.IntegrationSubmission) (submissionPublishInfo, error) {
	info := submissionPublishInfo{}
	if submission.Status != "succeeded" {
		return info, nil
	}

	events, err := store.ListEventsForItem(ctx, repoID, "integration_submission", submission.ID, 20)
	if err != nil {
		return info, err
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].EventType != "integration.succeeded" {
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
		info.PublishStatus = requests[i].Status
		break
	}

	if info.PublishStatus == "succeeded" {
		info.Outcome = submissionOutcomeLanded
	} else {
		info.Outcome = submissionOutcomeIntegrated
	}
	return info, nil
}
