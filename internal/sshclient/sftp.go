package sshclient

import (
	"fmt"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/maogou/ohmyssh/internal/pkg/zlog"
)

// sftpServerPaths are where OpenSSH's sftp-server binary lives, in the order
// they are worth trying. They exist so a host whose sshd_config has no
// "Subsystem sftp" line can still be transferred to: the subsystem request is
// refused, but the binary is usually there to be run by hand.
var sftpServerPaths = []string{
	"/usr/lib/openssh/sftp-server",
	"/usr/libexec/openssh/sftp-server",
	"/usr/libexec/sftp-server",
	"/usr/lib/sftp-server",
	"/usr/local/libexec/sftp-server",
}

// SFTPClient is an SFTP session riding on an established SSH connection.
//
// It does not own that connection: Close shuts the SFTP channel and leaves the
// SSH client alone, because the same connection may outlive it — and because
// sshclient.Client.Close is what releases a ProxyJump's bastions.
type SFTPClient struct {
	*sftp.Client

	// session is the sftp-server we started ourselves when the host had no
	// working subsystem. The pipes the SFTP client reads and writes belong to
	// it, so it has to be closed with the client rather than left behind.
	session *ssh.Session
}

// sftpOptions are the settings both ways of starting a session share.
//
// Concurrent writes are off unless asked for, and this is the asking: a file
// being sent is read in parallel requests instead of one packet per round trip.
// It only pays off when the copy is driven by io.Copy with a source that can say
// how much is left — which is how the transfer code is written — because
// pkg/sftp otherwise falls back to writing one packet at a time.
//
// Reads need no option here: they are concurrent unless turned off.
var sftpOptions = []sftp.ClientOption{sftp.UseConcurrentWrites(true)}

// NewSFTPClient opens an SFTP session on client.
//
// The subsystem is tried first, which is what a properly configured host
// answers to. Hosts whose sshd_config carries no "Subsystem sftp" refuse that
// request, so sftp-server is then started by name and its stdio handed to the
// SFTP client instead.
func NewSFTPClient(client *Client) (*SFTPClient, error) {
	if client == nil || client.Client == nil {
		return nil, fmt.Errorf("sftp: no ssh connection")
	}

	sc, err := sftp.NewClient(client.Client, sftpOptions...)
	if err == nil {
		zlog.L().Debug().Str("host", client.Host.Name).Msg("sftp subsystem started")
		return &SFTPClient{Client: sc}, nil
	}
	zlog.L().Debug().Err(err).Str("host", client.Host.Name).Msg("sftp subsystem refused; trying sftp-server")
	return startSFTPServer(client.Client, client.Host.Name, err)
}

// startSFTPServer runs sftp-server over a session channel and uses its stdio as
// the transport. subsystemErr is the refusal that sent us here; it is the error
// reported when every path fails too, since it is the one that says what the
// host actually objected to.
func startSFTPServer(client *ssh.Client, host string, subsystemErr error) (*SFTPClient, error) {
	lastErr := subsystemErr
	for _, path := range sftpServerPaths {
		session, err := client.NewSession()
		if err != nil {
			lastErr = err
			continue
		}

		stdin, err := session.StdinPipe()
		if err != nil {
			_ = session.Close()
			lastErr = err
			continue
		}
		stdout, err := session.StdoutPipe()
		if err != nil {
			_ = session.Close()
			lastErr = err
			continue
		}
		if err := session.Start(path); err != nil {
			_ = session.Close()
			lastErr = err
			continue
		}

		sc, err := sftp.NewClientPipe(stdout, stdin, sftpOptions...)
		if err != nil {
			_ = session.Close()
			lastErr = err
			continue
		}
		zlog.L().Debug().Str("host", host).Str("path", path).Msg("sftp-server started by hand")
		return &SFTPClient{Client: sc, session: session}, nil
	}

	return nil, fmt.Errorf("start sftp on %s: %w", host, lastErr)
}

// Close shuts the SFTP session. The SSH connection underneath is left open.
func (c *SFTPClient) Close() error {
	if c == nil || c.Client == nil {
		return nil
	}
	err := c.Client.Close()
	if c.session != nil {
		// The session is only non-nil on the hand-started path, where it owns
		// the pipes the client was reading.
		if closeErr := c.session.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
		c.session = nil
	}
	return err
}
