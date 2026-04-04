package app

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/recallnet/mainline/internal/domain"
	"github.com/recallnet/mainline/internal/policy"
	"github.com/recallnet/mainline/internal/state"
)

const executionEstimateWindow = 24 * time.Hour

type executionEstimate struct {
	WindowHours      int                      `json:"window_hours"`
	Basis            domain.SubmissionOutcome `json:"basis"`
	SampleCount      int                      `json:"sample_count"`
	AvgExecutionMS   int64                    `json:"avg_execution_ms,omitempty"`
	AvgIntegrationMS int64                    `json:"avg_integration_ms,omitempty"`
	AvgLandedMS      int64                    `json:"avg_landed_ms,omitempty"`
}

func collectExecutionEstimate(ctx context.Context, store state.Store, repoID int64, cfg policy.File, submissions []state.IntegrationSubmission) (executionEstimate, error) {
	cutoff := time.Now().UTC().Add(-executionEstimateWindow)
	var integrationDurations []int64
	var landedDurations []int64

	for _, submission := range submissions {
		if submission.Status != domain.SubmissionStatusSucceeded {
			continue
		}

		submissionEvents, err := store.ListEventsForItem(ctx, repoID, string(domain.ItemTypeIntegrationSubmission), submission.ID, 20)
		if err != nil {
			return executionEstimate{}, err
		}

		startedAt, succeededAt := integrationEventBounds(submissionEvents)
		if !startedAt.IsZero() && !succeededAt.IsZero() && !succeededAt.Before(cutoff) {
			integrationDurations = append(integrationDurations, succeededAt.Sub(startedAt).Milliseconds())
		}

		info, err := resolveSubmissionPublishInfo(ctx, store, repoID, submission)
		if err != nil {
			return executionEstimate{}, err
		}
		if info.PublishRequestID == 0 || info.PublishStatus != string(domain.PublishStatusSucceeded) || startedAt.IsZero() {
			continue
		}

		publishEvents, err := store.ListEventsForItem(ctx, repoID, string(domain.ItemTypePublishRequest), info.PublishRequestID, 20)
		if err != nil {
			return executionEstimate{}, err
		}
		completedAt := publishCompletedAt(publishEvents)
		if completedAt.IsZero() || completedAt.Before(cutoff) {
			continue
		}
		landedDurations = append(landedDurations, completedAt.Sub(startedAt).Milliseconds())
	}

	estimate := executionEstimate{
		WindowHours:      int(executionEstimateWindow / time.Hour),
		AvgIntegrationMS: averageDurationMS(integrationDurations),
		AvgLandedMS:      averageDurationMS(landedDurations),
	}
	if cfg.Publish.Mode == "auto" && len(landedDurations) > 0 {
		estimate.Basis = submissionOutcomeLanded
		estimate.SampleCount = len(landedDurations)
		estimate.AvgExecutionMS = estimate.AvgLandedMS
	} else {
		estimate.Basis = submissionOutcomeIntegrated
		estimate.SampleCount = len(integrationDurations)
		estimate.AvgExecutionMS = estimate.AvgIntegrationMS
	}
	return estimate, nil
}

func annotateQueueEstimates(submissions []statusSubmission, estimate executionEstimate) []statusSubmission {
	if len(submissions) == 0 || estimate.AvgExecutionMS <= 0 {
		return submissions
	}

	activeIndexes := make([]int, 0, len(submissions))
	for i, submission := range submissions {
		if submission.Status == domain.SubmissionStatusQueued || submission.Status == domain.SubmissionStatusRunning {
			activeIndexes = append(activeIndexes, i)
		}
	}
	sort.SliceStable(activeIndexes, func(i, j int) bool {
		left := submissions[activeIndexes[i]]
		right := submissions[activeIndexes[j]]
		return compareQueueOrder(left.integrationSubmission(), right.integrationSubmission()) < 0
	})
	for position, idx := range activeIndexes {
		submissions[idx].QueuePosition = position + 1
		submissions[idx].EstimatedCompletionMS = estimate.AvgExecutionMS * int64(position+1)
		submissions[idx].EstimateBasis = estimate.Basis
	}
	return submissions
}

func submissionQueueEstimate(ctx context.Context, queued queuedSubmission) (executionEstimate, int, error) {
	submissions, err := queued.Store.ListIntegrationSubmissions(ctx, queued.RepoRecord.ID)
	if err != nil {
		return executionEstimate{}, 0, err
	}
	estimate, err := collectExecutionEstimate(ctx, queued.Store, queued.RepoRecord.ID, queued.Config, submissions)
	if err != nil {
		return executionEstimate{}, 0, err
	}
	if estimate.AvgExecutionMS <= 0 {
		return estimate, 0, nil
	}
	active := make([]state.IntegrationSubmission, 0, len(submissions))
	seenQueuedSubmission := false
	for _, submission := range submissions {
		if submission.ID == queued.Submission.ID {
			seenQueuedSubmission = true
		}
		if submission.Status == domain.SubmissionStatusQueued || submission.Status == domain.SubmissionStatusRunning {
			active = append(active, submission)
		}
	}
	if !seenQueuedSubmission && (queued.Submission.Status == domain.SubmissionStatusQueued || queued.Submission.Status == domain.SubmissionStatusRunning) {
		active = append(active, queued.Submission)
	}
	sort.SliceStable(active, func(i, j int) bool {
		return compareQueueOrder(active[i], active[j]) < 0
	})
	for idx, submission := range active {
		if submission.ID == queued.Submission.ID {
			return estimate, idx + 1, nil
		}
	}
	return estimate, 0, nil
}

func compareQueueOrder(left state.IntegrationSubmission, right state.IntegrationSubmission) int {
	if left.Status == domain.SubmissionStatusRunning && right.Status != domain.SubmissionStatusRunning {
		return -1
	}
	if right.Status == domain.SubmissionStatusRunning && left.Status != domain.SubmissionStatusRunning {
		return 1
	}

	leftPriority := submissionPriorityRank(left.Priority)
	rightPriority := submissionPriorityRank(right.Priority)
	if leftPriority != rightPriority {
		if leftPriority < rightPriority {
			return -1
		}
		return 1
	}
	if !left.CreatedAt.Equal(right.CreatedAt) {
		if left.CreatedAt.Before(right.CreatedAt) {
			return -1
		}
		return 1
	}
	if left.ID < right.ID {
		return -1
	}
	if left.ID > right.ID {
		return 1
	}
	return 0
}

func submissionPriorityRank(priority string) int {
	switch priority {
	case submissionPriorityHigh:
		return 0
	case submissionPriorityNormal, "":
		return 1
	case submissionPriorityLow:
		return 2
	default:
		return 3
	}
}

func integrationEventBounds(events []state.EventRecord) (time.Time, time.Time) {
	var startedAt time.Time
	var succeededAt time.Time
	slices.Reverse(events)
	for _, event := range events {
		switch event.EventType {
		case domain.EventTypeIntegrationStarted:
			startedAt = event.CreatedAt
		case domain.EventTypeIntegrationSucceeded:
			succeededAt = event.CreatedAt
		}
	}
	return startedAt, succeededAt
}

func publishCompletedAt(events []state.EventRecord) time.Time {
	slices.Reverse(events)
	for _, event := range events {
		if event.EventType == domain.EventTypePublishCompleted {
			return event.CreatedAt
		}
	}
	return time.Time{}
}

func averageDurationMS(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	var total int64
	for _, value := range values {
		total += value
	}
	return total / int64(len(values))
}
