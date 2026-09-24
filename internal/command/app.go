// Package command wires the ohmyssh commands onto an urfave/cli v3 command
// tree.
//
// Nothing here decides anything: a command turns its flags into arguments for
// the service layer and turns what comes back into an exit status. The
// behaviour it drives lives in internal/service.
package command

import (
	"context"

	"github.com/urfave/cli/v3"

	"github.com/maogou/ohmyssh/internal/config"
	"github.com/maogou/ohmyssh/internal/constant"
	"github.com/maogou/ohmyssh/internal/pkg/zlog"
	"github.com/maogou/ohmyssh/internal/repository"
	"github.com/maogou/ohmyssh/internal/service"
)

// New builds the command tree with the service layer wired to its
// dependencies. It is exported so tests can exercise the CLI without spawning a
// process.
func New() *cli.Command {
	// setupLogging installs the real logger in Before, after this function has
	// run. logging.L() hands back a pointer to the process-wide logger rather
	// than a copy, so what is passed down here observes that swap.
	logger := zlog.L()

	hosts := repository.NewHosts(hostsPath())
	creds := repository.NewCredential()
	transport := repository.NewTransport()

	connect := service.NewConnectService(logger, transport, hosts, creds)
	credentials := service.NewCredentialService(hosts, creds)

	return &cli.Command{
		Name:      "ohmyssh",
		Usage:     "SSH connection manager with an interactive host browser",
		Version:   constant.Version(),
		ArgsUsage: "[host]",
		Description: `ohmyssh reads hosts from your OpenSSH config and connects to them over SSH.

Run it without arguments to browse hosts interactively, or name a host to
connect straight away:

  ohmyssh                  browse hosts and connect with enter
  ohmyssh web1             open a shell on web1
  ohmyssh web1 -- uptime   run one command under a PTY
  ohmyssh exec web1 -- df -h   run one command without a PTY
  ohmyssh put web1 ./out/ /opt/app/   copy files to web1

A password can be piped in rather than passed as an argument, which keeps it out
of the process list and the shell's history:

  echo "$PW" | ohmyssh --password-stdin web1 -- uptime

Host key verification uses trust-on-first-use against ~/.ssh/known_hosts:
unknown hosts are recorded, changed keys are rejected.`,
		Flags:  globalFlags(),
		Before: setupLogging,
		Action: rootAction(connect),
		Commands: []*cli.Command{
			listCommand(connect),
			connectCommand(connect),
			execCommand(connect),
			putCommand(connect),
			getCommand(connect),
			scpCommand(connect),
			forgetCommand(credentials),
		},
	}
}

// rootAction browses hosts, or connects directly when a host is named.
func rootAction(connect service.ConnectService) cli.ActionFunc {
	return func(ctx context.Context, cmd *cli.Command) error {
		opts, err := connectOptions(cmd)
		if err != nil {
			return err
		}
		if cmd.Args().Len() == 0 {
			return exit(connect.Browse(ctx, opts, cmd.String("filter")))
		}
		return exit(connect.Connect(ctx, opts, cmd.Args().First(), cmd.Args().Tail()))
	}
}

// hostsPath is where hosts added through ohmyssh are written. A machine whose
// home directory cannot be found cannot keep them, which is worth saying once at
// startup rather than at the first attempt to add one; the connection itself is
// unaffected, so it does not stop the command.
func hostsPath() string {
	path, err := config.DefaultHostsPath()
	if err != nil {
		zlog.L().Warn().Err(err).Msg("hosts added through ohmyssh cannot be saved")
		return ""
	}
	return path
}
