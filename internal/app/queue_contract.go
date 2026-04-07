package app

type queueSummary struct {
	Headline              string `json:"headline"`
	QueueLength           int    `json:"queue_length"`
	HasBlockedSubmissions bool   `json:"has_blocked_submissions"`
	HasRunningPublishes   bool   `json:"has_running_publishes"`
	HasRunningSubmissions bool   `json:"has_running_submissions"`
	HasQueuedWork         bool   `json:"has_queued_work"`
}

type queueSnapshot struct {
	Counts          statusCounts `json:"counts"`
	Summary         queueSummary `json:"summary"`
	UnfinishedItems []string     `json:"unfinished_items"`
}
