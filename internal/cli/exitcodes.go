package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/gscode1/gs-acm-utils/internal/mockdata"
)

// Process exit codes (plan section 6.2).
const (
	// ExitOK: all inputs resolved.
	ExitOK = 0
	// ExitFailed: one or more inputs failed to resolve or validate.
	ExitFailed = 1
	// ExitMockData: the mock-data directory failed to load or validate.
	ExitMockData = 2
	// ExitUsage: bad flags or arguments, unreadable input.
	ExitUsage = 3
)

// usageError marks an error as a usage problem (exit 3): bad flags, missing
// required flags, unreadable or missing input.
type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

func usagef(format string, args ...any) error { return &usageError{fmt.Errorf(format, args...)} }

func asUsage(err error) error {
	if err == nil {
		return nil
	}

	return &usageError{err}
}

// failureError marks resolution failures (exit 1). When Reported is set the
// failures were already written to stderr by the command and are not printed again.
type failureError struct {
	err      error
	Reported bool
}

func (e *failureError) Error() string { return e.err.Error() }
func (e *failureError) Unwrap() error { return e.err }

// ExitCode is the single place that maps an error returned by a command to a
// process exit code. Errors that are not classified (cobra flag/argument
// errors, unknown commands) are usage errors.
func ExitCode(err error) int {
	var (
		fail *failureError
		load *mockdata.LoadErrors
	)

	switch {
	case err == nil:
		return ExitOK
	case errors.As(err, &fail):
		return ExitFailed
	case errors.As(err, &load):
		return ExitMockData
	default:
		return ExitUsage
	}
}

// reportError writes err to w once, in a human-readable form. A mock-data load
// failure prints every problem on its own line followed by a count line.
func reportError(w io.Writer, err error) {
	var (
		fail *failureError
		load *mockdata.LoadErrors
	)

	switch {
	case err == nil:
	case errors.As(err, &fail) && fail.Reported:
	case errors.As(err, &load):
		for _, e := range load.Errors {
			fmt.Fprintln(w, e)
		}

		noun := "errors"
		if len(load.Errors) == 1 {
			noun = "error"
		}

		fmt.Fprintf(w, "mock data failed to load: %d %s\n", len(load.Errors), noun)
	default:
		fmt.Fprintln(w, "error:", err)
	}
}
