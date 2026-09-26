package sshclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maogou/ohmyssh/internal/config"
	"github.com/maogou/ohmyssh/internal/pkg/zlog"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// testServer is a minimal in-process SSH server: enough of the protocol to
// exercise authentication, host keys, PTY requests, exit statuses and file
// transfers.
type testServer struct {
	listener net.Listener
	signer   ssh.Signer
	wg       sync.WaitGroup
	// fsRoot is the directory the sftp subsystem serves. Empty means the
	// subsystem is refused, which is how the fallback path is provoked.
	fsRoot string

	// globals are the connection-wide requests clients have sent, in order.
	// Nothing here implements any of them, but keeping the names is what lets a
	// test see a keepalive go out.
	globalMu sync.Mutex
	globals  []string

	// holdReplies makes the server accept global requests without ever
	// answering them, which is the shape of a far end that has gone quiet
	// without closing: the connection looks open and answers nothing. It is an
	// atomic because it is set by the test while the serve goroutine reads it.
	holdReplies atomic.Bool

	// stall holds the "stall" command open until the test lets go. A command that
	// is genuinely still running is the only state a cancellation can interrupt,
	// and it cannot be produced with a sleep: how long is long enough would be a
	// guess, and a wrong guess either races or slows the suite.
	stall chan struct{}

	// holdChannels makes the server leave new channels unanswered — neither
	// accepted nor rejected, so a client waiting on one waits forever. That is the
	// shape of a bastion that has stopped forwarding without closing, which is
	// what a hop dial has to be able to give up on.
	holdChannels atomic.Bool

	// banner is the text the server sends before it authenticates, if any. It is
	// read through a pointer to the atomic because a test sets it after the
	// server is already listening: the write is on the test's goroutine and the
	// read, when the banner goes out, is on the connection's.
	banner *atomic.Value
}

// setBanner makes the server send text as it starts authenticating, which is the
// one place a server says something before the login is decided.
func (s *testServer) setBanner(text string) { s.banner.Store(text) }

// newTestServer starts a server that accepts exactly the given public key.
// It returns the signer it uses as its host key so tests can pin it.
func newTestServer(t *testing.T, authorized ssh.PublicKey) (*testServer, ssh.Signer) {
	t.Helper()
	return newTestServerWithFS(t, authorized, "")
}

// newTestServerWithFS starts a server whose sftp subsystem serves root. The
// caller keeps root, usually a t.TempDir, so it can assert on the files directly;
// an empty root refuses the subsystem request outright.
//
// Only relative remote paths reach root: pkg/sftp joins the working directory
// onto a relative path and passes an absolute one through to the real
// filesystem, which is why every transfer test names its remote end without a
// leading slash.
func newTestServerWithFS(t *testing.T, authorized ssh.PublicKey, root string) (*testServer, ssh.Signer) {
	t.Helper()

	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatalf("host signer: %v", err)
	}

	// The server is built before the config that points at it, because the banner
	// is whatever the test has set by the time a client connects rather than
	// something the server is constructed with.
	banner := &atomic.Value{}

	serverCfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if authorized == nil || bytes.Equal(key.Marshal(), authorized.Marshal()) {
				return &ssh.Permissions{}, nil
			}
			return nil, fmt.Errorf("key not authorised")
		},
		BannerCallback: func(ssh.ConnMetadata) string {
			text, _ := banner.Load().(string)
			return text
		},
	}
	serverCfg.AddHostKey(hostSigner)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv := &testServer{listener: listener, signer: hostSigner, fsRoot: root, stall: make(chan struct{}), banner: banner}
	srv.wg.Add(1)
	go func() {
		defer srv.wg.Done()
		srv.serve(serverCfg)
	}()
	t.Cleanup(srv.Close)
	return srv, hostSigner
}

func (s *testServer) Addr() string { return s.listener.Addr().String() }

func (s *testServer) Close() {
	_ = s.listener.Close()
	// A command left stalled would keep its handler goroutine alive past the test
	// that started it. Closing the gate lets it return, which is also what ends
	// the session it belongs to.
	close(s.stall)
	s.wg.Wait()
}

func (s *testServer) serve(cfg *ssh.ServerConfig) {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return // listener closed by Close
		}
		go func() {
			defer conn.Close()
			sshConn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
			if err != nil {
				return
			}
			defer sshConn.Close()
			go s.handleGlobalRequests(reqs)
			for newChan := range chans {
				if s.holdChannels.Load() {
					// Answering neither way is the whole point: the client's request
					// stays outstanding, as it does at a far end that has stopped
					// forwarding without going away.
					continue
				}
				if newChan.ChannelType() != "session" {
					_ = newChan.Reject(ssh.UnknownChannelType, "only session channels are supported")
					continue
				}
				channel, requests, err := newChan.Accept()
				if err != nil {
					continue
				}
				go s.handleSession(channel, requests)
			}
		}()
	}
}

