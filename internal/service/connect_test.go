package service

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/maogou/ohmyssh/internal/config"
	"github.com/maogou/ohmyssh/internal/credential"
	"github.com/maogou/ohmyssh/internal/pkg/errno"
	"github.com/maogou/ohmyssh/internal/repository"
	"github.com/maogou/ohmyssh/internal/sshclient"
	"github.com/rs/zerolog"
	"golang.org/x/crypto/ssh"
)

// The tests in this file drive the password policy: which password is tried,
// when the user is asked, and what gets written back to the store. That policy
// used to be welded to the CLI, so none of it was covered. Dialling is the seam
// that makes it reachable — every branch below is decided before a session is
// opened, so a transport that answers dials instead of connecting is enough.

const (
	savedPassword = "saved-pw"
	flagPassword  = "flag-pw"
	typedPassword = "typed-pw"
)

func testHost() config.SSHHost {
	return config.SSHHost{Name: "web1", Hostname: "10.0.0.5", User: "deploy", Port: "2222"}
}

// authFailure is the error a real server produces when it rejects every method
// offered. IsAuthFailure recognises it, which is what allows a retry.
func authFailure() error {
	return &ssh.ServerAuthError{Errors: []error{errors.New("no supported methods remain")}}
}

// fakeTransport records the password each dial carried. accept is the one that
// succeeds; dialErr forces a failure that is not an authentication failure.
type fakeTransport struct {
	accept   string
	dialErr  error
	attempts []string
	// runErr is what a command run over an already connected client reports.
	runErr error
	// sftpCalls counts transfer sessions asked for. A transfer that never got
	// past the dial must leave it at zero.
	sftpCalls int
}

func (f *fakeTransport) Dial(_ context.Context, _ config.SSHHost, opts sshclient.DialOptions) (*sshclient.Client, error) {
	f.attempts = append(f.attempts, opts.Password)
	if f.dialErr != nil {
		return nil, f.dialErr
	}
	if opts.Password == f.accept {
		// A bare client is enough: nothing past the dial is exercised here, and
		// Close tolerates the nil connection inside it.
		return &sshclient.Client{}, nil
	}
	return nil, authFailure()
}

// RunCommand and Interactive exist only to satisfy the interface. Opening a
// session needs a real connection, and those paths are covered by the
// in-process SSH server tests in internal/sshclient.
func (f *fakeTransport) RunCommand(_ context.Context, _ *sshclient.Client, _ []string, _, _ io.Writer) error {
	return f.runErr
}

func (*fakeTransport) Interactive(_ *sshclient.Client, _ []string) *sshclient.InteractiveSession {
	return nil
}

// SFTP reports that no transfer can start here: reaching it would mean a dial
// succeeded, and the path past a dial needs a real connection. It counts the
// attempt all the same, which is how a test asserts that a failed dial stayed
// on its side of the line.
func (f *fakeTransport) SFTP(_ *sshclient.Client) (*sshclient.SFTPClient, error) {
	f.sftpCalls++
	return nil, errors.New("no sftp in tests")
}

// fakeCredential stands in for the encrypted file store, so a test never needs
// to point XDG_CONFIG_HOME at a temporary directory.
type fakeCredential struct {
	saved map[string]string
	// sets records, in order, the keys passed to Set.
	sets []string
	// setErr makes the store refuse every write, for the branch where saving a
	// password fails after a connection that worked. setCalls counts the
	// attempts, so a test can tell "refused" from "never asked".
	setErr   error
	setCalls int
}

func newFakeCredential(saved map[string]string) *fakeCredential {
	if saved == nil {
		saved = map[string]string{}
	}
	return &fakeCredential{saved: saved}
}

// Key delegates to the real one so the fake agrees with production about which
// login identity an alias refers to.
func (*fakeCredential) Key(host config.SSHHost) string { return credential.Key(host) }

func (f *fakeCredential) Get(key string) (string, bool) {
	password, ok := f.saved[key]
	return password, ok
}

func (f *fakeCredential) Set(key, password string) error {
	f.setCalls++
	if f.setErr != nil {
		return f.setErr
	}
	f.saved[key] = password
	f.sets = append(f.sets, key)
	return nil
}

