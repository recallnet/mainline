package app

import (
	"context"

	"github.com/recallnet/mainline/internal/domain"
	"github.com/recallnet/mainline/internal/state"
)

func appendSubmissionLifecycleEvent(ctx context.Context, store state.Store, repoID int64, submissionID int64, eventType domain.EventType, payload domain.SubmissionLifecyclePayload) error {
	return appendStateEvent(ctx, store, state.EventRecord{
		RepoID:    repoID,
		ItemType:  domain.ItemTypeIntegrationSubmission,
		ItemID:    state.NullInt64(submissionID),
		EventType: eventType,
		Payload:   mustJSON(payload),
	})
}

func appendIntegrationBlockedEvent(ctx context.Context, store state.Store, repoID int64, submissionID int64, payload domain.IntegrationBlockedPayload) error {
	return appendStateEvent(ctx, store, state.EventRecord{
		RepoID:    repoID,
		ItemType:  domain.ItemTypeIntegrationSubmission,
		ItemID:    state.NullInt64(submissionID),
		EventType: domain.EventTypeIntegrationBlocked,
		Payload:   mustJSON(payload),
	})
}