// handleGlobalRequests answers the connection-wide requests a client sends the
// way ssh.DiscardRequests would — a refusal, since this server implements none
// of them — and remembers their names on the way past.
func (s *testServer) handleGlobalRequests(reqs <-chan *ssh.Request) {
	for req := range reqs {
		s.globalMu.Lock()
		s.globals = append(s.globals, req.Type)
		s.globalMu.Unlock()
		if req.WantReply && !s.holdReplies.Load() {
			_ = req.Reply(false, nil)
		}
	}
}

// globalRequests returns a copy of the request names seen so far.
func (s *testServer) globalRequests() []string {
	s.globalMu.Lock()
	defer s.globalMu.Unlock()

	return slices.Clone(s.globals)
}

// handleSession serves one session channel. The "exec" payload is interpreted
// by runTestCommand so tests can assert on output and exit status, and the
// "subsystem" request runs a real SFTP server over the channel so transfers are
// exercised against the protocol rather than against a mock.
func (s *testServer) handleSession(channel ssh.Channel, requests <-chan *ssh.Request) {
	defer channel.Close()

	for req := range requests {
		switch req.Type {
		case "pty-req", "env", "window-change", "shell":
			if req.Type == "shell" {
				_, _ = io.WriteString(channel, "shell-started\n")
				_ = req.Reply(true, nil)
				sendExitStatus(channel, 0)
				return
			}
			_ = req.Reply(true, nil)

		case "subsystem":
			if s.fsRoot == "" || sshString(req.Payload) != "sftp" {
				_ = req.Reply(false, nil)
				continue
			}
			_ = req.Reply(true, nil)
			s.serveSFTP(channel)
			return

		case "exec":
			_ = req.Reply(true, nil)
			code := s.runTestCommand(sshString(req.Payload), channel)
			sendExitStatus(channel, code)
			return

		default:
			_ = req.Reply(false, nil)
		}
	}
}

// serveSFTP runs an SFTP server on the channel until the client hangs up. It
// serves fsRoot as its working directory, so a relative path in a test lands
// under the temporary directory the test owns.
func (s *testServer) serveSFTP(channel ssh.Channel) {
	server, err := sftp.NewServer(channel, sftp.WithServerWorkingDirectory(s.fsRoot))
	if err != nil {
		return
	}
	// Serve returns io.EOF when the client closes the channel cleanly, which is
	// how a finished transfer ends rather than a failure.
	if err := server.Serve(); err != nil && !errors.Is(err, io.EOF) {
		return
	}
}

// sshString reads the single SSH string an SSH request payload holds: a 4-byte
// big-endian length followed by that many bytes. Close and exec requests are
// both shaped that way.
func sshString(payload []byte) string {
	if len(payload) < 4 {
		return ""
	}
	n := binary.BigEndian.Uint32(payload[:4])
	if int(n) > len(payload)-4 {
		return ""
	}
	return string(payload[4 : 4+n])
}

// sendExitStatus reports a command's exit code on the channel.
func sendExitStatus(channel ssh.Channel, code int) {
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, uint32(code))
	_, _ = channel.SendRequest("exit-status", false, payload)
}

// runTestCommand is the fake remote shell: it understands a couple of commands
// so tests can verify output streaming and exit status propagation. It is a
// method so "stall" can wait on the test that started it.
func (s *testServer) runTestCommand(command string, out io.Writer) int {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return 0
	}
	switch fields[0] {
	case "echo":
		_, _ = fmt.Fprintln(out, strings.Join(fields[1:], " "))
		return 0
	case "fail":
		_, _ = io.WriteString(out, "boom\n")
		return 3
	case "stall":
		// Runs until the test lets go, so a cancellation arrives at a command that
		// is still running rather than at one that has already finished.
		<-s.stall
		return 0
	default:
		_, _ = fmt.Fprintf(out, "unknown: %s\n", command)
		return 127
	}
}

// newTestClientKey generates an ed25519 key pair and writes the private half to
// a temporary file in PKCS#8 PEM form, which ssh.ParsePrivateKey understands.
func newTestClientKey(t *testing.T) (ssh.PublicKey, string) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	path := filepath.Join(t.TempDir(), "id_ed25519")
	block := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, block, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("ssh public key: %v", err)
	}
	return sshPub, path
}

// dialTestHost dials a test server with the given key, using an isolated
// known_hosts file.
func dialTestHost(t *testing.T, addr, keyPath, knownHosts string) (*Client, error) {
	t.Helper()

	hostname, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	host := config.SSHHost{Name: hostname, Hostname: hostname, Port: port, Identity: keyPath}

	return Dial(context.Background(), host, DialOptions{
		DisableAgent: true,
		KnownHosts:   knownHosts,
	})
}