func (f *fakeCredential) Delete(key string) (bool, error) {
	if _, ok := f.saved[key]; !ok {
		return false, nil
	}
	delete(f.saved, key)
	return true, nil
}

func (f *fakeCredential) Clear() (int, error) {
	n := len(f.saved)
	f.saved = map[string]string{}
	return n, nil
}

func (f *fakeCredential) Keys() []string {
	keys := make([]string, 0, len(f.saved))
	for key := range f.saved {
		keys = append(keys, key)
	}
	return keys
}

func (f *fakeCredential) Path() string { return "memory" }

// newTestConnect builds the service through its constructor, so the wiring is
// exercised too, and returns the concrete type the tests poke at.
func newTestConnect(t *testing.T, transport repository.Transport, creds repository.Credential) *connectService {
	t.Helper()

	// An empty extra path reads the ssh config alone: the hosts a test lists have
	// to be the ones the test wrote, not the ones whoever is running it has added
	// to their own ~/.ohmyssh/hosts.
	return newTestConnectWith(t, transport, repository.NewHosts(""), creds)
}

// newTestConnectWith is newTestConnect with the host store chosen by the caller,
// for the tests that write hosts rather than only read them.
func newTestConnectWith(
	t *testing.T,
	transport repository.Transport,
	hosts repository.Hosts,
	creds repository.Credential,
) *connectService {
	t.Helper()

	logger := zerolog.Nop()
	connect, ok := NewConnectService(&logger, transport, hosts, creds).(*connectService)
	if !ok {
		t.Fatal("NewConnectService did not return *connectService")
	}
	return connect
}

// neverAsk fails the test if a password prompt happens at all.
func neverAsk(t *testing.T) func(string) (string, error) {
	t.Helper()
	return func(string) (string, error) {
		t.Fatal("the user was asked for a password when the policy says they should not be")
		return "", nil
	}
}

// A saved password that still works means no prompt: this is what makes the
// second connection to a host silent.
func TestSavedPasswordConnectsWithoutAsking(t *testing.T) {
	host := testHost()
	creds := newFakeCredential(map[string]string{credential.Key(host): savedPassword})
	transport := &fakeTransport{accept: savedPassword}
	connect := newTestConnect(t, transport, creds)
	connect.askPassword = neverAsk(t)

	client, err := connect.dialWithPrompt(context.Background(), host, ConnectOptions{}, sshclient.DialOptions{})
	if err != nil {
		t.Fatalf("dialWithPrompt: %v", err)
	}
	if client == nil {
		t.Fatal("dialWithPrompt returned no client")
	}
	if len(transport.attempts) != 1 || transport.attempts[0] != savedPassword {
		t.Errorf("dials = %v, want exactly [%s]", transport.attempts, savedPassword)
	}
	if len(creds.sets) != 0 {
		t.Errorf("re-saved a password the user never typed: %v", creds.sets)
	}
}

// A stale entry must not lock the user out: it is replaced by asking, and the
// replacement is what gets stored.
func TestStaleSavedPasswordIsReplacedByAsking(t *testing.T) {
	host := testHost()
	key := credential.Key(host)
	creds := newFakeCredential(map[string]string{key: "stale-pw"})
	transport := &fakeTransport{accept: typedPassword}
	connect := newTestConnect(t, transport, creds)
	connect.askPassword = func(label string) (string, error) {
		// The prompt names the machine, not the local alias.
		if want := "deploy@10.0.0.5"; label != want {
			t.Errorf("prompt label = %q, want %q", label, want)
		}
		return typedPassword, nil
	}

	client, err := connect.dialWithPrompt(context.Background(), host, ConnectOptions{}, sshclient.DialOptions{})
	if err != nil {
		t.Fatalf("dialWithPrompt: %v", err)
	}
	if client == nil {
		t.Fatal("dialWithPrompt returned no client")
	}
	want := []string{"stale-pw", typedPassword}
	if len(transport.attempts) != len(want) {
		t.Fatalf("dials = %v, want %v", transport.attempts, want)
	}
	for i := range want {
		if transport.attempts[i] != want[i] {
			t.Errorf("dial %d used %q, want %q", i, transport.attempts[i], want[i])
		}
	}
	if got := creds.saved[key]; got != typedPassword {
		t.Errorf("stored password = %q, want %q", got, typedPassword)
	}
}

