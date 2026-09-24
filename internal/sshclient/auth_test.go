package sshclient

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/maogou/ohmyssh/internal/config"
)

// fakeAgent serves the ssh-agent protocol on a unix socket and counts the
// connections it has seen close. Whether the client released the socket is not
// otherwise observable: it holds no name, and an unclosed one is just an fd that
// outlives the connection it authenticated.
type fakeAgent struct {
	path   string
	pub    ssh.PublicKey
	closed atomic.Int64
}

// newFakeAgent starts an agent holding a single key, so the auth chain finds
// signers and takes ownership of the connection.
//
// The socket gets a directory of its own rather than living under t.TempDir():
// unix socket paths are capped at just over a hundred bytes, and a per-test path
// under the system temporary directory on macOS runs close enough to that limit
// to fail on the length of the test's name.
func newFakeAgent(t *testing.T) *fakeAgent {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate agent key: %v", err)
	}
	pub, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		t.Fatalf("agent public key: %v", err)
	}

	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: priv}); err != nil {
		t.Fatalf("add key to keyring: %v", err)
	}

	dir, err := os.MkdirTemp("", "ohmyssh-agent")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	path := filepath.Join(dir, "agent.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen on %s: %v", path, err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	fa := &fakeAgent{path: path, pub: pub}
	go fa.serve(listener, keyring)
	return fa
}

// serve answers agent requests until the listener is closed. ServeAgent returns
// once the client end of the socket goes away, so its return is the signal these
// tests wait on.
func (a *fakeAgent) serve(listener net.Listener, keyring agent.Agent) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			_ = agent.ServeAgent(keyring, conn)
			a.closed.Add(1)
		}()
	}
}

// waitForClose waits for want connections to close. The count only moves after
// the client closes the socket and the serve goroutine observes EOF, so a
// deadline is needed rather than an immediate read.
func (a *fakeAgent) waitForClose(t *testing.T, want int64) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for {
		if got := a.closed.Load(); got >= want {
			return
		} else if time.Now().After(deadline) {
			t.Fatalf("agent connections closed = %d, want %d", got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// agentHost describes a test server reached with the fake agent's key.
func agentHost(t *testing.T, addr string, a *fakeAgent) config.SSHHost {
	t.Helper()

	hostname, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	return config.SSHHost{
		Name:          hostname,
		Hostname:      hostname,
		Port:          port,
		IdentityAgent: a.path,
	}
}

// The agent connection has to outlive the handshake — a rekey needs the agent to
// sign again — and has to be gone once the connection it authenticated is
// closed. Authentication succeeding at all proves the agent was really used,
// since the server accepts only the key the fake agent holds.
func TestDialReleasesAgentConnectionOnClose(t *testing.T) {
	fa := newFakeAgent(t)
	server, _ := newTestServer(t, fa.pub)

	client, err := Dial(context.Background(), agentHost(t, server.Addr(), fa), DialOptions{
		KnownHosts: filepath.Join(t.TempDir(), "known_hosts"),
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if got := fa.closed.Load(); got != 0 {
		t.Fatalf("agent connections closed = %d before Close, want 0", got)
	}

	if err := client.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	fa.waitForClose(t, 1)
}

// A handshake that fails must still release the agent. The agent is dialled
// while the auth chain is built, before the server has been reached, so every
// rejected connection would otherwise leave a socket behind.
func TestDialReleasesAgentConnectionWhenHandshakeFails(t *testing.T) {
	fa := newFakeAgent(t)
	// The server accepts a key the agent does not hold, so authentication fails
	// after the agent has already handed over its signers.
	otherKey, _ := newTestClientKey(t)
	server, _ := newTestServer(t, otherKey)

	_, err := Dial(context.Background(), agentHost(t, server.Addr(), fa), DialOptions{
		KnownHosts: filepath.Join(t.TempDir(), "known_hosts"),
	})
	if err == nil {
		t.Fatal("dial succeeded with a key the server does not accept")
	}
	fa.waitForClose(t, 1)
}
