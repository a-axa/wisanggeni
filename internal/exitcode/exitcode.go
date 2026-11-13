// Package exitcode carries the documented process exit codes together with an
// error type that reports one.
//
// The numbers are the CLI's public contract (§5). They live here, rather than in
// cmd, so the engine packages that the CLI and the TUI share can tag a failure
// with the right code without importing the command layer. cmd aliases these
// constants, and Execute reads the code back through the ExitCode method that
// error values here implement.
package exitcode

import (
	"errors"
	"fmt"
)

// The exit codes of §5.
const (
	// OK is a run that completed.
	OK = 0

	// Error is a usage or input failure.
	Error = 1

	// Capacity is a payload larger than the carrier can hold.
	Capacity = 2

	// Decrypt is a passphrase that does not open the blob, or a blob that is not
	// there. The two are deliberately indistinguishable (§11.1).
	Decrypt = 3

	// Verify is a post-write check that failed.
	Verify = 4

	// Unfit is a carrier or a fill the policy refuses.
	Unfit = 5
)

// Coded is a failure that maps onto a process exit code. The type name avoids
// the Error constant, which callers use for the generic exit code.
type Coded struct {
	// Code is one of the constants above.
	Code int

	// Err is the underlying failure.
	Err error
}

func (e *Coded) Error() string { return e.Err.Error() }

// Unwrap exposes the cause to errors.Is and errors.As.
func (e *Coded) Unwrap() error { return e.Err }

// ExitCode implements the interface cmd.Execute looks for.
func (e *Coded) ExitCode() int { return e.Code }

// Fail builds a coded error from a format string.
func Fail(code int, format string, args ...any) error {
	return &Coded{Code: code, Err: fmt.Errorf(format, args...)}
}

// Code reports the exit code carried by err, or Error when it carries none.
// Callers pass a non-nil error.
func Code(err error) int {
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) {
		return coded.ExitCode()
	}
	return Error
}
