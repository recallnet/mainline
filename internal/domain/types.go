package domain

type RefKind string

const (
	RefKindBranch RefKind = "branch"
	RefKindSHA    RefKind = "sha"
)

type SubmissionStatus string

const (
	SubmissionStatusQueued     SubmissionStatus = "queued"
	SubmissionStatusRunning    SubmissionStatus = "running"
	SubmissionStatusBlocked    SubmissionStatus = "blocked"
	SubmissionStatusFailed     SubmissionStatus = "failed"
	SubmissionStatusSucceeded  SubmissionStatus = "succeeded"
	SubmissionStatusCancelled  SubmissionStatus = "cancelled"
	SubmissionStatusSuperseded SubmissionStatus = "superseded"
)

type PublishStatus string

const (
	PublishStatusQueued     PublishStatus = "queued"
	PublishStatusRunning    PublishStatus = "running"
	PublishStatusFailed     PublishStatus = "failed"
	PublishStatusSucceeded  PublishStatus = "succeeded"
	PublishStatusCancelled  PublishStatus = "cancelled"
	PublishStatusSuperseded PublishStatus = "superseded"
)

type SubmissionOutcome string

const (
	SubmissionOutcomeIntegrated SubmissionOutcome = "integrated"
	SubmissionOutcomeLanded     SubmissionOutcome = "landed"
	SubmissionOutcomeBlocked    SubmissionOutcome = "blocked"
)

type BlockedReason string

const (
	BlockedReasonCheckTimeout   BlockedReason = "check_timeout"
	BlockedReasonRebaseConflict BlockedReason = "rebase_conflict"
)

type ItemType string

const (
	ItemTypeRepository            ItemType = "repository"
	ItemTypeIntegrationSubmission ItemType = "integration_submission"
	ItemTypePublishRequest        ItemType = "publish_request"
)

type EventType string

const (
	EventTypeRepositoryInitialized       EventType = "repository.initialized"
	EventTypeSubmissionCreated           EventType = "submission.created"
	EventTypeSubmissionReprioritized     EventType = "submission.reprioritized"
	EventTypeProtectedSyncedFromUpstream EventType = "protected.synced_from_upstream"
	EventTypeIntegrationStarted          EventType = "integration.started"
	EventTypeIntegrationBlocked          EventType = "integration.blocked"
	EventTypeIntegrationFailed           EventType = "integration.failed"
	EventTypeIntegrationSucceeded        EventType = "integration.succeeded"
	EventTypeIntegrationRecovered        EventType = "integration.recovered"
	EventTypePublishRequested            EventType = "publish.requested"
	EventTypePublishStarted              EventType = "publish.started"
	EventTypePublishCompleted            EventType = "publish.completed"
	EventTypePublishFailed               EventType = "publish.failed"
	EventTypePublishSuperseded           EventType = "publish.superseded"
	EventTypePublishRetryScheduled       EventType = "publish.retry_scheduled"
	EventTypePublishRetried              EventType = "publish.retried"
	EventTypePublishCancelled            EventType = "publish.cancelled"
	EventTypePublishRecovered            EventType = "publish.recovered"
)
