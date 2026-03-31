package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/recallnet/mainline/internal/git"
	"github.com/recallnet/mainline/internal/policy"
	"github.com/recallnet/mainline/internal/queue"
	"github.com/recallnet/mainline/internal/state"
	"github.com/recallnet/mainline/internal/worker"
)

var cliCommands = []string{
	"submit",
	"status",
	"run-once",
	"publish",
	"doctor",
	"repo init",
	"repo show",
}

// RunCLI executes the mainline command-line interface.
func RunCLI(args []string) error {
	return runCLI(args, os.Stdout, os.Stderr)
}

func runCLI(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet("mainline", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var showHelp bool
	fs.BoolVar(&showHelp, "help", false, "show help")
	fs.BoolVar(&showHelp, "h", false, "show help")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCLIHelp(stdout)
			return nil
		}
		return err
	}

	if showHelp {
		printCLIHelp(stdout)
		return nil
	}

	remaining := fs.Args()
	if len(remaining) == 0 {
		printCLIHelp(stdout)
		return nil
	}

	command, commandArgs := parseCLICommand(remaining)
	if !isKnownCLICommand(command) {
		return fmt.Errorf("unknown command %q\n\n%s", command, cliHelpText())
	}

	wiring := bootstrap()
	fmt.Fprintf(stdout, "%s is not implemented yet.\n", command)
	if len(commandArgs) > 0 {
		fmt.Fprintf(stdout, "Received %d trailing argument(s) for future subcommand handling.\n", len(commandArgs))
	}
	fmt.Fprintf(stdout, "Protected branch default: %s\n", wiring.Policy.Repo.ProtectedBranch)
	fmt.Fprintf(stdout, "Repository root: %s\n", wiring.Git.RepositoryRoot)
	return nil
}

func printCLIHelp(w io.Writer) {
	fmt.Fprint(w, cliHelpText())
}

func cliHelpText() string {
	return `mainline coordinates local protected-branch integrations and publishes.

Usage:
  mainline [command]

Commands:
  submit
  status
  run-once
  publish
  doctor
  repo init
  repo show

Use "mainline [command]" for upcoming milestone implementations.
`
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

	if args[0] == "repo" {
		if len(args) == 1 {
			return "repo", nil
		}
		return strings.Join(args[:2], " "), args[2:]
	}

	return args[0], args[1:]
}

type wiring struct {
	Git     git.Engine
	Queue   queue.Manager
	State   state.Store
	Policy  policy.Config
	Workers worker.Registry
}

func bootstrap() wiring {
	return wiring{
		Git:     git.NewEngine("."),
		Queue:   queue.NewManager(),
		State:   state.NewStore(""),
		Policy:  policy.DefaultConfig(),
		Workers: worker.NewRegistry(),
	}
}
