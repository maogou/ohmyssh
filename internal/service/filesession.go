package service

import (
	"context"
	"fmt"

	"github.com/maogou/ohmyssh/internal/config"
	"github.com/maogou/ohmyssh/internal/sshclient"
	"github.com/maogou/ohmyssh/internal/ui"
)

// openRemote dials host and leaves an SFTP session open on it for the browser's
// file view.
//
// Prompting is turned off, the way it is for every transfer the browser starts:
// the browser owns the terminal — the alt screen is up and the keyboard is in
// raw mode — so a password prompt would be drawn behind it and the two readers
// would fight over the same keystrokes. A host with a saved password, which is
// every host connected to from here, browses and transfers without being asked;
// one without is told what to do about it.
//
// Nothing is closed here on the way out. The session owns both the SFTP channel
// and the connection under it, and hands them back together through Close.
func (s *connectService) openRemote(
	ctx context.Context,
	host config.SSHHost,
	opts ConnectOptions,
	dial sshclient.DialOptions,
) (ui.RemoteSession, error) {
	opts.NoPrompt = true

	client, err := s.dialWithPrompt(ctx, host, opts, dial)
	if err != nil {
		if sshclient.IsAuthFailure(err) {
			return nil, fmt.Errorf(
				"no saved password for %s: connect to it once, or use ohmyssh put or get from a shell",
				host.Name,
			)
		}
		return nil, err
	}

	session, err := s.transport.SFTP(client)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("%s: %w", host.Name, err)
	}

	// Where the login landed, which is the home directory the remote pane opens
	// in. A server that will not answer is given the root rather than an error:
	// the pane is still worth showing, and the first directory the user opens
	// will say what the server does object to.
	home, err := session.RealPath(".")
	if err != nil {
		home = "/"
	}

	return &remoteSession{
		client:   client,
		sftp:     session,
		home:     home,
		service:  s,
		hostName: host.Name,
	}, nil
}

// remoteSession is one host connection held open for as long as the file view is
// up, so that walking a tree costs one dial rather than one per directory, and
// so that every file the user sends or fetches rides the connection the pane was
// browsed over.
type remoteSession struct {
	client *sshclient.Client
	sftp   *sshclient.SFTPClient
	home   string

	// service and hostName are what a transfer started from the view needs to
	// reach the same plan-and-copy tail the command line uses, so that a file
	// moved from the pane and one moved by put are moved by the same code.
	service  *connectService
	hostName string
}

func (r *remoteSession) Home() string { return r.home }

// ReadDir converts a listing into the shape the pane draws. The protocol
// carries more per entry than the pane shows, and converting here is what keeps
// sftp out of the ui package.
func (r *remoteSession) ReadDir(path string) ([]ui.RemoteEntry, error) {
	entries, err := r.sftp.ReadDir(path)
	if err != nil {
		return nil, err
	}

	listed := make([]ui.RemoteEntry, 0, len(entries))
	for _, entry := range entries {
		listed = append(listed, ui.RemoteEntry{
			Name: entry.Name(),
			Dir:  entry.IsDir(),
			Size: entry.Size(),
		})
	}
	return listed, nil
}

// Transfer moves one path over the session's own channel. It is the one-shot
// transfer's plan-and-copy tail with the dial already behind it.
func (r *remoteSession) Transfer(ctx context.Context, req ui.TransferRequest, progress sshclient.ProgressFunc) error {
	_, err := r.service.runPlan(ctx, r.sftp, r.hostName, TransferRequest{
		Local:     req.Local,
		Remote:    req.Remote,
		Direction: req.Direction,
	}, progress)
	return err
}

// Close releases the channel and the connection under it, in that order: the
// SFTP session rides on the connection, so closing the connection first would
// leave it talking to a socket that has gone away.
//
// The fields are cleared as they go, which is what makes a second Close — the
// view closing on the way out, the browser closing what is left at exit — a
// no-op rather than a second teardown of the same connection.
func (r *remoteSession) Close() error {
	sftp, client := r.sftp, r.client
	r.sftp, r.client = nil, nil
	if sftp == nil {
		return nil
	}

	err := sftp.Close()
	if client != nil {
		if closeErr := client.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}
	return err
}