// An explicit --password wins over anything on disk, and is worth saving so the
// next run does not need the flag.
func TestFlagPasswordWinsAndIsSaved(t *testing.T) {
	host := testHost()
	key := credential.Key(host)
	creds := newFakeCredential(map[string]string{key: savedPassword})
	transport := &fakeTransport{accept: flagPassword}
	connect := newTestConnect(t, transport, creds)
	connect.askPassword = neverAsk(t)

	if _, err := connect.dialWithPrompt(context.Background(), host, ConnectOptions{Password: flagPassword}, sshclient.DialOptions{Password: flagPassword}); err != nil {
		t.Fatalf("dialWithPrompt: %v", err)
	}
	if len(transport.attempts) != 1 || transport.attempts[0] != flagPassword {
		t.Errorf("dials = %v, want exactly [%s]", transport.attempts, flagPassword)
	}
	if got := creds.saved[key]; got != flagPassword {
		t.Errorf("stored password = %q, want the one from the flag", got)
	}
}

// --no-save-password has to hold even on the retry path, which is the one that
// actually writes.
func TestNoSavePasswordLeavesTheStoreAlone(t *testing.T) {
	host := testHost()
	key := credential.Key(host)
	creds := newFakeCredential(map[string]string{key: "stale-pw"})
	transport := &fakeTransport{accept: typedPassword}
	connect := newTestConnect(t, transport, creds)
	connect.askPassword = func(string) (string, error) { return typedPassword, nil }

	if _, err := connect.dialWithPrompt(context.Background(), host, ConnectOptions{NoSavePassword: true}, sshclient.DialOptions{}); err != nil {
		t.Fatalf("dialWithPrompt: %v", err)
	}
	if len(creds.sets) != 0 {
		t.Errorf("--no-save-password still wrote %v", creds.sets)
	}
	if got := creds.saved[key]; got != "stale-pw" {
		t.Errorf("stored password = %q, want the stale one left untouched", got)
	}
}

// --no-prompt makes a rejected password a plain failure, which is what scripts
// running exec depend on.
func TestNoPromptFailsInsteadOfAsking(t *testing.T) {
	host := testHost()
	creds := newFakeCredential(map[string]string{credential.Key(host): "stale-pw"})
	transport := &fakeTransport{accept: typedPassword}
	connect := newTestConnect(t, transport, creds)
	connect.askPassword = neverAsk(t)

	_, err := connect.dialWithPrompt(context.Background(), host, ConnectOptions{NoPrompt: true}, sshclient.DialOptions{})
	if err == nil {
		t.Fatal("--no-prompt returned no error after the password was rejected")
	}
	if !sshclient.IsAuthFailure(err) {
		t.Errorf("error = %v, want the authentication failure", err)
	}
	if len(transport.attempts) != 1 {
		t.Errorf("dials = %v, want one attempt and no retry", transport.attempts)
	}
}

// A connection that fails for any other reason (refused, timed out, unknown
// host key) is reported as-is. Prompting would ask for the wrong thing.
func TestNonAuthFailureIsNotRetried(t *testing.T) {
	host := testHost()
	refused := errors.New("dial tcp 10.0.0.5:2222: connect: connection refused")
	transport := &fakeTransport{dialErr: refused}
	connect := newTestConnect(t, transport, newFakeCredential(nil))
	connect.askPassword = neverAsk(t)

	_, err := connect.dialWithPrompt(context.Background(), host, ConnectOptions{}, sshclient.DialOptions{})
	if !errors.Is(err, refused) {
		t.Errorf("error = %v, want the dial failure", err)
	}
	if len(transport.attempts) != 1 {
		t.Errorf("dials = %v, want one attempt", transport.attempts)
	}
}

