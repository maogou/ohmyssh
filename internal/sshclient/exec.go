package sshclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/maogou/ohmyssh/internal/pkg/zlog"
)

// RunCommand executes command on the remote host without a PTY and streams its
// output. The command is a positional vector, not a shell string, and is joined
// with spaces for the remote shell exactly as OpenSSH does when given trailing
// arguments.
//
// A non-zero remote exit status is returned as an *ExitError.
func RunCommand(ctx context.Context, client *ssh.Client, command []string, stdout, stderr io.Writer) error {
	if len(command) == 0 {
		return errors.New("no command given")
	}

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("open ssh session: %w", err)
	}
	defer func() { _ = session.Close() }()

	session.Stdout = stdout
	session.Stderr = stderr

	line := strings.Join(command, " ")
	zlog.L().Debug().Str("command", line).Msg("running remote command")

	if err := session.Start(line); err != nil {
		return fmt.Errorf("start %q: %w", line, err)
	}

	// Cancelling the context closes the session, which makes Wait return.
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		select {
		case <-ctx.Done():
			// Ask politely first; Close in the defer guarantees termination.
			_ = session.Signal(ssh.SIGTERM)
			_ = session.Close()
		case <-stopped:
		}
	}()

	err = session.Wait()
	if err != nil {
		// A cancellation is reported ahead of the failure it caused: closing the
		// session is what made Wait return, and "context canceled" says why where
		// the session's own teardown error would not.
		//
		// It is asked only once Wait has failed. Asked unconditionally, it
		// outranks a command that finished — one that printed its last line in
		// the moment ctrl+c arrived — and reports a success as an interruption,
		// which is a status a script reading the exit code acts on.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if exitErr, ok := errors.AsType[*ssh.ExitError](err); ok {
			return &ExitError{Code: exitErr.ExitStatus(), Err: err}
		}
		return err
	}
	return nil
}

// IsAuthFailure reports whether err came from the server rejecting our
// credentials, as opposed to a network or host key problem. Callers use this to
// decide whether prompting for a password is worth a retry.
func IsAuthFailure(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := errors.AsType[*ssh.ServerAuthError](err); ok {
		return true
	}
	if _, ok := errors.AsType[*ssh.ExitError](err); ok {
		return false
	}
	return strings.Contains(err.Error(), "unable to authenticate")
}
