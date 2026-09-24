package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rs/zerolog"
	"github.com/urfave/cli/v3"

	"github.com/maogou/ohmyssh/internal/i18n"
	"github.com/maogou/ohmyssh/internal/pkg/errno"
	"github.com/maogou/ohmyssh/internal/pkg/zlog"
	"github.com/maogou/ohmyssh/internal/service"
	"github.com/maogou/ohmyssh/internal/sshclient"
)

// globalFlags are readable from every subcommand because urfave/cli v3 resolves
// flag lookups through the command's lineage.
//
// The Usage strings are read out of the catalogue here rather than written into
// the commands, because a flag's Usage is fixed when the tree is built and
// --help prints whatever was fixed. Run sets the language before calling New for
// exactly this reason.
func globalFlags() []cli.Flag {
	m := i18n.M()
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "config",
			Aliases: []string{"F"},
			Usage:   m.FlagConfigUsage,
			Sources: cli.EnvVars("OHMYSSH_CONFIG"),
		},
		&cli.StringFlag{
			Name:    "known-hosts",
			Usage:   m.FlagKnownHostsUsage,
			Sources: cli.EnvVars("OHMYSSH_KNOWN_HOSTS"),
		},
		&cli.StringFlag{
			Name:    "log-level",
			Value:   "info",
			Usage:   m.FlagLogLevelUsage,
			Sources: cli.EnvVars("OHMYSSH_LOG_LEVEL"),
		},
		&cli.StringFlag{
			Name:    "log-format",
			Value:   "console",
			Usage:   m.FlagLogFormatUsage,
			Sources: cli.EnvVars("OHMYSSH_LOG_FORMAT"),
		},
		&cli.BoolFlag{
			Name:    "debug",
			Aliases: []string{"d"},
			Usage:   m.FlagDebugUsage,
		},
		&cli.BoolFlag{
			Name:  "insecure",
			Usage: m.FlagInsecureUsage,
		},
		&cli.BoolFlag{
			Name:  "no-agent",
			Usage: m.FlagNoAgentUsage,
		},
		&cli.StringFlag{
			Name:    "password",
			Aliases: []string{"p"},
			Usage:   m.FlagPasswordUsage,
			Sources: cli.EnvVars("OHMYSSH_PASSWORD"),
		},
		&cli.BoolFlag{
			Name:  "password-stdin",
			Usage: m.FlagPasswordStdinUsage,
		},
		&cli.BoolFlag{
			Name:  "no-prompt",
			Usage: m.FlagNoPromptUsage,
		},
		&cli.BoolFlag{
			Name:  "no-save-password",
			Usage: m.FlagNoSavePasswordUsage,
		},
		&cli.DurationFlag{
			Name:  "timeout",
			Value: sshclient.DefaultDialTimeout,
			Usage: m.FlagTimeoutUsage,
		},
		// Declared here so that it appears in --help beside the others, with the
		// variable that can also set it. It is read before the tree exists, by
		// prescanLanguage; by the time the framework parses this one the language
		// has already been chosen, and the value is not consulted again.
		&cli.StringFlag{
			Name:    "lang",
			Aliases: []string{"language"},
			Usage:   m.FlagLangUsage,
			Sources: cli.EnvVars("OHMYSSH_LANG"),
		},
	}
}

// setupLogging runs before any command and installs the global logger.
func setupLogging(_ context.Context, cmd *cli.Command) (context.Context, error) {
	level := cmd.String("log-level")
	if cmd.Bool("debug") {
		level = zerolog.DebugLevel.String()
	}
	zlog.Setup(
		zlog.Options{
			Level:  level,
			Pretty: cmd.String("log-format") != "json",
		},
	)
	if cmd.Bool("insecure") {
		zlog.L().Warn().Msg("host key verification is disabled")
	}
	return nil, nil
}

// connectOptions collects the global flags into the service layer's view of
// them, so that no service has to know a command line exists.
//
// It can fail because one of the flags reads standard input, and a password that
// was meant to arrive on a pipe may not arrive at all. That is a command line
// that cannot be acted on, which is what usageError is for.
func connectOptions(cmd *cli.Command) (service.ConnectOptions, error) {
	opts := service.ConnectOptions{
		ConfigPath:     cmd.String("config"),
		Password:       cmd.String("password"),
		NoPrompt:       cmd.Bool("no-prompt"),
		NoSavePassword: cmd.Bool("no-save-password"),
		NoAgent:        cmd.Bool("no-agent"),
		KnownHosts:     cmd.String("known-hosts"),
		Insecure:       cmd.Bool("insecure"),
		Timeout:        cmd.Duration("timeout"),
	}

	if !cmd.Bool("password-stdin") {
		return opts, nil
	}
	if opts.Password != "" {
		return service.ConnectOptions{}, usageError(i18n.M().PasswordBoth)
	}

	in := cmd.Root().Reader
	if in == nil {
		in = os.Stdin
	}
	password, err := readPassword(in)
	if err != nil {
		return service.ConnectOptions{}, err
	}
	opts.Password = password
	return opts, nil
}

// readPassword reads the one line --password-stdin names.
//
// The flag exists because an argument is public: anything that can list the
// processes on the machine can read a command line, so `--password hunter2` puts
// the password in front of every user on it and into the shell's history. What
// arrives on a pipe is not.
//
// The read is one byte at a time on purpose. Anything read past the newline
// would be taken from whatever else is on the other end — a remote command's own
// standard input, for one — which is not this function's to consume.
func readPassword(in io.Reader) (string, error) {
	var line []byte
	buf := make([]byte, 1)
	for {
		n, err := in.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				break
			}
			line = append(line, buf[0])
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return "", fmt.Errorf("read --password-stdin: %w", err)
		}
	}

	// A line ending in CRLF is still one line: the carriage return belongs to the
	// line ending rather than to the password, whether it came from a Windows
	// shell or from a file written on one.
	password := strings.TrimSuffix(string(line), "\r")
	if password == "" {
		return "", usageError(i18n.M().PasswordStdinEmpty)
	}
	return password, nil
}

// usageError reports a command line that cannot be acted on. It is a separate
// exit status from a failure, so a wrapper script can tell a mistyped argument
// from a host that would not answer.
//
// The errno value is built here and translated in one place, like every other
// failure the command layer reports: see exit in run.go.
func usageError(message string) error {
	return exit(errno.NewExit(errno.CodeUsage, message))
}
