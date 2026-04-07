package app

type queueCounts struct {
	QueuedSubmissions    int `json:"queued_submissions"`
	RunningSubmissions   int `json:"running_submissions"`
	BlockSubmissions     int `json:"blocked_submissions"`
	FailedSubmissions    int `json:"failed_submissions"`
	CancelledSubmissions int `json:"cancelled_submissions"`
	QueuedPublishes      int `json:"queued_publishes"`
	RunningPublishes     int `json:"running_publishes"`
	FailedPublishes      int `json:"failed_publishes"`
	CancelledPublishes   int `json:"cancelled_publishes"`
	SucceededPublishes   int `json:"succeeded_publishes"`
}

type queueSummary struct {
	Headline              string `json:"headline"`
	QueueLength           int    `json:"queue_length"`
	HasBlockedSubmissions bool   `json:"has_blocked_submissions"`
	HasRunningPublishes   bool   `json:"has_running_publishes"`
	HasRunningSubmissions bool   `json:"has_running_submissions"`
	HasQueuedWork         bool   `json:"has_queued_work"`
}

type queueSnapshot struct {
	Counts          queueCounts  `json:"counts"`
	Summary         queueSummary `json:"summary"`
	UnfinishedItems []string     `json:"unfinished_items"`
}
