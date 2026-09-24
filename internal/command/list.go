package command

import (
	"context"

	"github.com/maogou/ohmyssh/internal/i18n"
	"github.com/maogou/ohmyssh/internal/service"
	"github.com/urfave/cli/v3"
)

func listCommand(connect service.ConnectService) *cli.Command {
	m := i18n.M()
	return &cli.Command{
		Name:    "list",
		Aliases: []string{"ls", "browse"},
		Usage:   m.ListUsage,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "filter",
				Aliases: []string{"f"},
				Usage:   m.ListFilterUsage,
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
