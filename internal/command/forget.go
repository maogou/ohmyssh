package command

import (
	"context"
	"fmt"

	"github.com/maogou/ohmyssh/internal/i18n"
	"github.com/maogou/ohmyssh/internal/pkg/errno"
	"github.com/maogou/ohmyssh/internal/service"
	"github.com/urfave/cli/v3"
)

func forgetCommand(credentials service.CredentialService) *cli.Command {
	m := i18n.M()
	return &cli.Command{
		Name:        "forget",
		Usage:       m.ForgetUsage,
		ArgsUsage:   "[host]",
		Description: m.ForgetDescription,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "all",
				Usage: m.ForgetAllUsage,
			},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			if cmd.Bool("all") {
				removed, err := credentials.ForgetAll()
				if err != nil {
					return err
				}
				fmt.Printf(i18n.M().ForgotAll+"\n", removed)
				return nil
			}

			if !cmd.Args().Present() {
				return listSavedPasswords(credentials)
			}

			key, removed, err := credentials.Forget(cmd.String("config"), cmd.Args().First())
			if err != nil {
				return err
			}
			if !removed {
				return exit(errno.NewExit(errno.CodeError, fmt.Sprintf(i18n.M().NoSavedPassword, key)))
			}
			fmt.Printf(i18n.M().ForgotOne+"\n", key)
			return nil
		},
	}
}

// listSavedPasswords prints what forget could remove, so the command is
// discoverable without reading the store by hand.
func listSavedPasswords(credentials service.CredentialService) error {
	keys := credentials.Saved()
	if len(keys) == 0 {
		fmt.Println("no saved passwords")
		return nil
	}

	fmt.Printf("saved passwords in %s:\n", credentials.Path())
	for _, key := range keys {
		fmt.Printf("  %s\n", key)
	}
	fmt.Println("\nforget one with: ohmyssh forget <host>")
	return nil
}
