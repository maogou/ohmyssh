package command

import (
	"errors"
	"strings"
	"testing"

	"github.com/maogou/ohmyssh/internal/pkg/errno"
	"github.com/urfave/cli/v3"
)

// exit is where two vocabularies meet: the service layer returns errno values
// so it never has to import the CLI framework, and the framework only knows its
// own exit errors. Everything about a remote command's exit status passes
// through here, so it is worth pinning down.

func TestExitCarriesTheStatusOntoTheFramework(t *testing.T) {
	coder, ok := errors.AsType[cli.ExitCoder](exit(errno.NewExit(7, "")))
	if !ok {
		t.Fatal("exit did not return an ExitCoder")
	}
	if got := coder.ExitCode(); got != 7 {
		t.Errorf("ExitCode = %d, want 7", got)
	}
	// A remote status comes with no message of its own; the framework must not
	// be handed an empty one to print.
	if msg := strings.TrimSpace(coder.Error()); msg != "" {
		t.Errorf("message = %q, want none", msg)
	}
}

func TestExitKeepsTheMessageAndTheStatus(t *testing.T) {
	const message = "exec requires a host and a command"

	coder, ok := errors.AsType[cli.ExitCoder](exit(errno.NewExit(errno.CodeUsage, message)))
	if !ok {
		t.Fatal("exit did not return an ExitCoder")
	}
	if got := coder.ExitCode(); got != errno.CodeUsage {
		t.Errorf("ExitCode = %d, want %d", got, errno.CodeUsage)
	}
	if got := coder.Error(); got != message {
		t.Errorf("message = %q, want %q", got, message)
	}
}

// Anything that is not an errno value is left alone, so the framework reports
// it the ordinary way.
func TestExitLeavesOrdinaryErrorsAlone(t *testing.T) {
	boom := errors.New("boom")
	if got := exit(boom); !errors.Is(got, boom) {
		t.Errorf("exit(boom) = %v, want it unchanged", got)
	}
	if got := exit(nil); got != nil {
		t.Errorf("exit(nil) = %v, want nil", got)
	}
}