// With no terminal to prompt on (piped stdin), the dial failure is what gets
// reported. Surfacing the prompt error instead would make ohmyssh look broken
// when it is only waiting for input that can never arrive.
func TestUnavailablePromptReportsTheDialFailure(t *testing.T) {
	host := testHost()
	creds := newFakeCredential(map[string]string{credential.Key(host): "stale-pw"})
	transport := &fakeTransport{accept: typedPassword}
	connect := newTestConnect(t, transport, creds)
	connect.askPassword = func(string) (string, error) {
		return "", errors.New("stdin is not a terminal")
	}

	_, err := connect.dialWithPrompt(context.Background(), host, ConnectOptions{}, sshclient.DialOptions{})
	if !sshclient.IsAuthFailure(err) {
		t.Errorf("error = %v, want the authentication failure rather than the prompt failure", err)
	}
}

// Only a password that was shown to work is worth keeping. A second rejection
// fails the connection and leaves the store as it was.
func TestRejectedTypedPasswordIsNotSaved(t *testing.T) {
	host := testHost()
	creds := newFakeCredential(nil)
	transport := &fakeTransport{accept: "something-else"}
	connect := newTestConnect(t, transport, creds)
	connect.askPassword = func(string) (string, error) { return typedPassword, nil }

	_, err := connect.dialWithPrompt(context.Background(), host, ConnectOptions{}, sshclient.DialOptions{})
	if err == nil {
		t.Fatal("dialWithPrompt succeeded with a password the server rejects")
	}
	if !sshclient.IsAuthFailure(err) {
		t.Errorf("error = %v, want the authentication failure", err)
	}
	if len(creds.sets) != 0 {
		t.Errorf("saved a password that never worked: %v", creds.sets)
	}
	// With nothing saved, the first dial offers no password at all so that the
	// agent and the key files get their chance before the user is interrupted.
	want := []string{"", typedPassword}
	if len(transport.attempts) != len(want) || transport.attempts[0] != want[0] || transport.attempts[1] != want[1] {
		t.Errorf("dials = %v, want %v", transport.attempts, want)
	}
}

// passwordLabel is what the user reads while deciding which password to type.
func TestPasswordLabelNamesTheMachineNotTheAlias(t *testing.T) {
	if got, want := passwordLabel(testHost()), "deploy@10.0.0.5"; got != want {
		t.Errorf("passwordLabel = %q, want %q", got, want)
	}
	// A host reached without an alias still names the machine it will ask about.
	if got := passwordLabel(config.SSHHost{Hostname: "example.com"}); !strings.HasSuffix(got, "@example.com") {
		t.Errorf("passwordLabel = %q, want it to end with @example.com", got)
	}
}

// A password that cannot be written to the store is not a reason to refuse a
// connection that works: the user is connected, and the only cost is being asked
// again next time.
func TestAPasswordThatCannotBeSavedDoesNotFailTheConnection(t *testing.T) {
	host := testHost()
	creds := newFakeCredential(nil)
	creds.setErr = errors.New("the store is read-only")
	transport := &fakeTransport{accept: typedPassword}
	connect := newTestConnect(t, transport, creds)
	connect.askPassword = func(string) (string, error) { return typedPassword, nil }

	client, err := connect.dialWithPrompt(context.Background(), host, ConnectOptions{}, sshclient.DialOptions{})
	if err != nil {
		t.Fatalf("dialWithPrompt: %v", err)
	}
	if client == nil {
		t.Fatal("dialWithPrompt returned no client")
	}
	// The save was attempted and refused, rather than never attempted: a test
	// that only asserted the connection succeeded would pass either way.
	if creds.setCalls != 1 {
		t.Errorf("Set was called %d times, want 1", creds.setCalls)
	}
	if len(creds.sets) != 0 {
		t.Errorf("a password the store refused was recorded as saved: %v", creds.sets)
	}
}

