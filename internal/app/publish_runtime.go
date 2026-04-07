package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/recallnet/mainline/internal/policy"
	"github.com/recallnet/mainline/internal/state"
)

const (
	publishStagePrepare  = "prepare"
	publishStageValidate = "validate"
	publishStagePush     = "push"
)

const (
	publishFailureKindPrepareFailed               = "prepare_failed"
	publishFailureKindPrepareDirtiedProtectedRoot = "prepare_dirtied_protected_root"
	publishFailureKindValidateFailed              = "validate_failed"
	publishFailureKindInheritedHookFailed         = "inherited_hook_failed"
	publishFailureKindGitPushFailed               = "git_push_failed"
)

type publishExecutionPolicy struct {
	ConfiguredHookPolicy   string `json:"configured_hook_policy"`
	EffectiveHookPolicy    string `json:"effective_hook_policy"`
	HooksBypassedForPush   bool   `json:"hooks_bypassed_for_push"`
	PreparePublishEnabled  bool   `json:"prepare_publish_enabled"`
	ValidatePublishEnabled bool   `json:"validate_publish_enabled"`
}

type protectedWorktreeActivity struct {
	Domain       string    `json:"domain"`
	Stage        string    `json:"stage,omitempty"`
	Owner        string    `json:"owner"`
	RequestID    int64     `json:"request_id,omitempty"`
	PID          int       `json:"pid,omitempty"`
	StartedAt    time.Time `json:"started_at"`
	MainWorktree string    `json:"main_worktree"`
	Summary      string    `json:"summary"`
}

type publishFailurePayload struct {
	TargetSHA            string `json:"target_sha"`
	Error                string `json:"error"`
	Stage                string `json:"stage,omitempty"`
	Kind                 string `json:"kind,omitempty"`
	ConfiguredHookPolicy string `json:"configured_hook_policy,omitempty"`
	EffectiveHookPolicy  string `json:"effective_hook_policy,omitempty"`
	HooksBypassed        bool   `json:"hooks_bypassed,omitempty"`
}

func buildPublishExecutionPolicy(cfg policy.File) publishExecutionPolicy {
	prepare, validate := publishCheckStages(cfg.Checks)
	effective := "inherit"
	if shouldBypassGitHooks(cfg) {
		effective = "replace-with-mainline-checks"
		if strings.TrimSpace(cfg.Repo.HookPolicy) == "bypass-with-explicit-command" {
			effective = "bypass-with-explicit-command"
		}
	}
	return publishExecutionPolicy{
		ConfiguredHookPolicy:   strings.TrimSpace(cfg.Repo.HookPolicy),
		EffectiveHookPolicy:    effective,
		HooksBypassedForPush:   shouldBypassGitHooks(cfg),
		PreparePublishEnabled:  len(prepare) > 0,
		ValidatePublishEnabled: len(validate) > 0,
	}
}

func buildProtectedWorktreeActivity(mainWorktree string, integrationWorker *state.LeaseMetadata, publishWorker *state.LeaseMetadata) *protectedWorktreeActivity {
	if publishWorker != nil {
		return leaseToProtectedWorktreeActivity(mainWorktree, publishWorker)
	}
	if integrationWorker != nil {
		return leaseToProtectedWorktreeActivity(mainWorktree, integrationWorker)
	}
	return nil
}

func leaseToProtectedWorktreeActivity(mainWorktree string, metadata *state.LeaseMetadata) *protectedWorktreeActivity {
	if metadata == nil {
		return nil
	}
	stageSuffix := ""
	if metadata.Stage != "" {
		stageSuffix = fmt.Sprintf(" (%s)", metadata.Stage)
	}
	summary := fmt.Sprintf("%s request #%d%s is actively operating in the protected worktree", metadata.Domain, metadata.RequestID, stageSuffix)
	if metadata.RequestID == 0 {
		summary = fmt.Sprintf("%s%s is actively operating in the protected worktree", metadata.Domain, stageSuffix)
	}
	return &protectedWorktreeActivity{
		Domain:       metadata.Domain,
		Stage:        metadata.Stage,
		Owner:        metadata.Owner,
		RequestID:    metadata.RequestID,
		PID:          metadata.PID,
		StartedAt:    metadata.CreatedAt,
		MainWorktree: mainWorktree,
		Summary:      summary,
	}
}

func classifyPushFailure(cfg policy.File, cause error) string {
	if cause == nil {
		return publishFailureKindGitPushFailed
	}
	text := strings.ToLower(cause.Error())
	if !shouldBypassGitHooks(cfg) {
		for _, indicator := range []string{"pre-push", "pre-push hook", "pre-push script failed", "hook declined", "rejected by hook"} {
			if strings.Contains(text, indicator) {
				return publishFailureKindInheritedHookFailed
			}
		}
	}
	return publishFailureKindGitPushFailed
}