// wantMode asserts the mode of a file this package writes. Mode bits are a Unix
// idea: Windows applies a mode as the read-only attribute and nothing else, so
// every file there reads back 0666 and every directory 0777 whatever was asked
// for. The mode is therefore asserted where the platform has one to report.
func wantMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if perm := info.Mode().Perm(); perm != want {
		t.Errorf("%s mode = %o, want %o", path, perm, want)
	}
}

// A host with nothing to authenticate with is reported as such. That error path
// releases the ssh-agent connection, and it is easy to arm that release with a
// nil — a return statement assigns to the named result before a deferred call
// reads it — which turns a clear message into a panic.
func TestDialReportsAHostWithNoAuthenticationMethod(t *testing.T) {
	// An empty home directory is what leaves the chain empty: no identity file
	// on the host, no default key to find under ~/.ssh, and no agent to ask. It
	// also keeps the test from depending on the keys the machine happens to hold.
	// Both names are set because os.UserHomeDir reads HOME everywhere but
	// Windows, where it reads USERPROFILE — set only on Unix, the real profile
	// would answer there and its keys would fill the chain this test empties.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	host := config.SSHHost{Name: "lonely", Hostname: "lonely.example.com"}
	_, err := Dial(context.Background(), host, DialOptions{DisableAgent: true})
	if err == nil {
		t.Fatal("Dial accepted a host with no authentication method")
	}
	if !strings.Contains(err.Error(), "no authentication methods available") {
		t.Errorf("err = %v, want it to name the missing authentication", err)
	}
}

func TestDialAndRunCommand(t *testing.T) {
	pub, keyPath := newTestClientKey(t)
	server, _ := newTestServer(t, pub)
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")

	client, err := dialTestHost(t, server.Addr(), keyPath, knownHosts)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	var stdout, stderr bytes.Buffer
	if err := RunCommand(context.Background(), client.Client, []string{"echo", "hello", "world"}, &stdout, &stderr); err != nil {
		t.Fatalf("run command: %v", err)
	}
	if got, want := stdout.String(), "hello world\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

// silentListener is a TCP listener that accepts and then says nothing, which is
// the shape of a server that is up enough for the connection to succeed and
// broken enough to never answer the version exchange. It is the one failure a
// TCP-only timeout cannot bound, and the one a cancelled context has to reach.
func silentListener(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var (
		mu   sync.Mutex
		held []net.Conn
	)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return // listener closed by the cleanup below
			}
			mu.Lock()
			held = append(held, conn)
			mu.Unlock()
		}
	}()
	t.Cleanup(
		func() {
			_ = listener.Close()
			mu.Lock()
			defer mu.Unlock()
			for _, conn := range held {
				_ = conn.Close()
			}
		},
	)
	return listener.Addr().String()
}

// dialSilentHost dials a listener that never handshakes, so the timeout tests
// exercise the same path a real dial takes.
func dialSilentHost(t *testing.T, ctx context.Context, addr string, timeout time.Duration) error {
	t.Helper()

	_, keyPath := newTestClientKey(t)
	hostname, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	host := config.SSHHost{Name: hostname, Hostname: hostname, Port: port, Identity: keyPath}

	_, err = Dial(ctx, host, DialOptions{
		DisableAgent: true,
		KnownHosts:   filepath.Join(t.TempDir(), "known_hosts"),
		Timeout:      timeout,
	})
	return err
}

// A server that accepts TCP but never answers the version exchange must be cut
// off by the dial timeout rather than hang in the handshake. The ssh package
// applies ClientConfig.Timeout to the TCP dial alone, so the deadline that
// covers the exchange is this package's to set.
func TestDialTimeoutBoundsTheHandshake(t *testing.T) {
	addr := silentListener(t)

	start := time.Now()
	err := dialSilentHost(t, context.Background(), addr, 250*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("dial succeeded against a server that never handshakes")
	}
	if !strings.Contains(err.Error(), "handshake") {
		t.Errorf("error does not name the handshake: %v", err)
	}
	// A generous margin for a loaded CI box: the point is that the dial came
	// back near the timeout, not that the clock is precise.
	if elapsed > 5*time.Second {
		t.Errorf("dial returned after %v, want it bounded by the 250ms timeout", elapsed)
	}
}

// Cancelling the context must end a dial whose handshake is still out, whatever
// the timeout says. Without this, ctrl+c during a connect is swallowed and the
// only way out of the hang is to kill the process.
func TestDialContextCancelEndsTheHandshake(t *testing.T) {
	addr := silentListener(t)

	// A timeout long enough that only the cancellation can end the attempt.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := dialSilentHost(t, ctx, addr, 30*time.Second)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("dial succeeded despite a cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error does not carry the cancellation: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("cancellation took %v to end the dial, want it at once", elapsed)
	}
}

