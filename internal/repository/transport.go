package repository

import (
	"context"
	"io"

	"github.com/maogou/ohmyssh/internal/config"
	"github.com/maogou/ohmyssh/internal/sshclient"
)

// Transport moves bytes to and from a remote host.
//
// Dial returns the concrete *sshclient.Client rather than an interface of our
// own. This seam exists to make the dialling *policy* replaceable, not to
// abstract SSH away: wrapping the client too would add a layer that only ever
// forwards calls.
type Transport interface {
	Dial(ctx context.Context, host config.SSHHost, opts sshclient.DialOptions) (*sshclient.Client, error)
	RunCommand(ctx context.Context, client *sshclient.Client, command []string, stdout, stderr io.Writer) error
	Interactive(client *sshclient.Client, command []string) *sshclient.InteractiveSession
	// SFTP opens a file transfer session over an already-established
	// connection, so a transfer reuses the same host key checks, proxy
	// traversal and authentication the shell did.
	SFTP(client *sshclient.Client) (*sshclient.SFTPClient, error)
}

// NewTransport returns the SSH-backed transport.
func NewTransport() Transport { return transport{} }

// transport is stateless: sshclient keeps no per-connection state of its own,
// everything lives on the connection it returns.
type transport struct{}

func (transport) Dial(ctx context.Context, host config.SSHHost, opts sshclient.DialOptions) (*sshclient.Client, error) {
	return sshclient.Dial(ctx, host, opts)
}

// client.Client is the embedded *ssh.Client: unwrapping it here keeps callers
// from having to know how sshclient.Client is put together.
func (transport) RunCommand(ctx context.Context, client *sshclient.Client, command []string, stdout, stderr io.Writer) error {
	return sshclient.RunCommand(ctx, client.Client, command, stdout, stderr)
}

func (transport) Interactive(client *sshclient.Client, command []string) *sshclient.InteractiveSession {
	return sshclient.NewInteractiveSession(client.Client, command)
}

func (transport) SFTP(client *sshclient.Client) (*sshclient.SFTPClient, error) {
	return sshclient.NewSFTPClient(client)
}
