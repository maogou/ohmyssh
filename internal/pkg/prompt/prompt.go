// Package prompt reads secrets and confirmations from the terminal.
package prompt

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/term"
)

// Password reads a password without echoing it, returning an error when there
// is no terminal to read from. The prompt goes to stderr so it cannot be
// mistaken for program output, which keeps `ohmyssh exec` safe to pipe.
func Password(label string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("cannot prompt for a password: standard input is not a terminal")
	}

	fmt.Fprintf(os.Stderr, "%s's password: ", label)
	password, err := term.ReadPassword(fd)
	// ReadPassword leaves the cursor after the prompt; close the line either way.
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return string(password), nil
}
