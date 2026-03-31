package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

// RunDaemon executes the mainlined command-line interface.
func RunDaemon(args []string) error {
	return runDaemon(args, os.Stdout, os.Stderr)
}

func runDaemon(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet("mainlined", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var showHelp bool
	fs.BoolVar(&showHelp, "help", false, "show help")
	fs.BoolVar(&showHelp, "h", false, "show help")

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

	wiring := bootstrap()
	fmt.Fprintln(stdout, "mainlined worker loop is not implemented yet.")
	fmt.Fprintf(stdout, "Integration worker: %s\n", wiring.Workers.Integration.Name)
	fmt.Fprintf(stdout, "Publish worker: %s\n", wiring.Workers.Publish.Name)
	return nil
}

func printDaemonHelp(w io.Writer) {
	fmt.Fprint(w, daemonHelpText())
}

func daemonHelpText() string {
	return `mainlined runs the background worker loop for mainline.

Usage:
  mainlined [flags]

Flags:
  -h, --help   show help
`
}
