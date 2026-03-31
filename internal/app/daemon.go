package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type daemonOptions struct {
	repoPath  string
	interval  time.Duration
	maxCycles int
	jsonLogs  bool
	idleExit  bool
}

type daemonLog struct {
	Level     string `json:"level"`
	Event     string `json:"event"`
	Repo      string `json:"repo,omitempty"`
	Cycle     int    `json:"cycle,omitempty"`
	Message   string `json:"message,omitempty"`
	Timestamp string `json:"timestamp"`
}

// RunDaemon executes the mainlined command-line interface.
func RunDaemon(args []string) error {
	return runDaemon(args, os.Stdout, os.Stderr)
}

func runDaemon(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet("mainlined", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var showHelp bool
	opts := daemonOptions{
		repoPath: ".",
		interval: time.Second,
	}

	fs.BoolVar(&showHelp, "help", false, "show help")
	fs.BoolVar(&showHelp, "h", false, "show help")
	fs.StringVar(&opts.repoPath, "repo", opts.repoPath, "repository path")
	fs.DurationVar(&opts.interval, "interval", opts.interval, "poll interval")
	fs.IntVar(&opts.maxCycles, "max-cycles", 0, "stop after N worker cycles (0 means run forever)")
	fs.BoolVar(&opts.jsonLogs, "json", false, "emit structured json logs")
	fs.BoolVar(&opts.idleExit, "idle-exit", false, "exit after the first idle cycle")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printDaemonHelp(stdout)
			return nil
		}
		return err
	}

	if showHelp {
		printDaemonHelp(stdout)
		return nil
	}
	if opts.interval <= 0 {
		return fmt.Errorf("interval must be positive")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return runDaemonLoop(ctx, opts, stdout)
}

func runDaemonLoop(ctx context.Context, opts daemonOptions, stdout io.Writer) error {
	logDaemon(stdout, opts, "info", "daemon.started", 0, "starting worker loop")

	ticker := time.NewTicker(opts.interval)
	defer ticker.Stop()

	for cycle := 1; ; cycle++ {
		result, err := runOneCycle(opts.repoPath)
		if err != nil {
			logDaemon(stdout, opts, "error", "cycle.failed", cycle, err.Error())
			return err
		}

		logDaemon(stdout, opts, "info", "cycle.completed", cycle, result)

		idle := result == "No queued publish requests."
		if opts.idleExit && idle {
			logDaemon(stdout, opts, "info", "daemon.idle_exit", cycle, "no queued work")
			return nil
		}
		if opts.maxCycles > 0 && cycle >= opts.maxCycles {
			logDaemon(stdout, opts, "info", "daemon.max_cycles_reached", cycle, "stopping after configured cycles")
			return nil
		}

		select {
		case <-ctx.Done():
			logDaemon(stdout, opts, "info", "daemon.stopped", cycle, "shutdown requested")
			return nil
		case <-ticker.C:
		}
	}
}

func logDaemon(w io.Writer, opts daemonOptions, level string, event string, cycle int, message string) {
	if opts.jsonLogs {
		record := daemonLog{
			Level:     level,
			Event:     event,
			Repo:      opts.repoPath,
			Cycle:     cycle,
			Message:   message,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
		encoder := json.NewEncoder(w)
		_ = encoder.Encode(record)
		return
	}

	if cycle > 0 {
		fmt.Fprintf(w, "[%s] cycle=%d %s: %s\n", level, cycle, event, message)
		return
	}
	fmt.Fprintf(w, "[%s] %s: %s\n", level, event, message)
}

func printDaemonHelp(w io.Writer) {
	fmt.Fprint(w, daemonHelpText())
}

func daemonHelpText() string {
	return `mainlined runs the background worker loop for mainline.

Usage:
  mainlined [flags]

Flags:
  -h, --help            show help
  --repo string         repository path (default ".")
  --interval duration   poll interval (default 1s)
  --max-cycles int      stop after N cycles (default 0 means forever)
  --json                emit structured json logs
  --idle-exit           exit after the first idle cycle
`
}
