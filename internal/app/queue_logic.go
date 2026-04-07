package app

import (
	"github.com/recallnet/mainline/internal/domain"
	"github.com/recallnet/mainline/internal/state"
)

func summarizeCounts(submissions []state.IntegrationSubmission, requests []state.PublishRequest) queueCounts {
	var counts queueCounts
	for _, submission := range submissions {
		switch submission.Status {
		case domain.SubmissionStatusQueued:
			counts.QueuedSubmissions++
		case domain.SubmissionStatusRunning:
			counts.RunningSubmissions++
		case domain.SubmissionStatusBlocked:
			counts.BlockSubmissions++
		case domain.SubmissionStatusFailed:
			counts.FailedSubmissions++
		case domain.SubmissionStatusCancelled:
			counts.CancelledSubmissions++
		case domain.SubmissionStatusSuperseded:
			// terminal and intentionally omitted from active queue counts
		}
	}
	for _, request := range requests {
		switch request.Status {
		case domain.PublishStatusQueued:
			counts.QueuedPublishes++
		case domain.PublishStatusRunning:
			counts.RunningPublishes++
		case domain.PublishStatusFailed:
			counts.FailedPublishes++
		case domain.PublishStatusCancelled:
			counts.CancelledPublishes++
		case domain.PublishStatusSucceeded:
			counts.SucceededPublishes++
		}
	}
	return counts
}

func summarizeQueue(counts queueCounts) queueSummary {
	queueLength := counts.QueuedSubmissions +
		counts.RunningSubmissions +
		counts.BlockSubmissions +
		counts.QueuedPublishes +
		counts.RunningPublishes
	summary := queueSummary{
		QueueLength:           queueLength,
		HasBlockedSubmissions: counts.BlockSubmissions > 0,
		HasRunningPublishes:   counts.RunningPublishes > 0,
		HasRunningSubmissions: counts.RunningSubmissions > 0,
		HasQueuedWork:         queueLength > 0,
	}
	switch {
	case counts.RunningPublishes > 0:
		summary.Headline = "publishing"
	case counts.BlockSubmissions > 0:
		summary.Headline = "blocked"
	case counts.RunningSubmissions > 0:
		summary.Headline = "integrating"
	case counts.QueuedSubmissions > 0 || counts.QueuedPublishes > 0:
		summary.Headline = "queued"
	default:
		summary.Headline = "idle"
		summary.QueueLength = 0
		summary.HasQueuedWork = false
	}
	return summary
}

func buildQueueAlerts(counts queueCounts) []string {
	var alerts []string
	if counts.RunningPublishes > 0 && counts.BlockSubmissions > 0 {
		alerts = append(alerts, "A publish is actively running. Separate blocked submissions still need attention, but they are not stopping the current publish.")
	}
	if counts.RunningSubmissions > 0 && counts.BlockSubmissions > 0 {
		alerts = append(alerts, "An integration is actively running. Separate blocked submissions still need attention.")
	}
	return alerts
}
