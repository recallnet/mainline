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
	repoPath     string
	registryPath string
	allRepos     bool
	interval     time.Duration
	maxCycles    int
	jsonLogs     bool
	idleExit     bool
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
	return RunDaemonWithName("mainlined", args)
}

// RunDaemonWithName executes the daemon CLI using the provided program name.
func RunDaemonWithName(programName string, args []string) error {
	return runDaemonWithName(programName, args, newStepPrinter(os.Stdout), os.Stderr)
}

func runDaemon(args []string, stdout *stepPrinter, stderr io.Writer) error {
	return runDaemonWithName("mainlined", args, stdout, stderr)
}

func runDaemonWithName(programName string, args []string, stdout *stepPrinter, stderr io.Writer) error {
	fs := flag.NewFlagSet(programName, flag.ContinueOnError)
	fs.SetOutput(stderr)

	var showHelp bool
	var showVersion bool
	opts := daemonOptions{
		repoPath: ".",
		interval: time.Second,
	}
	if path, err := globalRegistryPath(); err == nil {
		opts.registryPath = path
	}

	fs.BoolVar(&showHelp, "help", false, "show help")
	fs.BoolVar(&showHelp, "h", false, "show help")
	fs.BoolVar(&showVersion, "version", false, "show version")
	fs.StringVar(&opts.repoPath, "repo", opts.repoPath, "repository path")
	fs.BoolVar(&opts.allRepos, "all", false, "drain all registered repositories with one daemon")
	fs.StringVar(&opts.registryPath, "registry", opts.registryPath, "global registry path for --all mode")
	fs.DurationVar(&opts.interval, "interval", opts.interval, "poll interval")
	fs.IntVar(&opts.maxCycles, "max-cycles", 0, "stop after N worker cycles (0 means run forever)")
	fs.BoolVar(&opts.jsonLogs, "json", false, "emit structured json logs")
	fs.BoolVar(&opts.idleExit, "idle-exit", false, "exit after the first idle cycle")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printDaemonHelp(stdout.Raw(), programName)
			return nil
		}
		return err
	}

	if showHelp {
		printDaemonHelp(stdout.Raw(), programName)
		return nil
	}
	if showVersion {
		printVersion(stdout.Raw(), programName, false)
		return nil
	}
	if opts.interval <= 0 {
		return fmt.Errorf("interval must be positive")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return runDaemonLoop(ctx, opts, stdout.Raw())
}

func runDaemonLoop(ctx context.Context, opts daemonOptions, stdout io.Writer) error {
	if opts.allRepos {
		return runGlobalDaemonLoop(ctx, opts, stdout)
	}
	logDaemon(stdout, opts, "info", "daemon.started", 0, "starting worker loop")

	ticker := time.NewTicker(opts.interval)
	defer ticker.Stop()

	for cycle := 1; ; cycle++ {
		result, err := drainRepoUntilSettledContext(ctx, opts.repoPath)
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

func runGlobalDaemonLoop(ctx context.Context, opts daemonOptions, stdout io.Writer) error {
	if opts.registryPath == "" {
		path, err := globalRegistryPath()
		if err != nil {
			return err
		}
		opts.registryPath = path
	}
	logDaemon(stdout, opts, "info", "daemon.started", 0, "starting global worker loop")

	ticker := time.NewTicker(opts.interval)
	defer ticker.Stop()

	for cycle := 1; ; cycle++ {
		repos, err := loadRegisteredReposFromPath(opts.registryPath)
		if err != nil {
			return err
		}
		worked := false
		for _, repo := range repos {
			result, err := drainRepoUntilSettled(repo.MainWorktree)
			if err != nil {
				logDaemonForRepo(stdout, opts, repo.MainWorktree, "error", "cycle.failed", cycle, err.Error())
				continue
			}
			logDaemonForRepo(stdout, opts, repo.MainWorktree, "info", "cycle.completed", cycle, result)
			if !isIdleWorkerResult(result) && !isBusyWorkerResult(result) {
				worked = true
			}
		}

		if opts.idleExit && !worked {
			logDaemon(stdout, opts, "info", "daemon.idle_exit", cycle, "no queued work across registered repos")
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
	logDaemonForRepo(w, opts, opts.repoPath, level, event, cycle, message)
}

func logDaemonForRepo(w io.Writer, opts daemonOptions, repo string, level string, event string, cycle int, message string) {
	if opts.jsonLogs {
		record := daemonLog{
			Level:     level,
			Event:     event,
			Repo:      repo,
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

func printDaemonHelp(w io.Writer, programName string) {
	fmt.Fprint(w, daemonHelpText(programName))
}

func daemonHelpText(programName string) string {
	return fmt.Sprintf(`%s runs the optional background worker loop for mainline.

Usage:
  %s [flags]

Recommended optional machine-wide mode:
  mainlined --all --json --interval 2s

Most repos do not require a standing daemon. Mutating commands such as submit,
publish, retry, and cancel now try to become the drainer themselves and keep
running until the repo is quiescent.

Flags:
  -h, --help            show help
  --version             show version
  --repo string         repository path (default ".")
  --all                 drain all registered repositories with one daemon
  --registry string     global registry path for --all mode
  --interval duration   poll interval (default 1s)
  --max-cycles int      stop after N cycles (default 0 means forever)
  --json                emit structured json logs
  --idle-exit           exit after the first idle cycle
`, programName, programName)
}
