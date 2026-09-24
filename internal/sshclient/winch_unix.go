//go:build !windows

package sshclient

import (
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/crypto/ssh"
	"golang.org/x/term"

	"github.com/maogou/ohmyssh/internal/pkg/zlog"
)

// watchWindowSize forwards local SIGWINCH resizes to the remote PTY. The
// returned function stops watching and must be called before the session ends.
func watchWindowSize(fd int, session *ssh.Session, enabled bool) func() {
	if !enabled {
		return func() {}
	}

	changes := make(chan os.Signal, 1)
	signal.Notify(changes, syscall.SIGWINCH)
	done := make(chan struct{})

	go func() {
		for {
			select {
			case <-done:
				return
			case <-changes:
				width, height := terminalSize(fd)
				if err := session.WindowChange(height, width); err != nil {
					zlog.L().Debug().Err(err).Msg("resize remote pty")
					continue
				}
				zlog.L().Debug().Int("width", width).Int("height", height).Msg("remote pty resized")
			}
		}
	}()

	// Send the current size once: the PTY was opened with it, but a resize that
	// happened between GetSize and RequestPty would otherwise be lost.
	if width, height, err := term.GetSize(fd); err == nil && width > 0 && height > 0 {
		_ = session.WindowChange(height, width)
	}

	return func() {
		signal.Stop(changes)
		close(done)
	}
}
