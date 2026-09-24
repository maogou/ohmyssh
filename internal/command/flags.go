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

	"github.com/maogou/ohmyssh/internal/pkg/errno"
	"github.com/maogou/ohmyssh/internal/pkg/zlog"
	"github.com/maogou/ohmyssh/internal/service"
	"github.com/maogou/ohmyssh/internal/sshclient"
)

// globalFlags are readable from every subcommand because urfave/cli v3 resolves
// flag lookups through the command's lineage.
func globalFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "config",
			Aliases: []string{"F"},
			Usage:   "path to the ssh config file (default ~/.ssh/config)",
			Sources: cli.EnvVars("OHMYSSH_CONFIG"),
		},
		&cli.StringFlag{
			Name:    "known-hosts",
			Usage:   "path to known_hosts (default ~/.ssh/known_hosts)",
			Sources: cli.EnvVars("OHMYSSH_KNOWN_HOSTS"),
		},
		&cli.StringFlag{
			Name:    "log-level",
			Value:   "info",
			Usage:   "log level: trace, debug, info, warn, error, disabled",
			Sources: cli.EnvVars("OHMYSSH_LOG_LEVEL"),
		},
		&cli.StringFlag{
			Name:    "log-format",
			Value:   "console",
			Usage:   "log format: console or json",
			Sources: cli.EnvVars("OHMYSSH_LOG_FORMAT"),
		},
		&cli.BoolFlag{
			Name:    "debug",
			Aliases: []string{"d"},
			Usage:   "shortcut for --log-level debug",
		},
		&cli.BoolFlag{
			Name:  "insecure",
			Usage: "skip host key verification (unsafe: allows interception)",
		},
		&cli.BoolFlag{
			Name:  "no-agent",
			Usage: "do not authenticate with ssh-agent",
		},
		&cli.StringFlag{
			Name:    "password",
			Aliases: []string{"p"},
			Usage:   "password for password/keyboard-interactive auth (prompted when omitted)",
			Sources: cli.EnvVars("OHMYSSH_PASSWORD"),
		},
		&cli.BoolFlag{
			Name:  "password-stdin",
			Usage: "read the password from standard input, one line",
		},
		&cli.BoolFlag{
			Name:  "no-prompt",
			Usage: "never prompt for a password; fail instead",
		},
		&cli.BoolFlag{
			Name:  "no-save-password",
			Usage: "do not remember passwords that work (saved ones are still used)",
		},
		&cli.DurationFlag{
			Name:  "timeout",
			Value: sshclient.DefaultDialTimeout,
			Usage: "connection and handshake timeout",
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
		return service.ConnectOptions{}, usageError("use --password or --password-stdin, not both")
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
		return "", usageError("--password-stdin read no password from standard input")
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
