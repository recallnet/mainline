package app

import "errors"

type cliExitError struct {
	code int
	err  error
}

func (e *cliExitError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *cliExitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func exitWithCode(code int, err error) error {
	if err == nil || code == 0 {
		return err
	}
	return &cliExitError{code: code, err: err}
}

// CLIExitCode returns the process exit code for a CLI error.
func CLIExitCode(err error) int {
	if err == nil {
		return 0
	}
	var coded *cliExitError
	if errors.As(err, &coded) && coded.code > 0 {
		return coded.code
	}
	return 1
}