// The timeout has to reach a ProxyCommand transport too. That transport is a
// pair of pipes, and a pipe has no deadlines: SetDeadline on one is accepted and
// ignored. A bound built on a deadline therefore does nothing here, and a proxy
// that is running but never speaks leaves the dial hanging for as long as its
// process lives.
func TestDialTimeoutBoundsAProxyCommandHandshake(t *testing.T) {
	_, keyPath := newTestClientKey(t)
	host := config.SSHHost{Name: "proxy", Hostname: "proxy", Port: "22", Identity: keyPath}

	start := time.Now()
	_, err := Dial(context.Background(), host, DialOptions{
		DisableAgent: true,
		KnownHosts:   filepath.Join(t.TempDir(), "known_hosts"),
		// A proxy that holds its pipes open and says nothing: connected, then
		// quiet. The sleep outlasts the test, so only the timeout can end this.
		ProxyCommand: "sleep 10",
		Timeout:      250 * time.Millisecond,
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("dial succeeded through a proxy that never handshakes")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error does not name the timeout: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("dial returned after %v, want it bounded by the 250ms timeout", elapsed)
	}
}

// captureLogs points the process-wide logger at a buffer for the duration of a
// test, so that what is written beside a log line — a banner, a ProxyCommand's
// stderr — can be read rather than looked for on a terminal. The logger is put
// back the way it was found, since it is one object shared by the whole process
// and no test in this package watches it go by.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()

	out := &bytes.Buffer{}
	zlog.Setup(zlog.Options{Level: "info", Stderr: out})
	t.Cleanup(func() { zlog.Setup(zlog.Options{Level: "info"}) })
	return out
}

// A ProxyCommand's own stderr is text a user would otherwise read on the
// terminal, and the view that owns the terminal is just as likely to have
// started the command as a command line is: it goes where the logs go. Printed
// straight to the terminal it would land in the middle of a frame, leaving one
// row of the view above it per line — the corruption the file view showed.
func TestProxyCommandStderrFollowsTheLogs(t *testing.T) {
	logs := captureLogs(t)

	conn, err := proxyCommandConn(context.Background(), "echo proxy noise >&2")
	if err != nil {
		t.Fatalf("start proxy command: %v", err)
	}
	// Close reaps the command, and waiting for it is what says its stderr has
	// been copied out before the buffer is read.
	if err := conn.Close(); err != nil {
		t.Fatalf("close proxy command: %v", err)
	}

	if !strings.Contains(logs.String(), "proxy noise") {
		t.Errorf("the proxy command's stderr did not go where the logs go: %q", logs.String())
	}
}

// A server's login banner is not a log line either, and it arrives during the
// dial an open file view makes: several lines of the server's own text, printed
// into a frame the view is drawing.
func TestServerBannerFollowsTheLogs(t *testing.T) {
	logs := captureLogs(t)

	pub, keyPath := newTestClientKey(t)
	server, _ := newTestServer(t, pub)
	server.setBanner("Authorized access only\n")

	client, err := dialTestHost(t, server.Addr(), keyPath, filepath.Join(t.TempDir(), "known_hosts"))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	if !strings.Contains(logs.String(), "Authorized access only") {
		t.Errorf("the server's banner did not go where the logs go: %q", logs.String())
	}
}

// A hop dial has to be bounded the way every other phase of connecting is.
// ssh.Client.Dial has neither a context nor a deadline: it asks the bastion to
// open the onward channel and waits for the answer, so a bastion that stops
// answering — a firewall dropping it, a host out of file descriptors — holds the
// whole connect for as long as the socket lives, and the ctrl+c meant to stop it
// only ever reached the first hop.
func TestHopDialIsBoundedByTheContext(t *testing.T) {
	bastion := stalledBastion(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := hopDialResult(t, ctx, bastion, DefaultDialTimeout)
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("error does not carry the cancellation: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("cancellation took %v to end the hop dial, want it at once", elapsed)
	}
}

// The timeout has to bound a hop dial as well, because the context is not always
// the thing that ends it: an interactive session connects under a context that
// lives as long as the user is at the terminal and carries no deadline at all.
func TestHopDialTimeoutBoundsABastionThatNeverAnswers(t *testing.T) {
	bastion := stalledBastion(t)

	start := time.Now()
	err := hopDialResult(t, context.Background(), bastion, 250*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("hop dial succeeded against a bastion that never answers")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error does not name the timeout: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("hop dial returned after %v, want it bounded by the 250ms timeout", elapsed)
	}
}

// The bound has to reach the ProxyJump path itself and not only the helper it
// uses: this is the arrangement a user runs into, where the bastion accepts the
// connection and goes quiet when asked to forward.
func TestDialThroughABastionThatNeverAnswersTheHopIsBounded(t *testing.T) {
	pub, keyPath := newTestClientKey(t)
	bastion, _ := newTestServer(t, pub)
	bastion.holdChannels.Store(true)
	bastionHost, bastionPort, err := net.SplitHostPort(bastion.Addr())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}

	host := config.SSHHost{
		Name: "web1", Hostname: "10.0.0.1", Port: "22",
		Identity: keyPath, ProxyJump: "bastion",
	}

	client, elapsed, err := dialWithWatchdog(t, host, DialOptions{
		DisableAgent: true,
		KnownHosts:   filepath.Join(t.TempDir(), "known_hosts"),
		Timeout:      250 * time.Millisecond,
		LookupHost: func(alias string) (config.SSHHost, bool) {
			if alias != "bastion" {
				return config.SSHHost{}, false
			}
			return config.SSHHost{
				Name: "bastion", Hostname: bastionHost, Port: bastionPort, Identity: keyPath,
			}, true
		},
	})
	if client != nil {
		_ = client.Close()
	}

	if err == nil {
		t.Fatal("dial succeeded through a bastion that never answers the hop")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error does not name the timeout: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("dial returned after %v, want it bounded by the 250ms timeout", elapsed)
	}
}