// dialOptions is where the command line becomes a dial. Everything the flags say
// has to arrive, and so does the alias lookup a ProxyJump hop needs to name
// another host in the config.
func TestDialOptionsCarryEverySettingIntoTheDial(t *testing.T) {
	all := []config.SSHHost{
		{Name: "bastion", Hostname: "10.0.0.2"},
		{Name: "web1", Hostname: "10.0.0.5", Port: "2222"},
	}
	timeout := 3 * time.Second

	dial := dialOptions(ConnectOptions{
		Password:   flagPassword,
		KnownHosts: "/tmp/known_hosts",
		NoAgent:    true,
		Insecure:   true,
		Timeout:    timeout,
	}, all)

	if dial.Password != flagPassword {
		t.Errorf("Password = %q, want %q", dial.Password, flagPassword)
	}
	if dial.KnownHosts != "/tmp/known_hosts" {
		t.Errorf("KnownHosts = %q, want the flag's path", dial.KnownHosts)
	}
	if !dial.DisableAgent {
		t.Error("--no-agent did not reach the dial")
	}
	if !dial.InsecureIgnoreHostKey {
		t.Error("--insecure did not reach the dial")
	}
	if dial.Timeout != timeout {
		t.Errorf("Timeout = %v, want %v", dial.Timeout, timeout)
	}

	if dial.LookupHost == nil {
		t.Fatal("no alias lookup: a ProxyJump hop could not name another config host")
	}
	if host, ok := dial.LookupHost("web1"); !ok || host.Hostname != "10.0.0.5" || host.Port != "2222" {
		t.Errorf("LookupHost(web1) = %+v, %v, want the host behind the alias", host, ok)
	}
	if _, ok := dial.LookupHost("nowhere"); ok {
		t.Error("resolved an alias that is not in the config")
	}
}

// exec runs one command over a connection of its own, and the remote status
// becomes ours. It is forwarded with no message of our own: the command has
// already said whatever there was to say.
func TestExecForwardsTheRemoteExitStatus(t *testing.T) {
	host := testHost()
	transport := &fakeTransport{
		accept: savedPassword,
		runErr: &sshclient.ExitError{Code: 3},
	}
	creds := newFakeCredential(map[string]string{credential.Key(host): savedPassword})
	connect := newTestConnect(t, transport, creds)
	connect.askPassword = neverAsk(t)

	err := connect.Exec(
		context.Background(),
		ConnectOptions{ConfigPath: web1Config(t)},
		"web1",
		[]string{"systemctl", "is-active", "nginx"},
		io.Discard,
		io.Discard,
	)

	if got := errno.ExitCode(err); got != 3 {
		t.Errorf("exit code = %d, want 3 (err: %v)", got, err)
	}
	if got := errno.Message(err); got != "" {
		t.Errorf("message = %q, want none", got)
	}
}

// A command that worked is not an error, and nothing is said about it: exec's
// stdout is the command's own.
func TestExecSucceedsWhenTheCommandDoes(t *testing.T) {
	host := testHost()
	transport := &fakeTransport{accept: savedPassword}
	creds := newFakeCredential(map[string]string{credential.Key(host): savedPassword})
	connect := newTestConnect(t, transport, creds)
	connect.askPassword = neverAsk(t)

	err := connect.Exec(
		context.Background(),
		ConnectOptions{ConfigPath: web1Config(t)},
		"web1",
		[]string{"uptime"},
		io.Discard,
		io.Discard,
	)

	if err != nil {
		t.Errorf("Exec = %v, want nil", err)
	}
}

// A session that broke is not a remote exit status: reporting it through the
// remote status would exit with the same code a failing command does, and say
// nothing about why.
func TestExecReportsASessionFailureAsAFailure(t *testing.T) {
	host := testHost()
	broken := errors.New("ssh: connection lost")
	transport := &fakeTransport{accept: savedPassword, runErr: broken}
	creds := newFakeCredential(map[string]string{credential.Key(host): savedPassword})
	connect := newTestConnect(t, transport, creds)
	connect.askPassword = neverAsk(t)

	err := connect.Exec(
		context.Background(),
		ConnectOptions{ConfigPath: web1Config(t)},
		"web1",
		[]string{"uptime"},
		io.Discard,
		io.Discard,
	)

	if !errors.Is(err, broken) {
		t.Errorf("Exec = %v, want the session failure rather than a silent status", err)
	}
	if msg := errno.Message(err); !strings.Contains(msg, "connection lost") {
		t.Errorf("message = %q, want it to say what happened", msg)
	}
}
