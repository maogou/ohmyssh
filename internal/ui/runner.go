package ui

import (
	"context"
	"errors"
	"io"

	"github.com/maogou/ohmyssh/internal/config"
	"github.com/maogou/ohmyssh/internal/pkg/zlog"
)

// sessionCommand runs the session behind a selected host.
//
// It implements bubbletea's ExecCommand interface: tea.Exec calls
// SetStdin/SetStdout/SetStderr, restores the terminal to cooked mode, and only
// then calls Run, so the session owns a clean terminal for its lifetime.
type sessionCommand struct {
	host config.SSHHost
	run  SessionFunc

	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

// SetStdin satisfies bubbletea.ExecCommand.
func (c *sessionCommand) SetStdin(r io.Reader) { c.stdin = r }

// SetStdout satisfies bubbletea.ExecCommand.
func (c *sessionCommand) SetStdout(w io.Writer) { c.stdout = w }

// SetStderr satisfies bubbletea.ExecCommand.
func (c *sessionCommand) SetStderr(w io.Writer) { c.stderr = w }

// Run connects and blocks until the session ends. The dial is bounded by the
// connection timeout rather than a context deadline, because the session
// itself must outlive any connection timeout.
func (c *sessionCommand) Run() error {
	if c.run == nil {
		return errors.New("no session runner configured")
	}

	if err := c.run(context.Background(), c.host); err != nil {
		zlog.L().Error().Err(err).Str("host", c.host.Name).Msg("connection failed")
		return err
	}
	return nil
}
