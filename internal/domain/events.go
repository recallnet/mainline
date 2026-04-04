package domain

type RepositoryInitializedPayload struct {
	ProtectedBranch string `json:"protected_branch"`
	MainWorktree    string `json:"main_worktree"`
}

type ProtectedSyncedPayload struct {
	ProtectedBranch string `json:"protected_branch"`
	Upstream        string `json:"upstream"`
	BeforeSHA       string `json:"before_sha"`
	AfterSHA        string `json:"after_sha"`
}

type SubmissionLifecyclePayload struct {
	Branch         string  `json:"branch,omitempty"`
	SourceRef      string  `json:"source_ref,omitempty"`
	RefKind        RefKind `json:"ref_kind,omitempty"`
	SourceWorktree string  `json:"source_worktree,omitempty"`
	SourceSHA      string  `json:"source_sha,omitempty"`
	ProtectedSHA   string  `json:"protected_sha,omitempty"`
	Error          string  `json:"error,omitempty"`
}

type IntegrationBlockedPayload struct {
	Error             string        `json:"error"`
	Branch            string        `json:"branch,omitempty"`
	SourceRef         string        `json:"source_ref,omitempty"`
	RefKind           RefKind       `json:"ref_kind,omitempty"`
	BlockedReason     BlockedReason `json:"blocked_reason,omitempty"`
	ConflictFiles     []string      `json:"conflict_files,omitempty"`
	ProtectedTipSHA   string        `json:"protected_tip_sha,omitempty"`
	RetryHint         string        `json:"retry_hint,omitempty"`
	RetryRecommended  bool          `json:"retry_recommended"`
	SourceWorktree    string        `json:"source_worktree,omitempty"`
	ProtectedBranch   string        `json:"protected_branch,omitempty"`
	ProtectedUpstream string        `json:"protected_upstream,omitempty"`
	Timeout           string        `json:"timeout,omitempty"`
}

type PublishRequestedPayload struct {
	TargetSHA string  `json:"target_sha"`
	Reason    string  `json:"reason"`
	Branch    string  `json:"branch,omitempty"`
	SourceRef string  `json:"source_ref,omitempty"`
	RefKind   RefKind `json:"ref_kind,omitempty"`
}

type PublishStartedPayload struct {
	TargetSHA string `json:"target_sha"`
}

type PublishFailurePayload struct {
	TargetSHA string `json:"target_sha"`
	Error     string `json:"error"`
}

type PublishSupersededPayload struct {
	TargetSHA       string `json:"target_sha"`
	NewProtectedSHA string `json:"new_protected_sha,omitempty"`
	Reason          string `json:"reason"`
}

type PublishRetryScheduledPayload struct {
	TargetSHA        string `json:"target_sha"`
	Error            string `json:"error"`
	AttemptCount     int    `json:"attempt_count"`
	NextAttemptAt    string `json:"next_attempt_at"`
	BackoffSeconds   int    `json:"backoff_seconds"`
	RetryRecommended bool   `json:"retry_recommended"`
}
