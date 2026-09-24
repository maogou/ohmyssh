package sshclient

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/maogou/ohmyssh/internal/pkg/zlog"
)

// knownHostsMu serialises read-verify-append so concurrent connections to the
// same new host cannot interleave partial writes into known_hosts.
var knownHostsMu sync.Mutex

// DefaultKnownHostsPath returns ~/.ssh/known_hosts.
func DefaultKnownHostsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh", "known_hosts"), nil
}

// AcceptNewHostKeyCallback returns a trust-on-first-use callback: a host already
// present in known_hosts must match exactly, an unknown host is recorded, and a
// changed key is rejected. This mirrors OpenSSH's StrictHostKeyChecking=accept-new.
func AcceptNewHostKeyCallback(knownHostsPath string) (ssh.HostKeyCallback, error) {
	verify, err := knownHostsVerifier(knownHostsPath)
	if err != nil {
		return nil, fmt.Errorf("parse known_hosts: %w", err)
	}

	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if err := verify(hostname, remote, key); err == nil {
			return nil
		} else if !isUnknownHostKey(err) {
			return fmt.Errorf("verify host key for %s: %w", hostname, err)
		}

		knownHostsMu.Lock()
		defer knownHostsMu.Unlock()

		// Re-read under the lock: another connection may have recorded this host
		// after the callback captured its initial snapshot.
		current, err := knownHostsVerifier(knownHostsPath)
		if err != nil {
			return fmt.Errorf("reload known_hosts: %w", err)
		}
		if err := current(hostname, remote, key); err == nil {
			return nil
		} else if !isUnknownHostKey(err) {
			return fmt.Errorf("verify host key for %s: %w", hostname, err)
		}

		zlog.L().Info().
			Str("host", hostname).
			Str("key_type", key.Type()).
			Str("fingerprint", ssh.FingerprintSHA256(key)).
			Msg("recording new host key")
		return appendKnownHost(knownHostsPath, hostname, key)
	}, nil
}

// knownHostsVerifier returns a non-writing callback. A missing file behaves as
// "no keys known" so accept-new can create it later.
func knownHostsVerifier(knownHostsPath string) (ssh.HostKeyCallback, error) {
	if _, err := os.Stat(knownHostsPath); errors.Is(err, fs.ErrNotExist) {
		return func(string, net.Addr, ssh.PublicKey) error {
			return &knownhosts.KeyError{}
		}, nil
	} else if err != nil {
		return nil, err
	}
	return knownhosts.New(knownHostsPath)
}

// isUnknownHostKey reports whether err means "no entry for this host" rather
// than "entry exists but the key differs".
func isUnknownHostKey(err error) bool {
	var keyErr *knownhosts.KeyError
	if !errors.As(err, &keyErr) {
		return false
	}
	return len(keyErr.Want) == 0
}

func appendKnownHost(knownHostsPath, hostname string, key ssh.PublicKey) error {
	if err := os.MkdirAll(filepath.Dir(knownHostsPath), 0o700); err != nil {
		return fmt.Errorf("create known_hosts directory: %w", err)
	}
	line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key)

	f, err := os.OpenFile(knownHostsPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("append known_hosts: %w", err)
	}
	if _, err := fmt.Fprintln(f, line); err != nil {
		_ = f.Close()
		return fmt.Errorf("write known_hosts: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync known_hosts: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close known_hosts: %w", err)
	}
	// Best-effort: a successful write should not fail because chmod was denied.
	_ = os.Chmod(knownHostsPath, 0o600)
	return nil
}

// probeHostKeyOnce caches a throwaway key used only to interrogate known_hosts.
var (
	probeHostKeyOnce sync.Once
	probeHostKey     ssh.PublicKey
	probeHostKeyErr  error
)

func hostKeyProbeKey() (ssh.PublicKey, error) {
	probeHostKeyOnce.Do(
		func() {
			pub, _, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				probeHostKeyErr = err
				return
			}
			probeHostKey, probeHostKeyErr = ssh.NewPublicKey(pub)
		},
	)
	return probeHostKey, probeHostKeyErr
}

// HostKeyAlgorithms returns the host key types already trusted for the given
// dial targets. Offering these first stops a server from presenting a different
// key type than the one recorded, which would look like a mismatch
// (golang/go#29286).
func HostKeyAlgorithms(knownHostsPath string, addrs ...string) []string {
	verify, err := knownHostsVerifier(knownHostsPath)
	if err != nil {
		return nil
	}
	probe, err := hostKeyProbeKey()
	if err != nil {
		return nil
	}

	seen := make(map[string]struct{})
	var algos []string
	add := func(algo string) {
		if _, ok := seen[algo]; ok {
			return
		}
		seen[algo] = struct{}{}
		algos = append(algos, algo)
	}

	for _, addr := range addrs {
		if addr == "" {
			continue
		}
		_, port, err := net.SplitHostPort(addr)
		if err != nil {
			continue
		}
		remote := &net.TCPAddr{IP: net.IPv4zero}
		if p, err := strconv.Atoi(port); err == nil {
			remote.Port = p
		}
		// A KeyError carrying Want entries is how knownhosts reports the keys
		// it holds for this address.
		err = verify(addr, remote, probe)
		var keyErr *knownhosts.KeyError
		if !errors.As(err, &keyErr) || len(keyErr.Want) == 0 {
			continue
		}
		for _, known := range keyErr.Want {
			typ := known.Key.Type()
			if typ == ssh.KeyAlgoRSA {
				// RSA host keys may be presented under their SHA-2 variants.
				add(ssh.KeyAlgoRSASHA512)
				add(ssh.KeyAlgoRSASHA256)
			}
			add(typ)
		}
	}
	return algos
}
