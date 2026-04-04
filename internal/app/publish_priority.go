package app

import "github.com/recallnet/mainline/internal/state"

func publishPriorityRank(priority string) int {
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

func shouldPreemptPublish(running state.PublishRequest, candidate state.PublishRequest) bool {
	if candidate.ID == 0 || candidate.TargetSHA == "" {
		return false
	}
	if running.ID == candidate.ID || running.TargetSHA == candidate.TargetSHA {
		return false
	}
	return publishPriorityRank(candidate.Priority) <= publishPriorityRank(running.Priority)
}
