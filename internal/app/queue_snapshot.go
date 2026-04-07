package app

import (
	"context"

	"github.com/recallnet/mainline/internal/state"
)

type queueSnapshot struct {
	Counts          statusCounts `json:"counts"`
	Summary         queueSummary `json:"summary"`
	UnfinishedItems []string     `json:"unfinished_items"`
}

func loadQueueSnapshot(store state.Store, repoID int64) (queueSnapshot, error) {
	ctx := context.Background()
	submissions, err := store.ListIntegrationSubmissions(ctx, repoID)
	if err != nil {
		return queueSnapshot{}, err
	}
	requests, err := store.ListPublishRequests(ctx, repoID)
	if err != nil {
		return queueSnapshot{}, err
	}
	items, err := store.ListUnfinishedItems(ctx, repoID)
	if err != nil {
		return queueSnapshot{}, err
	}
	counts := summarizeCounts(submissions, requests)
	return queueSnapshot{
		Counts:          counts,
		Summary:         summarizeQueue(counts),
		UnfinishedItems: items,
	}, nil
}
