package command

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/maogou/ohmyssh/internal/service"
	"github.com/maogou/ohmyssh/internal/sshclient"
	"github.com/maogou/ohmyssh/internal/ui"
)

// put and get name the direction, scp works it out from which side names a host.
// All three end up in runTransfer, so the password policy, the progress display
// and the wording of the result are the same whichever one was typed.

func putCommand(connect service.ConnectService) *cli.Command {
	return &cli.Command{
		Name:      "put",
		Usage:     "Upload a file or a directory tree to a host",
		ArgsUsage: "<host> <local> <remote>",
		Description: `Copy local to remote, recursively when local is a directory.

Progress is reported on stderr, so stdout stays clean for the summary line:

  ohmyssh put web1 ./deploy.sh /tmp/deploy.sh
  ohmyssh put web1 ./out/ /opt/app/

A directory copies its contents into the remote path, not into a directory named
after it: the two lines above leave the files directly under /opt/app. A remote
path ending in a slash is a directory, so a single file keeps its own name
inside it. Existing files are overwritten.

A password can be piped in rather than typed, which keeps it out of a process
list it would otherwise be visible in:

  echo "$PW" | ohmyssh put --password-stdin web1 ./out/ /opt/app/`,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args().Slice()
			if len(args) != 3 {
				return usageError(
					"put requires a host, a local path and a remote path: ohmyssh put <host> <local> <remote>",
				)
			}
			return runTransfer(ctx, cmd, connect, sshclient.Upload, args[0], args[1], args[2])
		},
	}
}

func getCommand(connect service.ConnectService) *cli.Command {
	return &cli.Command{
		Name:      "get",
		Usage:     "Download a file or a directory tree from a host",
		ArgsUsage: "<host> <remote> <local>",
		Description: `Copy remote to local, recursively when remote is a directory.

It is put with the ends swapped, and the same rules apply:

  ohmyssh get web1 /var/log/app.log ./app.log
  ohmyssh get web1 /opt/app/ ./app/

A password can be piped in rather than typed, which keeps it out of a process
list it would otherwise be visible in:

  echo "$PW" | ohmyssh get --password-stdin web1 /var/log/app.log ./app.log

A directory copies its contents into the local path. A local path ending in a
separator is a directory, so a single file keeps its own name inside it.
Existing files are overwritten.`,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args().Slice()
			if len(args) != 3 {
				return usageError(
					"get requires a host, a remote path and a local path: ohmyssh get <host> <remote> <local>",
				)
			}
			return runTransfer(ctx, cmd, connect, sshclient.Download, args[0], args[2], args[1])
		},
	}
}

func scpCommand(connect service.ConnectService) *cli.Command {
	return &cli.Command{
		Name:      "scp",
		Usage:     "Copy between here and a host, naming each end the way scp does",
		ArgsUsage: "<src> <dst>",
		Description: `Copy two paths, exactly one of which names a host as host:path.

  ohmyssh scp ./deploy.sh web1:/tmp/deploy.sh
  ohmyssh scp web1:/var/log/app.log ./app.log

The direction follows from which side names a host, so scp is put and get with
nothing to decide. A path with no colon is local; a path with an empty one,
web1:, is the host's home directory.

A password can be piped in rather than typed, which keeps it out of a process
list it would otherwise be visible in:

  echo "$PW" | ohmyssh scp --password-stdin web1:/var/log/app.log ./app.log`,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args().Slice()
			if len(args) != 2 {
				return usageError("scp requires a source and a destination: ohmyssh scp <src> <dst>")
			}

			srcHost, srcPath, srcRemote := parseTransferTarget(args[0])
			dstHost, dstPath, dstRemote := parseTransferTarget(args[1])

			switch {
			case srcRemote && dstRemote:
				return usageError(
					"scp copies between here and a host, not between two hosts: " + args[0] + " and " + args[1] + " both name one",
				)
			case srcRemote:
				// Remote to local: args[1] is the local end.
				return runTransfer(ctx, cmd, connect, sshclient.Download, srcHost, args[1], srcPath)
			case dstRemote:
				return runTransfer(ctx, cmd, connect, sshclient.Upload, dstHost, args[0], dstPath)
			default:
				return usageError(
					"scp needs one end to name a host: " + args[0] + " and " + args[1] + " are both local",
				)
			}
		},
	}
}

// parseTransferTarget splits an scp-style host:path, reporting ok=false for a
// token that names no host. A bare "web1:" means the host's home directory.
//
// The Windows drive-letter test is what keeps a local C:\src from being read as
// a host called C, which is the one way this parse is ambiguous.
func parseTransferTarget(token string) (host, remotePath string, ok bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", "", false
	}

	if isDriveLetter(token) {
		return "", "", false
	}
	colon := strings.Index(token, ":")
	if colon <= 0 {
		return "", "", false
	}

	host, remotePath = token[:colon], token[colon+1:]
	// A slash in the host means this is a path that happens to contain a colon,
	// not an address: no host name has one.
	if strings.ContainsAny(host, `/\`) {
		return "", "", false
	}
	if remotePath == "" {
		remotePath = "."
	}
	return host, remotePath, true
}

// isDriveLetter reports whether a token starts with a Windows volume such as
// "C:\" or "C:/". Only those two shapes are caught: "C:file" is a drive-relative
// path, but it is indistinguishable from an scp target on a host called C, and
// scp's reading — host C, path file — is the one that carries information.
func isDriveLetter(token string) bool {
	if len(token) < 2 || token[1] != ':' {
		return false
	}
	if !isASCIILetter(token[0]) {
		return false
	}
	return len(token) == 2 || token[2] == '/' || token[2] == '\\'
}

func isASCIILetter(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

// runTransfer performs one transfer, reporting progress and the result on
// stderr.
//
// local and remote are the two ends by role — which of them is read and which is
// written is what direction decides. The summary quotes them in the order the
// bytes actually travel, so a get does not appear to run backwards.
func runTransfer(
	ctx context.Context,
	cmd *cli.Command,
	connect service.ConnectService,
	direction sshclient.Direction,
	target, local, remote string,
) error {
	// stderr rather than stdout, so the result can be piped somewhere useful
	// while the bar stays on the terminal. Taken from the command rather than
	// from os.Stderr directly: the root is where urfave/cli resolves that stream,
	// so a test that installs a buffer there is not left reading progress frames
	// off the test log.
	out := cmd.Root().ErrWriter
	if out == nil {
		out = os.Stderr
	}
	// Collected before the display is built: a command line that cannot be acted
	// on has no transfer to draw progress for.
	opts, err := connectOptions(cmd)
	if err != nil {
		return err
	}

	progress := ui.NewTransferProgress(out)
	req := service.TransferRequest{
		Target:    target,
		Local:     local,
		Remote:    remote,
		Direction: direction,
	}

	result, err := connect.Transfer(ctx, opts, req, progress.Update)
	// The live display has to be closed before anything else is written, or the
	// message lands on top of the progress bar.
	progress.Done()

	from, to := ui.TransferEnds(direction, local, remote)
	if err != nil {
		// The failure goes back as an error rather than being printed here, so
		// the runner adds its "ohmyssh: " prefix and the exit status in one
		// line, the way every other failure in this CLI is reported.
		return fmt.Errorf("%s %s → %s: %w", direction.Verb(), from, to, err)
	}

	// A summary that could not be written is not a transfer that failed, so the
	// write is dropped rather than reported as one.
	_, _ = fmt.Fprintln(out, ui.TransferSummary{
		Direction: direction,
		From:      from,
		To:        to,
		Files:     result.Files,
		Bytes:     result.Bytes,
	}.Report())
	return nil
}
