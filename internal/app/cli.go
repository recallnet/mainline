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

	command := strings.Join(remaining, " ")
	if !isKnownCLICommand(command) {
		return fmt.Errorf("unknown command %q\n\n%s", command, cliHelpText())
	}

	wiring := bootstrap()
	fmt.Fprintf(stdout, "%s is not implemented yet.\n", command)
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
