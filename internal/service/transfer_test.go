package service

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maogou/ohmyssh/internal/credential"
	"github.com/maogou/ohmyssh/internal/sshclient"
)

// A transfer is a session with a different payload as far as the service layer
// is concerned: it resolves the same target, dials through the same password
// policy and wraps its failures the same way. What is asserted here is exactly
// that — the seams a fake transport can reach.
//
// The happy path cannot be driven from this package: reaching it means an SFTP
// session, and repository.Transport hands back a concrete *sshclient.SFTPClient
// that no fake can stand in for. It is covered instead against a real SFTP
// server in internal/sshclient/transfer_test.go, and from the command line in
// internal/command/transfer_test.go, where the service is the thing faked.

// writeSSHConfig writes a throwaway ssh config, so Transfer can resolve a target
// the way it does in production.
func writeSSHConfig(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// web1Config is the config file matching testHost.
func web1Config(t *testing.T) string {
	t.Helper()

	return writeSSHConfig(t, `
Host web1
    HostName 10.0.0.5
    User deploy
    Port 2222
`)
}

// noProgress is the reporter for tests that do not care what was reported.
func noProgress(sshclient.Progress) {}

// handshake is the working password in these tests: the saved one, and the one
// the fake transport accepts. Only the transport and the service come back: the
// host is testHost, and the saved password is already what the transport is set
// up to accept.
func handshake(t *testing.T) (*fakeTransport, *connectService) {
	t.Helper()

	host := testHost()
	transport := &fakeTransport{accept: savedPassword}
	creds := newFakeCredential(map[string]string{credential.Key(host): savedPassword})
	return transport, newTestConnect(t, transport, creds)
}

// A target naming an alias resolves to the host behind it, dials with the
// password saved for that login, and only then asks for a transfer session.
func TestTransferResolvesTheTargetAndUsesTheSavedPassword(t *testing.T) {
	transport, connect := handshake(t)
	connect.askPassword = neverAsk(t)

	_, err := connect.Transfer(
		context.Background(),
		ConnectOptions{ConfigPath: web1Config(t)},
		TransferRequest{Target: "web1", Local: t.TempDir(), Remote: "/tmp/out", Direction: sshclient.Upload},
		noProgress,
	)
	if err == nil {
		t.Fatal("Transfer succeeded without an sftp session")
	}
	if !strings.Contains(err.Error(), "web1") {
		t.Errorf("error = %v, want it to name the host", err)
	}

	if len(transport.attempts) != 1 || transport.attempts[0] != savedPassword {
		t.Errorf("dials = %v, want exactly [%s]", transport.attempts, savedPassword)
	}
	if transport.sftpCalls != 1 {
		t.Errorf("sftp sessions opened = %d, want 1", transport.sftpCalls)
	}
}

// An upload is measured from the local filesystem, so a path that is not there
// fails without opening anything. A mistyped source should not cost a round
// trip, and should not make the host look like the problem.
func TestUploadIsPlannedBeforeAnythingIsOpened(t *testing.T) {
	transport, connect := handshake(t)
	connect.askPassword = neverAsk(t)

	missing := filepath.Join(t.TempDir(), "not-there")
	_, err := connect.Transfer(
		context.Background(),
		ConnectOptions{ConfigPath: web1Config(t)},
		TransferRequest{Target: "web1", Local: missing, Remote: "/tmp/out", Direction: sshclient.Upload},
		noProgress,
	)
	if err == nil {
		t.Fatal("Transfer accepted a local path that does not exist")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("error = %v, want it to be the missing path", err)
	}
	if !strings.Contains(err.Error(), "web1") {
		t.Errorf("error = %v, want it to name the host", err)
	}
	if len(transport.attempts) != 0 {
		t.Errorf("dials = %v, want none: the plan failed first", transport.attempts)
	}
	if transport.sftpCalls != 0 {
		t.Errorf("sftp sessions opened = %d, want none", transport.sftpCalls)
	}
}

// A connection that never came up has no SFTP session to open.
func TestFailedDialNeverOpensATransferSession(t *testing.T) {
	refused := errors.New("dial tcp 10.0.0.5:2222: connect: connection refused")
	transport := &fakeTransport{dialErr: refused}
	connect := newTestConnect(t, transport, newFakeCredential(nil))
	connect.askPassword = neverAsk(t)

	_, err := connect.Transfer(
		context.Background(),
		ConnectOptions{ConfigPath: web1Config(t)},
		TransferRequest{Target: "web1", Local: t.TempDir(), Remote: "/tmp/out", Direction: sshclient.Upload},
		noProgress,
	)
	if !errors.Is(err, refused) {
		t.Errorf("error = %v, want the dial failure", err)
	}
	if transport.sftpCalls != 0 {
		t.Errorf("opened %d sftp sessions over a connection that never came up", transport.sftpCalls)
	}
}

// A transfer runs the same password policy as a session, --no-prompt included:
// a script pushing files cannot be interrupted by a prompt any more than one
// running a command can.
func TestTransferHonoursNoPrompt(t *testing.T) {
	host := testHost()
	transport := &fakeTransport{accept: typedPassword}
	creds := newFakeCredential(map[string]string{credential.Key(host): "stale-pw"})
	connect := newTestConnect(t, transport, creds)
	connect.askPassword = neverAsk(t)

	_, err := connect.Transfer(
		context.Background(),
		ConnectOptions{ConfigPath: web1Config(t), NoPrompt: true},
		TransferRequest{Target: "web1", Local: t.TempDir(), Remote: "/tmp/out", Direction: sshclient.Upload},
		noProgress,
	)
	if !sshclient.IsAuthFailure(err) {
		t.Errorf("error = %v, want the authentication failure", err)
	}
	if len(transport.attempts) != 1 {
		t.Errorf("dials = %v, want one attempt and no retry", transport.attempts)
	}
	if transport.sftpCalls != 0 {
		t.Errorf("sftp sessions opened = %d, want none", transport.sftpCalls)
	}
}

// The browser owns the terminal, so the session it browses over must never
// prompt: the prompt would be drawn behind the alt screen and both readers would
// fight over the same keystrokes. A host with no saved password is told what to
// do about it rather than being left to hang.
func TestOpenRemoteNeverPrompts(t *testing.T) {
	host := testHost()
	transport := &fakeTransport{accept: typedPassword}
	creds := newFakeCredential(map[string]string{credential.Key(host): "stale-pw"})
	connect := newTestConnect(t, transport, creds)
	connect.askPassword = neverAsk(t)

	// Note the options: prompting is allowed here. Turning it off is the file
	// view's own job, since it is the view that owns the terminal.
	_, err := connect.openRemote(
		context.Background(),
		host,
		ConnectOptions{},
		sshclient.DialOptions{},
	)
	if err == nil {
		t.Fatal("openRemote succeeded without a saved password")
	}
	if !strings.Contains(err.Error(), "no saved password for web1") {
		t.Errorf("error = %v, want the browser's own wording", err)
	}
	if !strings.Contains(err.Error(), "ohmyssh put") {
		t.Errorf("error = %v, want it to say how to fix this", err)
	}
	if len(transport.attempts) != 1 {
		t.Errorf("dials = %v, want one attempt and no prompt", transport.attempts)
	}
}

// The rewording is for an authentication failure only. Any other failure keeps
// the reason it came with, or a permission problem would read as a missing
// password.
func TestOpenRemoteReportsOtherFailuresAsThemselves(t *testing.T) {
	host := testHost()
	transport := &fakeTransport{accept: savedPassword}
	creds := newFakeCredential(map[string]string{credential.Key(host): savedPassword})
	connect := newTestConnect(t, transport, creds)
	connect.askPassword = neverAsk(t)

	_, err := connect.openRemote(
		context.Background(),
		host,
		ConnectOptions{},
		sshclient.DialOptions{},
	)
	if err == nil {
		t.Fatal("openRemote succeeded without an sftp session")
	}
	if strings.Contains(err.Error(), "no saved password") {
		t.Errorf("error = %v, want the real reason rather than the password wording", err)
	}
	if transport.sftpCalls != 1 {
		t.Errorf("sftp sessions opened = %d, want 1", transport.sftpCalls)
	}
}

// A target the config does not know is reported before anything is dialled.
func TestTransferRejectsAnUnknownTarget(t *testing.T) {
	transport, connect := handshake(t)
	connect.askPassword = neverAsk(t)

	_, err := connect.Transfer(
		context.Background(),
		ConnectOptions{ConfigPath: web1Config(t)},
		TransferRequest{Target: "nosuchhost", Local: t.TempDir(), Remote: "/tmp/out", Direction: sshclient.Upload},
		noProgress,
	)
	if err == nil {
		t.Fatal("Transfer accepted an alias that is not in the config")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want it to say the host was not found", err)
	}
	if len(transport.attempts) != 0 || transport.sftpCalls != 0 {
		t.Errorf("an unknown host still tried to connect: %v", transport.attempts)
	}
}