// dialWithWatchdog runs Dial beside a test that has to be able to report a dial
// which never comes back. Left to block, the test would be reported as the whole
// package timing out rather than as the unbounded hop dial it is.
func dialWithWatchdog(t *testing.T, host config.SSHHost, opts DialOptions) (*Client, time.Duration, error) {
	t.Helper()

	type result struct {
		client *Client
		err    error
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		client, err := Dial(context.Background(), host, opts)
		done <- result{client, err}
	}()

	select {
	case res := <-done:
		return res.client, time.Since(start), res.err
	case <-time.After(5 * time.Second):
		t.Fatal("dial never returned: a hop dial through a bastion that does not answer is unbounded")
		return nil, 0, nil
	}
}

// stalledBastion is a connection whose far end accepts requests for onward
// channels and never answers them: a bastion that stopped forwarding without
// closing, which is the state a hop dial has to be able to give up on.
func stalledBastion(t *testing.T) *Client {
	t.Helper()

	pub, keyPath := newTestClientKey(t)
	server, _ := newTestServer(t, pub)
	server.holdChannels.Store(true)

	client, err := dialTestHost(t, server.Addr(), keyPath, filepath.Join(t.TempDir(), "known_hosts"))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	// hopDial abandons the connection it is given, so this is only for a run that
	// never reaches it.
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// hopDialResult runs one hop dial and fails the test rather than hanging if it
// does not come back: the failure being tested for is a call that never returns,
// and a test blocked on it could not report that.
func hopDialResult(t *testing.T, ctx context.Context, bastion *Client, timeout time.Duration) error {
	t.Helper()

	result := make(chan error, 1)
	go func() {
		_, err := hopDial(ctx, bastion.Client, "10.0.0.1:22", timeout)
		result <- err
	}()

	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("the hop dial never returned: a bastion that does not answer is not bounded")
		return nil
	}
}

