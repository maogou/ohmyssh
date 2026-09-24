package errno

import (
	"errors"
	"fmt"
	"testing"
)

// The service layer speaks only in these values, and the command layer turns
// them into a process exit status, so what they report has to survive being
// carried across that boundary.

func TestExitCodeReportsTheStatusToExitWith(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nothing went wrong", nil, CodeOK},
		{"a value built for the purpose", NewExit(CodeUsage, "bad command line"), CodeUsage},
		// Anything else is some other kind of failure, and 1 is what a caller
		// expects a failing program to report.
		{"an ordinary error", errors.New("boom"), CodeError},
		{"a wrapped value", fmt.Errorf("connect: %w", NewExit(7, "")), 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExitCode(tt.err); got != tt.want {
				t.Errorf("ExitCode(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

func TestMessageReportsWhatToPrint(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nothing went wrong", nil, ""},
		{"a value with something to say", NewExit(CodeError, "unknown host"), "unknown host"},
		// An empty message means "say nothing": that is how a remote command's
		// status is forwarded, and it has already said whatever there was to say.
		{"a value that says nothing", NewExit(7, ""), ""},
		// An ordinary error is not in the errno vocabulary and has to speak for
		// itself rather than be swallowed.
		{"an ordinary error", errors.New("boom"), "boom"},
		{"a wrapped value", fmt.Errorf("connect: %w", NewExit(CodeError, "unknown host")), "unknown host"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Message(tt.err); got != tt.want {
				t.Errorf("Message(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

// A value that says nothing still has to be printable: it reaches the log and,
// when it is not an exit status at all, an error message.
func TestErrorNeverRendersEmpty(t *testing.T) {
	if got := NewExit(7, "").Error(); got != "exit status 7" {
		t.Errorf("Error() = %q, want %q", got, "exit status 7")
	}
	if got := NewExit(7, "unknown host").Error(); got != "unknown host" {
		t.Errorf("Error() = %q, want %q", got, "unknown host")
	}
}
