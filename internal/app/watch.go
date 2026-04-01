package app

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"
)

type watchOptions struct {
	repoPath   string
	interval   time.Duration
	eventLimit int
	maxCycles  int
	asJSON     bool
}

type watchFrame struct {
	ObservedAt time.Time    `json:"observed_at"`
	Status     statusResult `json:"status"`
}

func runWatch(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet(currentCLIProgramName()+" watch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	setFlagUsage(fs, fmt.Sprintf(`Usage:
  %s watch [flags]

Continuously refresh protected-branch and queue state.

Examples:
  mq watch --repo /path/to/protected-main
  mq watch --repo /path/to/protected-main --json --interval 1s

Flags:
`, currentCLIProgramName()))

	opts := watchOptions{
		repoPath:   ".",
		interval:   2 * time.Second,
		eventLimit: 5,
	}

	fs.StringVar(&opts.repoPath, "repo", opts.repoPath, "repository path")
	fs.DurationVar(&opts.interval, "interval", opts.interval, "refresh interval")
	fs.IntVar(&opts.eventLimit, "events", opts.eventLimit, "number of recent events to show in each snapshot")
	fs.IntVar(&opts.maxCycles, "max-cycles", opts.maxCycles, "maximum number of refresh cycles before exiting")
	fs.BoolVar(&opts.asJSON, "json", false, "output ndjson snapshots instead of a terminal view")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if opts.interval <= 0 {
		return fmt.Errorf("interval must be positive")
	}
	if opts.eventLimit < 0 {
		return fmt.Errorf("events must be zero or greater")
	}
	if opts.maxCycles < 0 {
		return fmt.Errorf("max-cycles must be zero or greater")
	}

	return runWatchLoop(context.Background(), opts, stdout)
}

func runWatchLoop(ctx context.Context, opts watchOptions, stdout io.Writer) error {
	if opts.maxCycles == 1 {
		return renderWatchCycle(stdout, opts)
	}

	ticker := time.NewTicker(opts.interval)
	defer ticker.Stop()

	completed := 0
	for {
		if err := renderWatchCycle(stdout, opts); err != nil {
			return err
		}
		completed++
		if opts.maxCycles > 0 && completed >= opts.maxCycles {
			return nil
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func renderWatchCycle(stdout io.Writer, opts watchOptions) error {
	result, err := collectStatus(opts.repoPath, opts.eventLimit)
	if err != nil {
		return err
	}

	if opts.asJSON {
		return json.NewEncoder(stdout).Encode(watchFrame{
			ObservedAt: time.Now().UTC(),
			Status:     result,
		})
	}

	if _, err := fmt.Fprint(stdout, "\x1b[H\x1b[2J"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "mainline watch  %s\n\n", time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return renderStatus(stdout, result)
}
