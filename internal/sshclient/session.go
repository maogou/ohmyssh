package sshclient

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/muesli/cancelreader"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"

	"github.com/maogou/ohmyssh/internal/pkg/zlog"
)

// defaultTermSize is used when no local terminal size can be determined.
const (
	defaultTermWidth  = 80
	defaultTermHeight = 24
)

// ExitError carries the exit status reported by the remote command.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return fmt.Sprintf("remote command exited with status %d", e.Code)
}

func (e *ExitError) Unwrap() error { return e.Err }

// ExitCode extracts the remote exit status from err, defaulting to 1 for any
// non-exit failure so callers can map it straight onto os.Exit.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := errors.AsType[*ExitError](err); ok {
		return exitErr.Code
	}
	return 1
}

// InteractiveSession runs a login shell or a single command on a remote host
// attached to the local terminal.
//
// It implements bubbletea's ExecCommand interface, so a TUI can hand the
// terminal over to it with tea.Exec and reclaim it once the session ends.
type InteractiveSession struct {
	client  *ssh.Client
	command []string
	stdin   io.Reader
	stdout  io.Writer
	stderr  io.Writer
}

// NewInteractiveSession prepares a session. An empty command opens the login
// shell; otherwise the command runs under a PTY like `ssh -t`.
func NewInteractiveSession(client *ssh.Client, command []string) *InteractiveSession {
	return &InteractiveSession{
		client:  client,
		command: command,
		stdin:   os.Stdin,
		stdout:  os.Stdout,
		stderr:  os.Stderr,
	}
}

// SetStdin satisfies bubbletea.ExecCommand.
func (s *InteractiveSession) SetStdin(r io.Reader) { s.stdin = r }

// SetStdout satisfies bubbletea.ExecCommand.
func (s *InteractiveSession) SetStdout(w io.Writer) { s.stdout = w }

// SetStderr satisfies bubbletea.ExecCommand.
func (s *InteractiveSession) SetStderr(w io.Writer) { s.stderr = w }

