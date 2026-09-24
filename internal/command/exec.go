package command

import (
	"context"
	"os"

	"github.com/maogou/ohmyssh/internal/i18n"
	"github.com/maogou/ohmyssh/internal/service"
	"github.com/urfave/cli/v3"
)

func execCommand(connect service.ConnectService) *cli.Command {
	m := i18n.M()
	return &cli.Command{
		Name:        "exec",
		Usage:       m.ExecUsage,
		ArgsUsage:   "<host> -- <command...>",
		Description: m.ExecDescription,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args().Slice()
			if len(args) < 2 {
				return usageError(m.ExecMissing)
			}
			opts, err := connectOptions(cmd)
			if err != nil {
				return err
			}
			return exit(connect.Exec(ctx, opts, args[0], args[1:], os.Stdout, os.Stderr))
		},
	}
}
