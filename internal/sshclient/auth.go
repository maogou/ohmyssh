package sshclient

import (
	"errors"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/maogou/ohmyssh/internal/config"
	"github.com/maogou/ohmyssh/internal/pkg/zlog"
)

// defaultKeyNames are tried in order when the host config offers no identity file.
var defaultKeyNames = []string{"id_ed25519", "id_ecdsa", "id_rsa", "id_dsa"}

// authChain is an ordered set of auth methods plus a human-readable label for
// each. The ssh package keeps its method types unexported, so labels are
// recorded as the chain is built rather than recovered by type assertion.
type authChain struct {
	methods []ssh.AuthMethod
	labels  []string
	cleanup func()
}

func (c *authChain) add(m ssh.AuthMethod, label string) {
	c.methods = append(c.methods, m)
	c.labels = append(c.labels, label)
}

// buildAuthChain assembles the authentication methods for host, most preferred
// first. The returned cleanup releases the ssh-agent connection, which must stay
// open until the handshake completes.
func buildAuthChain(host config.SSHHost, opts DialOptions) *authChain {
	chain := &authChain{cleanup: func() {}}
	tried := make(map[string]bool)

	if opts.DisableAgent {
		zlog.L().Debug().Msg("ssh-agent disabled by flag")
	} else if m, label, closer := agentAuthMethod(host.IdentityAgent); m != nil {
		chain.add(m, label)
		chain.cleanup = closer
	}

	// An explicit IdentityFile is preferred over the well-known default names.
	if host.Identity != "" {
		path := config.ExpandPath(host.Identity)
		tried[path] = true
		switch m, err := keyAuthMethod(path, opts.Password); {
		case err != nil:
			zlog.L().Warn().Err(err).Str("identity_file", path).Msg("skipping identity file")
		case m != nil:
			chain.add(m, "publickey:"+filepath.Base(path))
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return chain
	}
	for _, name := range defaultKeyNames {
		path := filepath.Join(home, ".ssh", name)
		if tried[path] {
			continue
		}
		if _, statErr := os.Stat(path); statErr != nil {
			continue
		}
		m, err := keyAuthMethod(path, opts.Password)
		if err != nil {
			zlog.L().Debug().Err(err).Str("key", name).Msg("skipping default key")
			continue
		}
		if m != nil {
			chain.add(m, "publickey:"+name)
		}
	}

	if opts.Password != "" {
		// Servers frequently expose only keyboard-interactive, so offer both.
		chain.add(ssh.Password(opts.Password), "password")
		chain.add(
			ssh.KeyboardInteractive(
				func(_, _ string, questions []string, _ []bool) ([]string, error) {
					answers := make([]string, len(questions))
					for i := range questions {
						answers[i] = opts.Password
					}
					return answers, nil
				},
			), "keyboard-interactive",
		)
	}

	return chain
}

// agentAuthMethod dials the agent socket and returns its signers. Signers are
// read once up front rather than via PublicKeysCallback, which would re-dial the
// agent on every auth attempt and can stall.
func agentAuthMethod(configured string) (ssh.AuthMethod, string, func()) {
	socket := configured
	switch socket {
	case "", "SSH_AUTH_SOCK":
		socket = os.Getenv("SSH_AUTH_SOCK")
	case "none":
		return nil, "", func() {}
	default:
		socket = config.ExpandPath(socket)
	}
	if socket == "" {
		return nil, "", func() {}
	}

	conn, err := net.Dial("unix", socket)
	if err != nil {
		zlog.L().Debug().Err(err).Str("socket", socket).Msg("ssh-agent unreachable")
		return nil, "", func() {}
	}

	signers, err := agent.NewClient(conn).Signers()
	if err != nil {
		_ = conn.Close()
		zlog.L().Debug().Err(err).Msg("ssh-agent signers unavailable")
		return nil, "", func() {}
	}
	if len(signers) == 0 {
		_ = conn.Close()
		zlog.L().Debug().Str("socket", socket).Msg("ssh-agent holds no keys")
		return nil, "", func() {}
	}

	zlog.L().Debug().Str("socket", socket).Int("keys", len(signers)).Msg("using ssh-agent")
	return ssh.PublicKeys(signers...), "publickey:agent", func() { _ = conn.Close() }
}

// keyAuthMethod loads one private key. An encrypted key is unlocked with
// passphrase when one is available; otherwise it is skipped so the remaining
// methods still get their chance.
func keyAuthMethod(path, passphrase string) (ssh.AuthMethod, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	signer, err := ssh.ParsePrivateKey(pem)
	if err == nil {
		return ssh.PublicKeys(signer), nil
	}

	if _, ok := errors.AsType[*ssh.PassphraseMissingError](err); !ok {
		return nil, err
	}
	if passphrase == "" {
		return nil, errors.New("key is passphrase-protected and no password was supplied")
	}
	signer, err = ssh.ParsePrivateKeyWithPassphrase(pem, []byte(passphrase))
	if err != nil {
		return nil, err
	}
	zlog.L().Debug().Str("key", filepath.Base(path)).Msg("unlocked passphrase-protected key")
	return ssh.PublicKeys(signer), nil
}