// Run opens the session and blocks until it ends. The remote exit status is
// returned as an *ExitError so the caller can propagate it.
func (s *InteractiveSession) Run() error {
	session, err := s.client.NewSession()
	if err != nil {
		return fmt.Errorf("open ssh session: %w", err)
	}
	defer func() { _ = session.Close() }()

	// A PTY is only appropriate when we are attached to a terminal; without one
	// the session should behave like a pipe so redirection works.
	stdinFD, stdinIsTTY := terminalFile(s.stdin)
	stdoutFD, stdoutIsTTY := terminalFile(s.stdout)
	sizeFD := stdinFD
	if !stdinIsTTY && stdoutIsTTY {
		sizeFD = stdoutFD
	}

	if stdinIsTTY || stdoutIsTTY {
		width, height := terminalSize(sizeFD)
		modes := ssh.TerminalModes{
			// The local terminal is raw, so the remote must echo keystrokes back.
			ssh.ECHO:          1,
			ssh.TTY_OP_ISPEED: 14400,
			ssh.TTY_OP_OSPEED: 14400,
		}
		if err := session.RequestPty(termType(), height, width, modes); err != nil {
			return fmt.Errorf("request pty: %w", err)
		}
		zlog.L().Debug().Int("width", width).Int("height", height).Str("term", termType()).Msg("pty requested")
	}

	// stdin is read through a pipe of our own rather than handed to x/crypto/ssh
	// as it is. That package copies whatever Stdin holds into the session channel
	// on a goroutine of its own and never joins it, so a session that has ended
	// can still be reading the local terminal: it takes the keystroke meant for
	// whoever owns the terminal next — in the host browser, the first key pressed
	// after a session returns — and it reads a file the caller may by then be
	// closing, which is a data race rather than only a lost key.
	//
	// Owning the read is what makes it ours to stop *and to wait for*: Run does
	// not return while anything of ours is still reading the caller's stdin.
	// What ssh copies from is then an in-process pipe, which it can read as long
	// as it likes without touching anything the caller owns.
	stopStdin := func() {}
	if cancelable, err := cancelreader.NewReader(s.stdin); err == nil {
		reader, writer := io.Pipe()
		pump := make(chan struct{})
		go func() {
			defer close(pump)

			_, err := io.Copy(writer, cancelable)
			if errors.Is(err, cancelreader.ErrCanceled) {
				// The session is ending rather than the input: end of input is
				// what the far side of this pipe should see.
				err = nil
			}
			_ = writer.CloseWithError(err)
		}()

		var once sync.Once
		stopStdin = func() {
			once.Do(
				func() {
					// The writer first: it releases a pump that is handing a
					// buffer over rather than reading, and it is what tells a
					// session still copying from the pipe that stdin has ended.
					_ = writer.CloseWithError(io.EOF)
					// Cancel reports whether it managed to interrupt a read in
					// flight, and that is what makes waiting safe. An input it
					// cannot interrupt — anything that is not a file, where it
					// can only refuse later reads — would leave the pump parked
					// in the read it is already in, and Run waiting on it for
					// good.
					if cancelable.Cancel() {
						<-pump
					}
					_ = cancelable.Close()
				},
			)
		}
		session.Stdin = reader
	} else {
		zlog.L().Debug().Err(err).Msg("stdin cannot be canceled; a key may be lost after this session")
		session.Stdin = s.stdin
	}
	// Covers the early returns below; the happy path stops it explicitly.
	defer stopStdin()

	session.Stdout = s.stdout
	session.Stderr = s.stderr

	// Put the local terminal into raw mode so keystrokes reach the remote
	// unbuffered. bubbletea has already released the terminal at this point.
	var restore func()
	if stdinIsTTY {
		oldState, err := term.MakeRaw(stdinFD)
		if err != nil {
			return fmt.Errorf("set terminal to raw mode: %w", err)
		}
		restore = func() { _ = term.Restore(stdinFD, oldState) }
	}

	stopWinch := watchWindowSize(sizeFD, session, stdinIsTTY || stdoutIsTTY)

	if len(s.command) == 0 {
		zlog.L().Debug().Msg("starting remote shell")
		if err := session.Shell(); err != nil {
			stopWinch()
			if restore != nil {
				restore()
			}
			return fmt.Errorf("start shell: %w", err)
		}
	} else {
		command := strings.Join(s.command, " ")
		zlog.L().Debug().Str("command", command).Msg("starting remote command")
		if err := session.Start(command); err != nil {
			stopWinch()
			if restore != nil {
				restore()
			}
			return fmt.Errorf("start command %q: %w", command, err)
		}
	}

	err = session.Wait()
	stopWinch()
	// Release the local stdin pump before the terminal changes hands again.
	stopStdin()
	if restore != nil {
		restore()
	}

	if err != nil {
		if exitErr, ok := errors.AsType[*ssh.ExitError](err); ok {
			return &ExitError{Code: exitErr.ExitStatus(), Err: err}
		}
		if missing, ok := errors.AsType[*ssh.ExitMissingError](err); ok {
			// The server closed without reporting a status; treat as a failure
			// but surface the cause rather than a bare code.
			return &ExitError{Code: 1, Err: fmt.Errorf("session ended without exit status: %w", missing)}
		}
		return err
	}
	return nil
}

// terminalFile returns the file descriptor behind v and whether it is a
// terminal. v is an io.Reader for stdin and an io.Writer for stdout, so the
// parameter stays untyped; only the descriptor is of any use to the caller.
func terminalFile(v any) (int, bool) {
	f, ok := v.(*os.File)
	if !ok {
		return 0, false
	}
	fd := int(f.Fd())
	return fd, term.IsTerminal(fd)
}

// terminalSize reads the local window size, falling back to 80x24.
func terminalSize(fd int) (width, height int) {
	width, height = defaultTermWidth, defaultTermHeight
	if w, h, err := term.GetSize(fd); err == nil && w > 0 && h > 0 {
		return w, h
	}
	return width, height
}

// termType reports the terminal type to request, defaulting to xterm-256color.
func termType() string {
	if t := os.Getenv("TERM"); t != "" {
		return t
	}
	return "xterm-256color"
}
