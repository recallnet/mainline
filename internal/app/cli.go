package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

var activeCLIProgramName = "mainline"

var cliCommands = []string{
	"land",
	"submit",
	"status",
	"confidence",
	"run-once",
	"wait",
	"retry",
	"cancel",
	"publish",
	"logs",
	"watch",
	"events",
	"doctor",
	"completion",
	"version",
	"config edit",
	"registry prune",
	"repo audit",
	"repo init",
	"repo root",
	"repo show",
}

// RunCLI executes the mainline command-line interface.
func RunCLI(args []string) error {
	return RunCLIWithName("mainline", args)
}

// RunCLIWithName executes the CLI using the provided program name.
func RunCLIWithName(programName string, args []string) error {
	return runCLIWithName(programName, args, os.Stdout, os.Stderr)
}

func runCLI(args []string, stdout io.Writer, stderr io.Writer) error {
	return runCLIWithName("mainline", args, stdout, stderr)
}

func runCLIWithName(programName string, args []string, stdout io.Writer, stderr io.Writer) error {
	previousProgramName := activeCLIProgramName
	activeCLIProgramName = programName
	defer func() {
		activeCLIProgramName = previousProgramName
	}()

	fs := flag.NewFlagSet(programName, flag.ContinueOnError)
	fs.SetOutput(stderr)

	var showHelp bool
	var showVersion bool
	var asJSON bool
	fs.BoolVar(&showHelp, "help", false, "show help")
	fs.BoolVar(&showHelp, "h", false, "show help")
	fs.BoolVar(&showVersion, "version", false, "show version")
	fs.BoolVar(&asJSON, "json", false, "output json where supported")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCLIHelp(stdout, programName)
			return nil
		}
		return err
	}

	if showHelp {
		printCLIHelp(stdout, programName)
		return nil
	}
	if showVersion {
		printVersion(stdout, programName, asJSON)
		return nil
	}

	remaining := fs.Args()
	if len(remaining) == 0 {
		printCLIHelp(stdout, programName)
		return nil
	}

	command, commandArgs := parseCLICommand(remaining)
	if !isKnownCLICommand(command) {
		return fmt.Errorf("unknown command %q\n\n%s", command, cliHelpText(programName))
	}
	if command == "version" {
		printVersion(stdout, programName, asJSON)
		return nil
	}

	if asJSON {
		commandArgs = appendJSONFlag(commandArgs)
	}

	err := handleCommand(command, commandArgs, stdout, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}

func printCLIHelp(w io.Writer, programName string) {
	fmt.Fprint(w, cliHelpText(programName))
}

func cliHelpText(programName string) string {
	return fmt.Sprintf(`%s coordinates local protected-branch integrations and publishes.

Usage:
  %s [--json] <command> [flags]

Turbo paths:
  Agent in a topic worktree:
    %s submit --check-only --json
    %s submit --wait --timeout 15m --json
    # submit --wait stops at integrated; use land or wait --for landed for remote publish
    # plain submit now tries to become the drainer and stays alive until the repo is quiet
    # use submit --queue-only only when you explicitly want a different process to do the drain

  Controller or factory caller:
    %s land --json --timeout 30m
    %s submit --wait --for landed --timeout 30m --json
    %s wait --submission 42 --for landed --json --timeout 30m
    %s events --follow --json --lifecycle --repo /path/to/repo-root

  Operator:
    %s status --repo /path/to/repo-root --json
    %s doctor --fix --repo /path/to/repo-root --json

Initialize once per repo:
  %s repo init --repo /path/to/repo-root
  %s repo root --repo /path/to/repo-root --json
  git add mainline.toml && git commit -m "Initialize mainline repo policy"
  ./scripts/install-hooks.sh

Commands:
  land          submit and wait for integrate plus publish
  submit        queue a topic worktree or detached sha
  status        show queue and protected-branch state
  confidence    summarize evidence and promotion gates
  run-once      run one integration or publish cycle
  wait          wait on a submission id for integration or landed outcome
  retry         requeue a blocked, failed, or cancelled item
  cancel        cancel a queued, blocked, or failed item
  publish       queue publish of the protected tip
  logs          replay durable event history
  watch         refresh status continuously
  events        stream durable events or lifecycle envelopes
  doctor        inspect and optionally repair stuck states
  completion    emit shell completion
  version       show build metadata
  config edit   open mainline.toml in an editor
  registry prune remove stale repo entries from the global registry
  repo init     initialize repo config and durable state
  repo audit    list local branches not yet merged into protected main
  repo root     inspect or adopt the canonical root checkout
  repo show     inspect repo config and worktrees

Use "%s <command> --help" for command-specific examples.
`, programName, programName, programName, programName, programName, programName, programName, programName, programName, programName, programName, programName, programName)
}

func isKnownCLICommand(command string) bool {
	for _, candidate := range cliCommands {
		if candidate == command {
			return true
		}
	}
	return false
}

func parseCLICommand(args []string) (string, []string) {
	if len(args) == 0 {
		return "", nil
	}

	if args[0] == "repo" || args[0] == "config" || args[0] == "registry" {
		if len(args) == 1 {
			return args[0], nil
		}
		return strings.Join(args[:2], " "), args[2:]
	}

	return args[0], args[1:]
}

func appendJSONFlag(args []string) []string {
	for _, arg := range args {
		if arg == "--json" {
			return args
		}
	}
	return append([]string{"--json"}, args...)
}

func setFlagUsage(fs *flag.FlagSet, text string) {
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), text)
		fs.PrintDefaults()
	}
}

func currentCLIProgramName() string {
	if activeCLIProgramName == "" {
		return "mainline"
	}
	return activeCLIProgramName
}
