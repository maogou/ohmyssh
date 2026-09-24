package command

import (
	"context"
	"fmt"

	"github.com/maogou/ohmyssh/internal/pkg/errno"
	"github.com/maogou/ohmyssh/internal/service"
	"github.com/urfave/cli/v3"
)

func forgetCommand(credentials service.CredentialService) *cli.Command {
	return &cli.Command{
		Name:      "forget",
		Usage:     "Delete a saved password",
		ArgsUsage: "[host]",
		Description: `Delete the password ohmyssh saved for a host.

Connecting saves a password once it works, so that later connections do not ask
for one. forget is the undo:

  ohmyssh forget web1        forget the password for web1
  ohmyssh forget --all       forget every saved password
  ohmyssh forget             list the hosts with a saved password

Saved passwords live in an encrypted file; run --log-level debug to see where.`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "all",
				Usage: "forget every saved password",
			},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			if cmd.Bool("all") {
				removed, err := credentials.ForgetAll()
				if err != nil {
					return err
				}
				fmt.Printf("forgot %d saved password(s)\n", removed)
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
				return exit(errno.NewExit(errno.CodeError, fmt.Sprintf("no saved password for %s", key)))
			}
			fmt.Printf("forgot saved password for %s\n", key)
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
