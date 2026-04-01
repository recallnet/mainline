package app

import "github.com/recallnet/mainline/internal/state"

const (
	submissionRefKindBranch = "branch"
	submissionRefKindSHA    = "sha"
)

func submissionDisplayRef(submission state.IntegrationSubmission) string {
	if submission.BranchName != "" {
		return submission.BranchName
	}
	if submission.SourceRef != "" {
		return submission.SourceRef
	}
	return submission.SourceSHA
}

func preparedSubmissionDisplayRef(prepared preparedSubmission) string {
	if prepared.Branch != "" {
		return prepared.Branch
	}
	if prepared.SourceRef != "" {
		return prepared.SourceRef
	}
	return prepared.SourceSHA
}
