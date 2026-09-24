package command

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/maogou/ohmyssh/internal/i18n"
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

	err := run(ctx, args)
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

// run installs the language and hands the arguments to the command tree.
//
// This is the only place the language is set, and it is set before the tree is
// built: a command's Usage string is fixed when the tree is constructed, so
// asking for Chinese after New has run would give a Chinese --lang description
// under an English --help.
//
// Tests call New directly and so never get here, which is what keeps them
// reading the English they were written against.
func run(ctx context.Context, args []string) error {
	lang, err := prescanLanguage(args, os.Getenv)

	// Installed even when the flag was refused, and installed from what came
	// back beside the error rather than from what was asked for: the complaint
	// below has to be written in a language the person who typed it can read,
	// and the one they named is by definition not one this program has.
	i18n.Setup(lang)

	if err != nil {
		var unsupported *i18n.UnsupportedError
		if !errors.As(err, &unsupported) {
			return err
		}
		return cli.Exit(
			fmt.Sprintf(i18n.M().UnsupportedLanguage, unsupported.Value, i18n.Names()),
			errno.CodeUsage,
		)
	}

	return New().Run(ctx, args)
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
