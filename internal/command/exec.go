package command

import (
	"context"
	"os"

	"github.com/maogou/ohmyssh/internal/service"
	"github.com/urfave/cli/v3"
)

func execCommand(connect service.ConnectService) *cli.Command {
	return &cli.Command{
		Name:      "exec",
		Usage:     "Run a command on a remote host without a PTY",
		ArgsUsage: "<host> -- <command...>",
		Description: `Run one command on a host and return its exit status.

Output is streamed straight to stdout and stderr, so exec composes in pipelines:

  ohmyssh exec web1 -- df -h
  ohmyssh exec web1 -- cat /etc/hostname | tr -d '\\n'

The remote exit status becomes ohmyssh's exit status.

A password can be piped in rather than typed, which keeps it out of a process
list it would otherwise be visible in:

  echo "$PW" | ohmyssh exec --password-stdin web1 -- uptime`,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args().Slice()
			if len(args) < 2 {
				return usageError(
					"exec requires a host and a command: ohmyssh exec <host> -- <command>",
				)
			}
			opts, err := connectOptions(cmd)
			if err != nil {
				return err
			}
			return exit(connect.Exec(ctx, opts, args[0], args[1:], os.Stdout, os.Stderr))
		},
	}
}
