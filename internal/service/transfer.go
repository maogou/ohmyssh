package service

import (
	"context"
	"fmt"
	"os"

	"github.com/maogou/ohmyssh/internal/config"
	"github.com/maogou/ohmyssh/internal/sshclient"
)

// TransferRequest is one file or directory tree moving between the local
// machine and a host.
type TransferRequest struct {
	// Target names the host: an alias from the ssh config, or a bare
	// [user@]host[:port]. It is resolved exactly the way connect resolves it.
	// It is empty for a transfer the browser asked for, which names the host
	// with the host itself rather than with a string it would have to re-resolve.
	Target string
	// Local and Remote are the two ends. Which of them is read and which is
	// written is what Direction decides.
	Local     string
	Remote    string
	Direction sshclient.Direction
}

// TransferResult reports what a finished transfer moved, so the caller can say
// so without having to have measured the tree itself.
type TransferResult struct {
	Files int
	Bytes int64
}

// Transfer moves req's source to its destination, reporting progress as it goes.
//
// It goes through the same dial and the same password policy as a session, so a
// host that has a saved password transfers without asking, and one that needs a
// password asks once for both. A cancelled context stops the transfer at the
// next chunk boundary.
func (s *connectService) Transfer(
	ctx context.Context,
	opts ConnectOptions,
	req TransferRequest,
	progress sshclient.ProgressFunc,
) (TransferResult, error) {
	host, all, err := s.target(opts.ConfigPath, req.Target)
	if err != nil {
		return TransferResult{}, err
	}
	return s.transferHost(ctx, host, opts, dialOptions(opts, all), req, progress)
}

// transferHost moves req against a host that has already been resolved and whose
// dial options have already been built.
//
// It is the one-shot shape the command line has: one dial, one transfer, and the
// connection goes away with it. The browser does not come through here — its
// file view holds a session open across many transfers instead, and reaches the
// same plan-and-copy tail through runPlan.
func (s *connectService) transferHost(
	ctx context.Context,
	host config.SSHHost,
	opts ConnectOptions,
	dial sshclient.DialOptions,
	req TransferRequest,
	progress sshclient.ProgressFunc,
) (TransferResult, error) {
	// An upload's source is local, so a path that is not there is caught before
	// anything is dialled: a typo should not cost a round trip, and should not
	// make the host look like the problem. The plan itself is built once there is
	// a session, because where a file lands depends on what the other end already
	// has — a remote path naming a directory takes the file inside it.
	if req.Direction == sshclient.Upload {
		if _, err := os.Stat(req.Local); err != nil {
			return TransferResult{}, fmt.Errorf("%s: local path: %w", host.Name, err)
		}
	}

	client, err := s.dialWithPrompt(ctx, host, opts, dial)
	if err != nil {
		return TransferResult{}, err
	}
	defer func() { _ = client.Close() }()

	session, err := s.transport.SFTP(client)
	if err != nil {
		return TransferResult{}, fmt.Errorf("%s: %w", host.Name, err)
	}
	// The SFTP session is closed first: it rides on the connection the defer
	// above releases.
	defer func() { _ = session.Close() }()

	return s.runPlan(ctx, session, host.Name, req, progress)
}

// runPlan measures req over an open session and copies what it measured.
//
// It is where both ways in meet: the command line arrives with a session it
// opened for this one transfer, the browser with the one its file view has been
// browsing over. hostName is only there to say which host failed, since an error
// from here is about a host the caller already knows by alias.
func (s *connectService) runPlan(
	ctx context.Context,
	session *sshclient.SFTPClient,
	hostName string,
	req TransferRequest,
	progress sshclient.ProgressFunc,
) (TransferResult, error) {
	var plan *sshclient.Plan
	var err error
	if req.Direction == sshclient.Upload {
		plan, err = session.PlanUpload(req.Local, req.Remote)
	} else {
		plan, err = session.PlanDownload(req.Remote, req.Local)
	}
	if err != nil {
		return TransferResult{}, fmt.Errorf("%s: %w", hostName, err)
	}

	if err := session.Run(ctx, plan, progress); err != nil {
		return TransferResult{}, fmt.Errorf("%s: %w", hostName, err)
	}
	return TransferResult{Files: plan.Files(), Bytes: plan.Bytes}, nil
}
