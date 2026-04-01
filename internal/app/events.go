package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/recallnet/mainline/internal/state"
)

type eventOptions struct {
	repoPath     string
	limit        int
	asJSON       bool
	lifecycle    bool
	follow       bool
	pollInterval time.Duration
	idleExit     bool
}

type lifecycleEmitter struct {
	repoRoot    string
	shaToBranch map[string]string
}

type lifecycleEvent struct {
	Event           string   `json:"event"`
	Timestamp       string   `json:"timestamp"`
	RepositoryRoot  string   `json:"repository_root"`
	Branch          string   `json:"branch,omitempty"`
	SHA             string   `json:"sha,omitempty"`
	SourceSHA       string   `json:"source_sha,omitempty"`
	SubmissionID    int64    `json:"submission_id,omitempty"`
	PublishID       int64    `json:"publish_request_id,omitempty"`
	SourceWorktree  string   `json:"source_worktree,omitempty"`
	Status          string   `json:"status,omitempty"`
	Error           string   `json:"error,omitempty"`
	BlockedReason   string   `json:"blocked_reason,omitempty"`
	ConflictFiles   []string `json:"conflict_files,omitempty"`
	ProtectedTipSHA string   `json:"protected_tip_sha,omitempty"`
	RetryHint       string   `json:"retry_hint,omitempty"`
}

func runEvents(args []string, stdout io.Writer, stderr io.Writer) error {
	return runEventCommand("mainline events", args, stdout, stderr)
}

func runLogs(args []string, stdout io.Writer, stderr io.Writer) error {
	return runEventCommand("mainline logs", args, stdout, stderr)
}

func runEventCommand(commandName string, args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet(commandName, flag.ContinueOnError)
	fs.SetOutput(stderr)
	setFlagUsage(fs, fmt.Sprintf(`Usage:
  %s [flags]

Replay or follow durable queue events. Add --lifecycle for stable branch-level
records that long-lived agents and daemons can consume directly.

Examples:
  mq events --repo /path/to/protected-main --follow --json --lifecycle
  mq logs --repo /path/to/protected-main --limit 50

Flags:
`, commandName))

	opts := eventOptions{
		repoPath:     ".",
		limit:        20,
		pollInterval: time.Second,
	}

	fs.StringVar(&opts.repoPath, "repo", opts.repoPath, "repository path")
	fs.IntVar(&opts.limit, "limit", opts.limit, "number of initial events to show")
	fs.BoolVar(&opts.asJSON, "json", false, "output json")
	fs.BoolVar(&opts.lifecycle, "lifecycle", false, "emit normalized branch lifecycle events")
	fs.BoolVar(&opts.follow, "follow", false, "stream new events")
	fs.DurationVar(&opts.pollInterval, "poll-interval", opts.pollInterval, "poll interval for --follow")
	fs.BoolVar(&opts.idleExit, "idle-exit", false, "exit after the first idle follow poll")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if opts.pollInterval <= 0 {
		return fmt.Errorf("poll-interval must be positive")
	}

	return runEventStream(context.Background(), opts, stdout)
}

func runEventStream(ctx context.Context, opts eventOptions, stdout io.Writer) error {
	_, _, _, repoRecord, store, err := loadRepoContext(opts.repoPath)
	if err != nil {
		return err
	}
	emitter := lifecycleEmitter{
		repoRoot:    repoRecord.CanonicalPath,
		shaToBranch: make(map[string]string),
	}

	initial, err := store.ListEvents(ctx, repoRecord.ID, opts.limit)
	if err != nil {
		return err
	}
	initial = reverseEvents(initial)
	lastID := int64(0)
	for _, event := range initial {
		if err := writeEvent(stdout, event, opts, &emitter); err != nil {
			return err
		}
		lastID = event.ID
	}

	if !opts.follow {
		return nil
	}

	ticker := time.NewTicker(opts.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			events, err := store.ListEventsAfter(ctx, repoRecord.ID, lastID, 100)
			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return nil
				}
				return err
			}
			if len(events) == 0 {
				if opts.idleExit {
					return nil
				}
				continue
			}
			for _, event := range events {
				if err := writeEvent(stdout, event, opts, &emitter); err != nil {
					return err
				}
				lastID = event.ID
			}
		}
	}
}

func reverseEvents(events []state.EventRecord) []state.EventRecord {
	if len(events) == 0 {
		return events
	}
	reversed := make([]state.EventRecord, 0, len(events))
	for i := len(events) - 1; i >= 0; i-- {
		reversed = append(reversed, events[i])
	}
	return reversed
}

func writeEvent(w io.Writer, event state.EventRecord, opts eventOptions, emitter *lifecycleEmitter) error {
	if opts.lifecycle {
		lifecycle, ok, err := emitter.project(event)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		encoder := json.NewEncoder(w)
		return encoder.Encode(lifecycle)
	}

	if opts.asJSON {
		encoder := json.NewEncoder(w)
		return encoder.Encode(event)
	}

	itemID := ""
	if event.ItemID.Valid {
		itemID = fmt.Sprintf("#%d", event.ItemID.Int64)
	}
	payload := strings.TrimSpace(string(event.Payload))
	if payload != "" && payload != "{}" {
		_, err := fmt.Fprintf(w, "%s  %s  %s%s  %s\n", event.CreatedAt.UTC().Format(time.RFC3339), event.EventType, event.ItemType, itemID, payload)
		return err
	}
	_, err := fmt.Fprintf(w, "%s  %s  %s%s\n", event.CreatedAt.UTC().Format(time.RFC3339), event.EventType, event.ItemType, itemID)
	return err
}

