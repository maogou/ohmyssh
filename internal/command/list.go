package command

import (
	"context"

	"github.com/maogou/ohmyssh/internal/service"
	"github.com/urfave/cli/v3"
)

func listCommand(connect service.ConnectService) *cli.Command {
	return &cli.Command{
		Name:    "list",
		Aliases: []string{"ls", "browse"},
		Usage:   "Browse hosts interactively and connect with enter",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "filter",
				Aliases: []string{"f"},
				Usage:   "seed the host filter",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			opts, err := connectOptions(cmd)
			if err != nil {
				return err
			}
			return exit(connect.Browse(ctx, opts, cmd.String("filter")))
		},
	}
}
