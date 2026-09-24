// Command ohmyssh connects to hosts defined in an OpenSSH config file.
package main

import (
	"os"

	"github.com/maogou/ohmyssh/internal/command"
)

func main() {
	os.Exit(command.Run(os.Args))
}
