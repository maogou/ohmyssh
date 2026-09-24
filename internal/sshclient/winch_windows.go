//go:build windows

package sshclient

import "golang.org/x/crypto/ssh"

// watchWindowSize is a no-op on Windows, which has no SIGWINCH. The PTY is
// opened at the size captured in Run and is not resized afterwards.
func watchWindowSize(_ int, _ *ssh.Session, _ bool) func() {
	return func() {}
}
