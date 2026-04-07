package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

var activeCLIProgramName = "mainline"

// RunCLI executes the mainline command-line interface.
func RunCLI(args []string) error {
	return RunCLIWithName("mainline", args)
}

// RunCLIWithName executes the CLI using the provided program name.
func RunCLIWithName(programName string, args []string) error {
	return runCLIWithName(programName, args, newStepPrinter(os.Stdout), os.Stderr)
}

func runCLI(args []string, stdout *stepPrinter, stderr io.Writer) error {
	return runCLIWithName("mainline", args, stdout, stderr)
}

func runCLIWithName(programName string, args []string, stdout *stepPrinter, stderr io.Writer) error {
	previousProgramName := activeCLIProgramName
	activeCLIProgramName = programName
	defer func() {
		activeCLIProgramName = previousProgramName
	}()

	root := newCLICommandTree(programName, stdout, stderr)
	root.SetArgs(args)
	root.SetOut(stdout.Raw())
	root.SetErr(stderr)
	return root.Execute()
}

func newCLICommandTree(programName string, stdout *stepPrinter, stderr io.Writer) *cobra.Command {
	var asJSON bool
	var showVersion bool

	root := &cobra.Command{
		Use:              programName,
		SilenceUsage:     true,
		SilenceErrors:    true,
		TraverseChildren: true,
		Args:             cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if showVersion {
				printVersion(stdout.Raw(), programName, asJSON)
				return nil
			}
			printCLIHelp(stdout.Raw(), programName)
			return nil
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetHelpFunc(func(*cobra.Command, []string) {
		printCLIHelp(stdout.Raw(), programName)
	})
	root.PersistentFlags().BoolVar(&asJSON, "json", false, "output json where supported")
	root.Flags().BoolVar(&showVersion, "version", false, "show version")

	root.AddCommand(
		newLegacyCommand("land", "submit and wait for integrate plus publish", true, stdout, stderr, runLand),
		newLegacyCommand("submit", "queue a topic worktree or detached sha", true, stdout, stderr, runSubmit),
		newLegacyCommand("status", "show queue and protected-branch state", true, stdout, stderr, runStatus),
		newLegacyCommand("next", "wait for the next protected-main commit or push", true, stdout, stderr, runNext),
		newLegacyCommand("confidence", "summarize evidence and promotion gates", true, stdout, stderr, runConfidence),
		newLegacyCommand("run-once", "run one integration or publish cycle", true, stdout, stderr, runRunOnce),
		newLegacyCommand("wait", "wait on a submission id for integration or landed outcome", true, stdout, stderr, runWait),
		newLegacyCommand("rebase", "rebase a topic branch onto protected main", true, stdout, stderr, runRebase),
		newLegacyCommand("blocked", "list blocked submissions and recovery actions", true, stdout, stderr, runBlocked),
		newLegacyCommand("retry", "requeue a blocked, failed, or cancelled item", false, stdout, stderr, runRetry),
		newLegacyCommand("cancel", "cancel a queued, blocked, or failed item", false, stdout, stderr, runCancel),
		newLegacyCommand("publish", "queue publish of the protected tip", true, stdout, stderr, runPublish),
		newLegacyCommand("logs", "replay durable event history", false, stdout, stderr, runLogs),
		newLegacyCommand("watch", "refresh status continuously", true, stdout, stderr, runWatch),
		newLegacyCommand("events", "stream durable events or lifecycle envelopes", true, stdout, stderr, runEvents),
		newLegacyCommand("doctor", "inspect and optionally repair stuck states", true, stdout, stderr, runDoctor),
		newLegacyCommand("completion", "emit shell completion", true, stdout, stderr, runCompletion),
		newVersionCommand(programName, stdout),
		newConfigCommand(stdout, stderr),
		newRegistryCommand(stdout, stderr),
		newRepoCommand(stdout, stderr),
	)

	return root
}

func newLegacyCommand(use string, short string, supportsJSON bool, stdout *stepPrinter, stderr io.Writer, runner func([]string, *stepPrinter, io.Writer) error) *cobra.Command {
	return &cobra.Command{
		Use:                use,
		Short:              short,
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if shouldForceJSON(cmd) {
				if !supportsJSON {
					return fmt.Errorf("unknown flag: --json")
				}
				args = appendJSONFlag(args)
			}
			err := runner(args, stdout, stderr)
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		},
	}
}

func newVersionCommand(programName string, stdout *stepPrinter) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "show build metadata",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			printVersion(stdout.Raw(), programName, shouldForceJSON(cmd))
			return nil
		},
	}
}

func newConfigCommand(stdout *stepPrinter, stderr io.Writer) *cobra.Command {
	config := &cobra.Command{
		Use:   "config",
		Short: "configuration commands",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	config.AddCommand(
		newLegacyCommand("edit", "open mainline.toml in an editor", false, stdout, stderr, runConfigEdit),
	)
	return config
}

func newRegistryCommand(stdout *stepPrinter, stderr io.Writer) *cobra.Command {
	registry := &cobra.Command{
		Use:   "registry",
		Short: "global registry commands",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	registry.AddCommand(
		newLegacyCommand("prune", "remove stale repo entries from the global registry", true, stdout, stderr, runRegistryPrune),
	)
	return registry
}

func newRepoCommand(stdout *stepPrinter, stderr io.Writer) *cobra.Command {
	repo := &cobra.Command{
		Use:   "repo",
		Short: "repository commands",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	repo.AddCommand(
		newLegacyCommand("audit", "list local branches not yet merged into protected main", true, stdout, stderr, runRepoAudit),
		newLegacyCommand("init", "initialize repo config and durable state", true, stdout, stderr, runRepoInit),
		newLegacyCommand("root", "inspect or adopt the canonical root checkout", true, stdout, stderr, runRepoRoot),
		newLegacyCommand("show", "inspect repo config and worktrees", true, stdout, stderr, runRepoShow),
	)
	return repo
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
    %s next --json
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
  next          wait for the next protected-main commit or push
  confidence    summarize evidence and promotion gates
  run-once      run one integration or publish cycle
  wait          wait on a submission id for integration or landed outcome
  rebase        rebase a topic branch onto protected main
  blocked       list blocked submissions and recovery actions
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
`, programName, programName, programName, programName, programName, programName, programName, programName, programName, programName, programName, programName, programName, programName)
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

func shouldForceJSON(cmd *cobra.Command) bool {
	flagValue := cmd.InheritedFlags().Lookup("json")
	return flagValue != nil && flagValue.Changed && flagValue.Value.String() == "true"
}
