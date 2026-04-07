package app

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/recallnet/mainline/internal/domain"
	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/policy"
	"github.com/recallnet/mainline/internal/state"
)

type nextTarget string

const (
	nextTargetPush   nextTarget = "push"
	nextTargetCommit nextTarget = "commit"
)

type nextResult struct {
	RepositoryRoot        string         `json:"repository_root"`
	ProtectedBranch       string         `json:"protected_branch"`
	Target                nextTarget     `json:"target"`
	ProtectedSHA          string         `json:"protected_sha"`
	SubmissionID          int64          `json:"submission_id,omitempty"`
	PublishRequestID      int64          `json:"publish_request_id,omitempty"`
	Branch                string         `json:"branch,omitempty"`
	SourceRef             string         `json:"source_ref,omitempty"`
	RefKind               domain.RefKind `json:"ref_kind,omitempty"`
	CommitSubject         string         `json:"commit_subject,omitempty"`
	CommittedAt           time.Time      `json:"committed_at,omitempty"`
	QueueState            string         `json:"queue_state,omitempty"`
	QueueLength           int            `json:"queue_length,omitempty"`
	HasBlockedSubmissions bool           `json:"has_blocked_submissions,omitempty"`
	HasRunningPublishes   bool           `json:"has_running_publishes,omitempty"`
	HasRunningSubmissions bool           `json:"has_running_submissions,omitempty"`
	HasQueuedWork         bool           `json:"has_queued_work,omitempty"`
	ObservedAt            time.Time      `json:"observed_at"`
}

func runNext(args []string, stdout *stepPrinter, stderr io.Writer) error {
	target := nextTargetPush
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		target = nextTarget(args[0])
		args = args[1:]
	}

	fs := flag.NewFlagSet(currentCLIProgramName()+" next", flag.ContinueOnError)
	fs.SetOutput(stderr)
	setFlagUsage(fs, fmt.Sprintf(`Usage:
  %s next [commit|push] [flags]

Wait for the next protected-main advance.

Examples:
  mq next
  mq next push --json
  mq next commit --json --timeout 15m

Flags:
`, currentCLIProgramName()))

	var repoPath string
	var asJSON bool
	var timeout time.Duration
	var pollInterval time.Duration

	fs.StringVar(&repoPath, "repo", ".", "repository path")
	fs.BoolVar(&asJSON, "json", false, "output json")
	fs.DurationVar(&timeout, "timeout", 30*time.Minute, "maximum time to wait")
	fs.DurationVar(&pollInterval, "poll-interval", time.Second, "poll interval while waiting for new durable events")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if timeout <= 0 {
		return fmt.Errorf("timeout must be greater than zero")
	}
	if pollInterval <= 0 {
		return fmt.Errorf("poll-interval must be greater than zero")
	}

	switch fs.NArg() {
	case 0:
	case 1:
		target = nextTarget(fs.Arg(0))
	default:
		return fmt.Errorf("usage: %s next [commit|push] [flags]", currentCLIProgramName())
	}
	if target != nextTargetPush && target != nextTargetCommit {
		return fmt.Errorf("target must be %q or %q", nextTargetCommit, nextTargetPush)
	}

	result, waitErr := waitForNext(repoPath, target, timeout, pollInterval)
	if asJSON {
		encoder := json.NewEncoder(stdout.Raw())
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			return err
		}
		return waitErr
	}

	stdout.Section("Next %s", target)
	stdout.Line("Protected branch: %s", result.ProtectedBranch)
	stdout.Line("Protected SHA: %s", result.ProtectedSHA)
	if result.CommitSubject != "" {
		stdout.Line("Commit: %s", result.CommitSubject)
	}
	if result.Branch != "" {
		stdout.Line("Branch: %s", result.Branch)
	}
	if result.PublishRequestID != 0 {
		stdout.Line("Publish request: %d", result.PublishRequestID)
	}
	if result.SubmissionID != 0 {
		stdout.Line("Submission: %d", result.SubmissionID)
	}
	stdout.Line("Queue after: state=%s length=%d blocked=%t running_publishes=%t running_submissions=%t queued_work=%t",
		result.QueueState,
		result.QueueLength,
		result.HasBlockedSubmissions,
		result.HasRunningPublishes,
		result.HasRunningSubmissions,
		result.HasQueuedWork,
	)
	stdout.Line("Observed at: %s", result.ObservedAt.UTC().Format(time.RFC3339))
	return waitErr
}

