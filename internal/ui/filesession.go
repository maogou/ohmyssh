package ui

import (
	"context"

	"github.com/maogou/ohmyssh/internal/config"
	"github.com/maogou/ohmyssh/internal/sshclient"
)

// RemoteEntry is one entry the file view's remote pane lists. It is the
// browser's own shape rather than the protocol's: a pane needs a name, whether
// it is a directory and how big it is, and nothing else an SFTP listing carries
// is drawn.
type RemoteEntry struct {
	Name string
	Dir  bool
	Size int64
}

// RemoteSession is a connection the file view walks and copies over.
//
// It is opened when the view goes up and closed when it comes down, which is
// what makes browsing a directory tree one dial rather than one per directory. A
// transfer the user starts reuses this connection too, so a host with a saved
// password moves every file of a browse without being asked again.
//
// Transfer is called on a goroutine of the view's own, and ctx is cancelled when
// the user backs out of it: an implementation must return once ctx is done and
// must not wait on the terminal, which the view still owns.
type RemoteSession interface {
	// Home is the directory the remote pane opens in: where the login landed,
	// which is the user's home directory. It is read when the session is built
	// rather than asked for here, so that drawing a frame never waits on a
	// round trip.
	Home() string
	// ReadDir lists one remote directory. Paths are the pane's own, so the
	// listing is of exactly the string the pane is showing.
	ReadDir(path string) ([]RemoteEntry, error)
	// Transfer moves one path, reporting progress as it goes. Which way the
	// bytes go is what req.Direction says, as it is everywhere else.
	Transfer(ctx context.Context, req TransferRequest, progress sshclient.ProgressFunc) error
	// Close releases the session and the connection under it.
	Close() error
}

// RemoteSessionFunc opens a session on a host for the file view.
//
// It is the browser's own type rather than the service's, for the same reason
// SessionFunc is: service imports ui and not the other way round. Passing a nil
// one leaves the upload and download keys saying that transfers are
// unavailable, rather than opening a view that could not fill itself.
type RemoteSessionFunc func(ctx context.Context, host config.SSHHost) (RemoteSession, error)
