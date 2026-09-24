package command

import (
	"context"

	"github.com/maogou/ohmyssh/internal/service"
	"github.com/urfave/cli/v3"
)

func connectCommand(connect service.ConnectService) *cli.Command {
	return &cli.Command{
		Name:      "connect",
		Aliases:   []string{"ssh"},
		Usage:     "Open an interactive shell (or run a command under a PTY)",
		ArgsUsage: "<host> [command...]",
		Description: `Connect to a host from your ssh config and open a login shell.

A trailing command runs under a PTY, like ssh -t, so interactive programs work:

  ohmyssh connect web1
  ohmyssh connect web1 -- htop

Use -- before the command so its own flags are not parsed by ohmyssh.

A password can be piped in rather than typed, which keeps it out of a process
list it would otherwise be visible in:

  echo "$PW" | ohmyssh connect --password-stdin web1

Running connect with no host opens the interactive host browser.`,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			opts, err := connectOptions(cmd)
			if err != nil {
				return err
			}
			if !cmd.Args().Present() {
				return exit(connect.Browse(ctx, opts, cmd.String("filter")))
			}
			return exit(connect.Connect(ctx, opts, cmd.Args().First(), cmd.Args().Tail()))
		},
	}
}