func waitForNext(repoPath string, target nextTarget, timeout time.Duration, pollInterval time.Duration) (nextResult, error) {
	_, repoRoot, cfg, repoRecord, store, err := loadRepoContext(repoPath)
	if err != nil {
		return nextResult{}, err
	}
	engine := git.NewEngine(cfg.Repo.MainWorktree)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	lastID := int64(0)
	initial, err := store.ListEvents(ctx, repoRecord.ID, 1)
	if err != nil {
		return nextResult{}, err
	}
	if len(initial) > 0 {
		lastID = initial[0].ID
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nextResult{
				RepositoryRoot:  repoRoot,
				ProtectedBranch: cfg.Repo.ProtectedBranch,
				Target:          target,
				ObservedAt:      time.Now().UTC(),
			}, exitWithCode(2, fmt.Errorf("timed out waiting for next %s on %s", target, cfg.Repo.ProtectedBranch))
		case <-ticker.C:
			events, err := store.ListEventsAfter(ctx, repoRecord.ID, lastID, 100)
			if err != nil {
				return nextResult{}, err
			}
			for _, event := range events {
				lastID = event.ID
				switch target {
				case nextTargetPush:
					if event.EventType != domain.EventTypePublishCompleted {
						continue
					}
					return buildNextPushResult(ctx, store, engine, repoRoot, cfg, event)
				case nextTargetCommit:
					if event.EventType != domain.EventTypeIntegrationSucceeded {
						continue
					}
					return buildNextCommitResult(engine, repoRoot, cfg, event)
				}
			}
		}
	}
}

func buildNextPushResult(ctx context.Context, store state.Store, engine git.Engine, repoRoot string, cfg policy.File, event state.EventRecord) (nextResult, error) {
	var payload struct {
		TargetSHA string `json:"target_sha"`
	}
	if len(event.Payload) > 0 {
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return nextResult{}, err
		}
	}
	info, err := engine.CommitInfo(payload.TargetSHA)
	if err != nil {
		return nextResult{}, err
	}
	result := nextResult{
		RepositoryRoot:   repoRoot,
		ProtectedBranch:  cfg.Repo.ProtectedBranch,
		Target:           nextTargetPush,
		ProtectedSHA:     info.SHA,
		PublishRequestID: event.ItemID.Int64,
		CommitSubject:    info.Subject,
		CommittedAt:      info.CommittedAt,
		ObservedAt:       time.Now().UTC(),
	}
	events, err := store.ListEventsForItem(ctx, event.RepoID, string(domain.ItemTypePublishRequest), event.ItemID.Int64, 10)
	if err != nil {
		return nextResult{}, err
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].EventType != domain.EventTypePublishRequested {
			continue
		}
		var requested struct {
			Branch    string         `json:"branch"`
			SourceRef string         `json:"source_ref"`
			RefKind   domain.RefKind `json:"ref_kind"`
		}
		if len(events[i].Payload) > 0 {
			if err := json.Unmarshal(events[i].Payload, &requested); err != nil {
				return nextResult{}, err
			}
		}
		result.Branch = requested.Branch
		result.SourceRef = requested.SourceRef
		result.RefKind = requested.RefKind
		break
	}
	if status, err := collectStatus(repoRoot, 0); err == nil {
		result.QueueState = status.QueueSummary.Headline
		result.QueueLength = status.QueueSummary.QueueLength
		result.HasBlockedSubmissions = status.QueueSummary.HasBlockedSubmissions
		result.HasRunningPublishes = status.QueueSummary.HasRunningPublishes
		result.HasRunningSubmissions = status.QueueSummary.HasRunningSubmissions
		result.HasQueuedWork = status.QueueSummary.HasQueuedWork
	}
	return result, nil
}

func buildNextCommitResult(engine git.Engine, repoRoot string, cfg policy.File, event state.EventRecord) (nextResult, error) {
	var payload struct {
		Branch       string         `json:"branch"`
		SourceRef    string         `json:"source_ref"`
		RefKind      domain.RefKind `json:"ref_kind"`
		ProtectedSHA string         `json:"protected_sha"`
	}
	if len(event.Payload) > 0 {
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return nextResult{}, err
		}
	}
	info, err := engine.CommitInfo(payload.ProtectedSHA)
	if err != nil {
		return nextResult{}, err
	}
	result := nextResult{
		RepositoryRoot:  repoRoot,
		ProtectedBranch: cfg.Repo.ProtectedBranch,
		Target:          nextTargetCommit,
		ProtectedSHA:    info.SHA,
		SubmissionID:    event.ItemID.Int64,
		Branch:          payload.Branch,
		SourceRef:       payload.SourceRef,
		RefKind:         payload.RefKind,
		CommitSubject:   info.Subject,
		CommittedAt:     info.CommittedAt,
		ObservedAt:      time.Now().UTC(),
	}
	if status, err := collectStatus(repoRoot, 0); err == nil {
		result.QueueState = status.QueueSummary.Headline
		result.QueueLength = status.QueueSummary.QueueLength
		result.HasBlockedSubmissions = status.QueueSummary.HasBlockedSubmissions
		result.HasRunningPublishes = status.QueueSummary.HasRunningPublishes
		result.HasRunningSubmissions = status.QueueSummary.HasRunningSubmissions
		result.HasQueuedWork = status.QueueSummary.HasQueuedWork
	}
	return result, nil
}