func (e *lifecycleEmitter) project(event state.EventRecord) (lifecycleEvent, bool, error) {
	record := lifecycleEvent{
		Timestamp:      event.CreatedAt.UTC().Format(time.RFC3339),
		RepositoryRoot: e.repoRoot,
	}
	if event.ItemID.Valid {
		switch event.ItemType {
		case "integration_submission":
			record.SubmissionID = event.ItemID.Int64
		case "publish_request":
			record.PublishID = event.ItemID.Int64
		}
	}

	var payload map[string]any
	if len(event.Payload) > 0 {
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return lifecycleEvent{}, false, err
		}
	}

	getString := func(key string) string {
		value, ok := payload[key]
		if !ok {
			return ""
		}
		text, _ := value.(string)
		return text
	}

	getStringSlice := func(key string) []string {
		value, ok := payload[key]
		if !ok {
			return nil
		}
		items, ok := value.([]any)
		if !ok {
			return nil
		}
		out := make([]string, 0, len(items))
		for _, item := range items {
			text, ok := item.(string)
			if ok && text != "" {
				out = append(out, text)
			}
		}
		return out
	}
	getRef := func() string {
		if branch := getString("branch"); branch != "" {
			return branch
		}
		if sourceRef := getString("source_ref"); sourceRef != "" {
			return sourceRef
		}
		if sourceSHA := getString("source_sha"); sourceSHA != "" {
			return sourceSHA
		}
		return getString("submitted_source")
	}

	switch event.EventType {
	case "submission.created":
		record.Event = "submitted"
		record.Status = "queued"
		record.Branch = getRef()
		record.SourceSHA = getString("source_sha")
		record.SourceWorktree = getString("source_worktree")
		return record, true, nil
	case "integration.started":
		record.Event = "integration_started"
		record.Status = "running"
		record.Branch = getRef()
		record.SourceSHA = getString("submitted_source")
		record.SourceWorktree = getString("source_worktree")
		return record, true, nil
	case "integration.blocked":
		record.Event = "blocked"
		record.Status = "blocked"
		record.Error = getString("error")
		record.BlockedReason = getString("blocked_reason")
		record.Branch = getRef()
		record.SourceWorktree = getString("source_worktree")
		record.ConflictFiles = getStringSlice("conflict_files")
		record.ProtectedTipSHA = getString("protected_tip_sha")
		record.RetryHint = getString("retry_hint")
		record.SHA = record.ProtectedTipSHA
		return record, true, nil
	case "integration.failed":
		record.Event = "failed"
		record.Status = "failed"
		record.Error = getString("error")
		record.Branch = getRef()
		return record, true, nil
	case "integration.succeeded":
		record.Event = "integrated"
		record.Status = "succeeded"
		record.Branch = getRef()
		record.SHA = getString("protected_sha")
		if record.SHA != "" && record.Branch != "" {
			e.shaToBranch[record.SHA] = record.Branch
		}
		return record, true, nil
	case "submission.retried":
		record.Event = "retried"
		record.Status = "queued"
		record.Branch = getRef()
		return record, true, nil
	case "submission.cancelled":
		record.Event = "cancelled"
		record.Status = "cancelled"
		record.Branch = getRef()
		return record, true, nil
	case "publish.requested":
		record.Event = "publish_requested"
		record.Status = "queued"
		record.SHA = getString("target_sha")
		record.Branch = getRef()
		if record.Branch == "" {
			record.Branch = e.shaToBranch[record.SHA]
		}
		if record.SHA != "" && record.Branch != "" {
			e.shaToBranch[record.SHA] = record.Branch
		}
		return record, true, nil
	case "publish.retry_scheduled":
		record.Event = "publish_retry_scheduled"
		record.Status = "queued"
		record.SHA = getString("target_sha")
		record.Branch = e.shaToBranch[record.SHA]
		record.Error = getString("error")
		return record, true, nil
	case "publish.completed":
		record.Event = "published"
		record.Status = "succeeded"
		record.SHA = getString("target_sha")
		record.Branch = e.shaToBranch[record.SHA]
		return record, true, nil
	case "publish.failed":
		record.Event = "publish_failed"
		record.Status = "failed"
		record.SHA = getString("target_sha")
		record.Branch = e.shaToBranch[record.SHA]
		record.Error = getString("error")
		return record, true, nil
	case "publish.retried":
		record.Event = "publish_retried"
		record.Status = "queued"
		record.SHA = getString("target_sha")
		record.Branch = e.shaToBranch[record.SHA]
		return record, true, nil
	case "publish.cancelled":
		record.Event = "publish_cancelled"
		record.Status = "cancelled"
		record.SHA = getString("target_sha")
		record.Branch = e.shaToBranch[record.SHA]
		return record, true, nil
	default:
		return lifecycleEvent{}, false, nil
	}
}
