package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

var cliCommands = []string{
	"submit",
	"status",
	"confidence",
	"run-once",
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
	"repo init",
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
	fs := flag.NewFlagSet(programName, flag.ContinueOnError)
	fs.SetOutput(stderr)

	var showHelp bool
	var showVersion bool
	fs.BoolVar(&showHelp, "help", false, "show help")
	fs.BoolVar(&showHelp, "h", false, "show help")
	fs.BoolVar(&showVersion, "version", false, "show version")

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
		printVersion(stdout, programName)
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
		printVersion(stdout, programName)
		return nil
	}

	return handleCommand(command, commandArgs, stdout, stderr)
}

func printCLIHelp(w io.Writer, programName string) {
	fmt.Fprint(w, cliHelpText(programName))
}

func cliHelpText(programName string) string {
	return fmt.Sprintf(`%s coordinates local protected-branch integrations and publishes.

Usage:
  %s [command]

Commands:
  submit
  status
  confidence
  run-once
  retry
  cancel
  publish
  logs
  watch
  events
  doctor
  completion
  version
  config edit
  repo init
  repo show

Implemented today: repo init/show, doctor, submit, status, confidence, run-once, retry, cancel, publish, logs, watch, events, completion, version, config edit.
`, programName, programName)
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

	if args[0] == "repo" || args[0] == "config" {
		if len(args) == 1 {
			return args[0], nil
		}
		return strings.Join(args[:2], " "), args[2:]
	}

	return args[0], args[1:]
}
