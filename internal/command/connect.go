package command

import (
	"context"

	"github.com/maogou/ohmyssh/internal/i18n"
	"github.com/maogou/ohmyssh/internal/service"
	"github.com/urfave/cli/v3"
)

func connectCommand(connect service.ConnectService) *cli.Command {
	m := i18n.M()
	return &cli.Command{
		Name:        "connect",
		Aliases:     []string{"ssh"},
		Usage:       m.ConnectUsage,
		ArgsUsage:   "<host> [command...]",
		Description: m.ConnectDescription,
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
