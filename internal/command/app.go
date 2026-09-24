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
	"github.com/maogou/ohmyssh/internal/i18n"
	"github.com/maogou/ohmyssh/internal/pkg/zlog"
	"github.com/maogou/ohmyssh/internal/repository"
	"github.com/maogou/ohmyssh/internal/service"
)

// New builds the command tree with the service layer wired to its
// dependencies. It is exported so tests can exercise the CLI without spawning a
// process.
//
// Every word in the tree comes out of the catalogue here, at the moment the tree
// is built. Run has already installed the language by then; a test that calls
// this directly gets the English the source is written in.
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
	m := i18n.M()

	return &cli.Command{
		Name:        "ohmyssh",
		Usage:       m.RootUsage,
		Version:     constant.Version(),
		ArgsUsage:   "[host]",
		Description: m.RootDescription,
		Flags:       globalFlags(),
		Before:      setupLogging,
		Action:      rootAction(connect),
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
