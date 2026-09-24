// Package errno carries the exit statuses ohmyssh reports to its caller.
//
// The service layer signals failures with these rather than with the CLI
// framework's own exit errors. That keeps the service free of any dependency on
// urfave/cli, so its behaviour can be tested by calling it directly instead of
// driving the command line.
package errno

import (
	"errors"
	"fmt"
)

// Process exit statuses.
//
// A remote command's status is deliberately not one of these: it is passed
// through unchanged, so `ohmyssh exec host -- cmd` reports what cmd reported.
const (
	CodeOK    = 0
	CodeError = 1 // general failure: unknown host, nothing saved, session failed
	CodeUsage = 2 // the command line was wrong
)

// ExitError pairs a message with the exit status that should report it.
//
// An empty message with a non-zero code means "exit silently with this status",
// which is how a remote command's status is forwarded: the remote command has
// already said whatever there was to say.
type ExitError struct {
	Code    int
	Message string
}

// NewExit builds an ExitError.
func NewExit(code int, message string) *ExitError {
	return &ExitError{Code: code, Message: message}
}

func (e *ExitError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("exit status %d", e.Code)
}

// ExitCode reports the status ohmyssh should exit with for err.
func ExitCode(err error) int {
	if err == nil {
		return CodeOK
	}
	if exitErr, ok := errors.AsType[*ExitError](err); ok {
		return exitErr.Code
	}
	return CodeError
}

// Message reports what should be printed for err, or "" when err should be
// reported silently.
func Message(err error) string {
	if err == nil {
		return ""
	}
	if exitErr, ok := errors.AsType[*ExitError](err); ok {
		return exitErr.Message
	}
	return err.Error()
}
