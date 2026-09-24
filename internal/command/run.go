package command

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/maogou/ohmyssh/internal/pkg/errno"
	"github.com/urfave/cli/v3"
)

// Run executes the CLI and returns the status the process should exit with.
//
// The error is turned into a status here rather than through log.Fatal so that
// a remote command's own exit status survives: the whole point of `ohmyssh exec
// host -- cmd` is that its callers can test $?.
func Run(args []string) int {
	// A cancelled context aborts a connection attempt; interactive sessions are
	// unaffected because the terminal is in raw mode and never raises SIGINT.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := New().Run(ctx, args)
	if err == nil {
		return errno.CodeOK
	}

	// An ExitCoder is normally handled by the framework, which reports it and
	// exits by itself; this path covers anything it handed back instead.
	if exitErr, ok := errors.AsType[cli.ExitCoder](err); ok {
		if msg := strings.TrimSpace(exitErr.Error()); msg != "" {
			fmt.Fprintln(os.Stderr, "ohmyssh:", msg)
		}
		return exitErr.ExitCode()
	}

	fmt.Fprintln(os.Stderr, "ohmyssh:", err)
	return errno.CodeError
}

// exit maps an error from the service layer onto the CLI framework's own exit
// error. The service layer stays free of urfave/cli by returning errno values;
// this is where the two vocabularies meet.
func exit(err error) error {
	if err == nil {
		return nil
	}
	if exitErr, ok := errors.AsType[*errno.ExitError](err); ok {
		return cli.Exit(exitErr.Message, exitErr.Code)
	}
	return err
}
