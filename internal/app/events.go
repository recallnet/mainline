package app

import (
	"context"
	"encoding/json"
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
	follow       bool
	pollInterval time.Duration
	idleExit     bool
}

func runEvents(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet("mainline events", flag.ContinueOnError)
	fs.SetOutput(stderr)

	opts := eventOptions{
		repoPath:     ".",
		limit:        20,
		pollInterval: time.Second,
	}

	fs.StringVar(&opts.repoPath, "repo", opts.repoPath, "repository path")
	fs.IntVar(&opts.limit, "limit", opts.limit, "number of initial events to show")
	fs.BoolVar(&opts.asJSON, "json", false, "output json")
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

	initial, err := store.ListEvents(ctx, repoRecord.ID, opts.limit)
	if err != nil {
		return err
	}
	initial = reverseEvents(initial)
	lastID := int64(0)
	for _, event := range initial {
		if err := writeEvent(stdout, event, opts.asJSON); err != nil {
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
				return err
			}
			if len(events) == 0 {
				if opts.idleExit {
					return nil
				}
				continue
			}
			for _, event := range events {
				if err := writeEvent(stdout, event, opts.asJSON); err != nil {
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

func writeEvent(w io.Writer, event state.EventRecord, asJSON bool) error {
	if asJSON {
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