// The timeout bounds the handshake, not the connection: one that was established
// in time stays usable for as long as the user needs it, however long ago the
// clock that bounded the handshake ran out.
func TestConnectionOutlivesItsHandshakeTimeout(t *testing.T) {
	pub, keyPath := newTestClientKey(t)
	server, _ := newTestServer(t, pub)

	hostname, port, err := net.SplitHostPort(server.Addr())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	host := config.SSHHost{Name: hostname, Hostname: hostname, Port: port, Identity: keyPath}

	client, err := Dial(context.Background(), host, DialOptions{
		DisableAgent: true,
		KnownHosts:   filepath.Join(t.TempDir(), "known_hosts"),
		Timeout:      250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Well past the point the handshake bound expired. A deadline left behind on
	// the connection would have taken it down by now.
	time.Sleep(750 * time.Millisecond)

	var stdout, stderr bytes.Buffer
	if err := RunCommand(context.Background(), client.Client, []string{"echo", "still", "here"}, &stdout, &stderr); err != nil {
		t.Fatalf("run command after the handshake timeout elapsed: %v", err)
	}
	if got, want := stdout.String(), "still here\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

// A connection that is idle is kept alive on its own. The reason is not the
// connection that fails loudly — that reports itself — but the one a NAT or
// firewall drops for looking unused, which answers nothing and leaves the user's
// next command hanging until they give up.
func TestKeepAlivePingsAnIdleConnection(t *testing.T) {
	pub, keyPath := newTestClientKey(t)
	server, _ := newTestServer(t, pub)
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")

	client, err := dialTestHost(t, server.Addr(), keyPath, knownHosts)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	// Production runs at keepAliveInterval, half a minute; the loop takes its
	// interval as an argument so a test can watch several of them go by. A
	// reply is given twice the interval, so this runs well above the scale
	// where a loaded CI box could exceed the bound and have the connection
	// declared gone mid-test: at 50ms the bound is a comfortable 100ms.
	go client.keepAlive(50 * time.Millisecond)

	// Two of them, because one is easy to get by accident and the point is that
	// the connection is pinged for as long as it is idle.
	//
	// The name is spelled out rather than compared with the constant it is sent
	// under: it is part of the protocol, and it is the one OpenSSH answers. A
	// request under any other name would be refused by every server, which is a
	// round trip that keeps nothing alive — and the count below would still
	// notice it going out.
	keepalives := func() int {
		n := 0
		for _, name := range server.globalRequests() {
			if name == "keepalive@openssh.com" {
				n++
			}
		}
		return n
	}
	deadline := time.Now().Add(5 * time.Second)
	for keepalives() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := keepalives(); got < 2 {
		t.Fatalf("the server saw %d keepalives, want at least 2: %v", got, server.globalRequests())
	}
}

// A connection that stops answering must be taken down by the keepalive loop,
// and whatever is blocked on the connection must fail because of it. The far
// end this stands for — a NAT or firewall that dropped the mapping — sends
// neither a reply nor a reset, so nothing on the connection would ever return
// on its own: the bound the loop puts on the round trip is the only thing that
// can end the wait.
func TestKeepAliveTakesDownAnUnansweringConnection(t *testing.T) {
	pub, keyPath := newTestClientKey(t)
	server, _ := newTestServer(t, pub)
	server.holdReplies.Store(true)
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")

	client, err := dialTestHost(t, server.Addr(), keyPath, knownHosts)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	// The loop runs above the scale where a loaded box could miss the bound
	// with the connection perfectly alive; see TestKeepAlivePingsAnIdleConnection.
	go client.keepAlive(25 * time.Millisecond)

	// A request that wants a reply is the shape of everything a user runs on
	// the connection. This one can only end two ways: the server answers —
	// which holdReplies forbids — or the keepalive loop gives up on the round
	// trip and closes the connection out from under it.
	waiting := make(chan error, 1)
	go func() {
		_, _, err := client.SendRequest("keepalive-probe", true, nil)
		waiting <- err
	}()

	select {
	case err := <-waiting:
		if err == nil {
			t.Fatal("a request was answered on a connection whose replies are held")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a request blocked on the connection is still waiting: the keepalive did not close it")
	}
}

func TestRunCommandPropagatesExitStatus(t *testing.T) {
	pub, keyPath := newTestClientKey(t)
	server, _ := newTestServer(t, pub)
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")

	client, err := dialTestHost(t, server.Addr(), keyPath, knownHosts)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	var stdout, stderr bytes.Buffer
	err = RunCommand(context.Background(), client.Client, []string{"fail"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected an error for a non-zero remote status")
	}
	if got := ExitCode(err); got != 3 {
		t.Errorf("ExitCode = %d, want 3 (err: %v)", got, err)
	}

	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error is %T, want *ExitError", err)
	}
	if exitErr.Code != 3 {
		t.Errorf("ExitError.Code = %d, want 3", exitErr.Code)
	}
}

// A command still running when the context is cancelled is reported as an
// interruption, which is the whole reason the context is checked at all: the
// session's own teardown error would say the connection failed rather than that
// the user asked it to stop.
func TestRunCommandReportsACancellationThatStoppedTheCommand(t *testing.T) {
	pub, keyPath := newTestClientKey(t)
	server, _ := newTestServer(t, pub)
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")

	client, err := dialTestHost(t, server.Addr(), keyPath, knownHosts)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	// "stall" runs until the server is let go, so the command is still running
	// when this fires and the cancellation is what ends it.
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()

	var stdout, stderr bytes.Buffer
	if err := RunCommand(ctx, client.Client, []string{"stall"}, &stdout, &stderr); !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

// A command that finishes is a success even when the context is cancelled around
// it. Cancelling closes the session, so the two events race by nature; the one
// that must not happen is a finished command — one that printed its last line as
// ctrl+c arrived — being reported as an interruption, because the exit status is
// something a script acts on.
//
// The context here cancels nothing: Err reports a cancellation, Done never fires,
// so no goroutine closes the session and the command is free to finish. That is
// the race's losing half made deterministic — the old code returned ctx.Err()
// before it ever looked at how the command ended.
func TestRunCommandReportsSuccessWhenTheContextIsCancelledAfterIt(t *testing.T) {
	pub, keyPath := newTestClientKey(t)
	server, _ := newTestServer(t, pub)
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")

	client, err := dialTestHost(t, server.Addr(), keyPath, knownHosts)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	var stdout, stderr bytes.Buffer
	err = RunCommand(settledContext{context.Background()}, client.Client, []string{"echo", "done"}, &stdout, &stderr)
	if err != nil {
		t.Errorf("a finished command was reported as %v, want no error", err)
	}
	if got, want := stdout.String(), "done\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

// settledContext is a context whose cancellation has already happened and cannot
// be observed: Err answers, Done never fires. Nothing an ssh session does is
// driven by it, which is the state RunCommand is asked about when it has to
// decide whether a command that ended was interrupted.
type settledContext struct{ context.Context }

func (settledContext) Err() error { return context.Canceled }

// The first connection to an unknown host must record its key, so a later
// connection can verify it.
func TestTrustOnFirstUseRecordsHostKey(t *testing.T) {
	pub, keyPath := newTestClientKey(t)
	server, hostSigner := newTestServer(t, pub)
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")

	client, err := dialTestHost(t, server.Addr(), keyPath, knownHosts)
	if err != nil {
		t.Fatalf("first dial: %v", err)
	}
	_ = client.Close()

	contents, err := os.ReadFile(knownHosts)
	if err != nil {
		t.Fatalf("known_hosts was not created: %v", err)
	}
	if !strings.Contains(string(contents), hostSigner.PublicKey().Type()) {
		t.Errorf("known_hosts does not record the host key type:\n%s", contents)
	}

	// The second connection must succeed against the now-trusted key.
	client2, err := dialTestHost(t, server.Addr(), keyPath, knownHosts)
	if err != nil {
		t.Fatalf("second dial against a recorded key: %v", err)
	}
	_ = client2.Close()

	// Permissions must stay private.
	wantMode(t, knownHosts, 0o600)
}

// A host whose key changed must be rejected rather than silently trusted.
func TestChangedHostKeyIsRejected(t *testing.T) {
	pub, keyPath := newTestClientKey(t)
	server, _ := newTestServer(t, pub)
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")

	client, err := dialTestHost(t, server.Addr(), keyPath, knownHosts)
	if err != nil {
		t.Fatalf("first dial: %v", err)
	}
	_ = client.Close()

	// Replace the recorded key with an unrelated one for the same address.
	_, otherPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherPub, err := ssh.NewPublicKey(otherPriv.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	line := knownhostsLine(server.Addr(), otherPub)
	if err := os.WriteFile(knownHosts, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := dialTestHost(t, server.Addr(), keyPath, knownHosts); err == nil {
		t.Fatal("dial succeeded despite a host key mismatch")
	} else if !strings.Contains(err.Error(), "host key") && !strings.Contains(err.Error(), "knownhosts") {
		t.Errorf("error does not mention the host key: %v", err)
	}
}

func TestUnauthorisedKeyIsRejected(t *testing.T) {
	authorizedPub, _ := newTestClientKey(t)
	_, otherKeyPath := newTestClientKey(t)
	server, _ := newTestServer(t, authorizedPub)
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")

	_, err := dialTestHost(t, server.Addr(), otherKeyPath, knownHosts)
	if err == nil {
		t.Fatal("dial succeeded with an unauthorised key")
	}
	if !IsAuthFailure(err) {
		t.Errorf("IsAuthFailure(%v) = false, want true", err)
	}
}

func TestInsecureIgnoreHostKeySkipsVerification(t *testing.T) {
	pub, keyPath := newTestClientKey(t)
	server, _ := newTestServer(t, pub)

	hostname, port, _ := net.SplitHostPort(server.Addr())
	host := config.SSHHost{Name: hostname, Hostname: hostname, Port: port, Identity: keyPath}

	client, err := Dial(context.Background(), host, DialOptions{
		DisableAgent:          true,
		InsecureIgnoreHostKey: true,
		KnownHosts:            filepath.Join(t.TempDir(), "known_hosts"),
	})
	if err != nil {
		t.Fatalf("dial with InsecureIgnoreHostKey: %v", err)
	}
	_ = client.Close()
}

// A host with no usable credentials must fail before any network activity.
func TestDialWithoutCredentialsReportsClearly(t *testing.T) {
	host := config.SSHHost{Name: "nowhere", Hostname: "127.0.0.1", Port: "1"}
	_, err := Dial(context.Background(), host, DialOptions{
		DisableAgent: true,
		KnownHosts:   filepath.Join(t.TempDir(), "known_hosts"),
	})
	if err == nil {
		t.Fatal("expected an error when no credentials exist")
	}
	// The default keys in the user's ~/.ssh may exist, in which case the dial
	// proceeds and fails at the network layer instead. Either way it must fail.
	if !strings.Contains(err.Error(), "no authentication methods") &&
		!strings.Contains(err.Error(), "dial") &&
		!strings.Contains(err.Error(), "handshake") {
		t.Errorf("unexpected error: %v", err)
	}
}

// InteractiveSession drives a command over a session channel. Without a local
// terminal it runs as a pipe, which is the path exercised here.
func TestInteractiveSessionRunsCommandAsPipe(t *testing.T) {
	pub, keyPath := newTestClientKey(t)
	server, _ := newTestServer(t, pub)
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")

	client, err := dialTestHost(t, server.Addr(), keyPath, knownHosts)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	var stdout, stderr bytes.Buffer
	session := NewInteractiveSession(client.Client, []string{"echo", "from-session"})
	session.SetStdin(strings.NewReader(""))
	session.SetStdout(&stdout)
	session.SetStderr(&stderr)

	if err := session.Run(); err != nil {
		t.Fatalf("session run: %v", err)
	}
	if got, want := stdout.String(), "from-session\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestInteractiveSessionPropagatesExitStatus(t *testing.T) {
	pub, keyPath := newTestClientKey(t)
	server, _ := newTestServer(t, pub)
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")

	client, err := dialTestHost(t, server.Addr(), keyPath, knownHosts)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	var stdout, stderr bytes.Buffer
	session := NewInteractiveSession(client.Client, []string{"fail"})
	session.SetStdin(strings.NewReader(""))
	session.SetStdout(&stdout)
	session.SetStderr(&stderr)

	err = session.Run()
	if err == nil {
		t.Fatal("expected an error for a non-zero remote status")
	}
	if got := ExitCode(err); got != 3 {
		t.Errorf("ExitCode = %d, want 3", got)
	}
}

// A finished session must not leave a goroutine reading local stdin. The stdin
// pump that x/crypto/ssh starts stays blocked in a read on the terminal after
// the session ends, and that stray read competes with whoever owns the terminal
// next — in the host browser it swallowed the first key pressed after a session
// returned, because the pump consumed the keystroke and failed to forward it.
//
// Stopping that read is something cancelreader can only do on Windows when the
// input is the console itself: it opens CONIN$ to cancel the read, and anything
// else — a pipe, which is what stdin is whenever it has been redirected, and
// what this test uses to stand in for a terminal — falls back to a reader whose
// Cancel reports it cancelled nothing and whose Close does nothing. The pump
// stays parked in its read until the next input satisfies it, and that input is
// lost. Nothing in this package can take it back, so the platform is where the
// difference lies rather than in the code under test.
func TestInteractiveSessionStopsReadingStdin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a non-console reader cannot be interrupted on Windows")
	}

	pub, keyPath := newTestClientKey(t)
	server, _ := newTestServer(t, pub)
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")

	client, err := dialTestHost(t, server.Addr(), keyPath, knownHosts)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	// A pipe stands in for the terminal: an *os.File, so on Unix it is
	// cancellable the same way the real stdin is.
	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer stdinRead.Close()
	defer stdinWrite.Close()

	var stdout, stderr bytes.Buffer
	session := NewInteractiveSession(client.Client, []string{"echo", "done"})
	session.SetStdin(stdinRead)
	session.SetStdout(&stdout)
	session.SetStderr(&stderr)

	if err := session.Run(); err != nil {
		t.Fatalf("session run: %v", err)
	}

	// Anything written now belongs to the next owner of stdin.
	if _, err := stdinWrite.Write([]byte("keystroke")); err != nil {
		t.Fatalf("write to stdin: %v", err)
	}
	// Give a surviving stray reader every chance to consume it first.
	time.Sleep(250 * time.Millisecond)

	type readResult struct {
		data string
		err  error
	}
	results := make(chan readResult, 1)
	go func() {
		buf := make([]byte, len("keystroke"))
		n, err := io.ReadFull(stdinRead, buf)
		results <- readResult{string(buf[:n]), err}
	}()

	select {
	case got := <-results:
		if got.err != nil {
			t.Fatalf("reading stdin after the session: %v", got.err)
		}
		if got.data != "keystroke" {
			t.Errorf("read %q from stdin, want %q", got.data, "keystroke")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stdin was left unreadable after the session: a stray reader is still consuming it")
	}
}

func TestIsAuthFailureClassification(t *testing.T) {
	if IsAuthFailure(nil) {
		t.Error("IsAuthFailure(nil) = true, want false")
	}
	if !IsAuthFailure(fmt.Errorf("ssh: handshake failed: unable to authenticate, attempted methods [none], no supported methods remain")) {
		t.Error("a handshake authentication error was not detected")
	}
	if IsAuthFailure(&ExitError{Code: 1}) {
		t.Error("an ExitError was misclassified as an auth failure")
	}
}

func TestExitCodeDefaults(t *testing.T) {
	if got := ExitCode(nil); got != 0 {
		t.Errorf("ExitCode(nil) = %d, want 0", got)
	}
	if got := ExitCode(errors.New("boom")); got != 1 {
		t.Errorf("ExitCode(generic) = %d, want 1", got)
	}
}

// knownhostsLine formats an address and key the way known_hosts expects.
func knownhostsLine(addr string, key ssh.PublicKey) string {
	return fmt.Sprintf("%s %s", addr, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))))
}
