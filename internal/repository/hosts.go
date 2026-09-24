package repository

import (
	"errors"
	"fmt"
	"strings"

	"github.com/maogou/ohmyssh/internal/config"
	"github.com/maogou/ohmyssh/internal/pkg/zlog"
)

// Hosts reads the host definitions ohmyssh connects to, and writes the ones the
// user adds through it.
type Hosts interface {
	// List returns every connectable host: those in configPath, or in the default
	// ssh config when configPath is empty, together with those added through
	// ohmyssh.
	List(configPath string) ([]config.SSHHost, error)
	// Find picks the host named by target out of an already-listed set, so a
	// caller that needs both the host and its neighbours reads the config once.
	// target may be an alias from the config, a name the config defines only by
	// wildcard, or a bare [user@]host[:port] the config knows nothing about.
	// configPath is the same one List was given, and is consulted only when the
	// listed set has no alias for target.
	Find(configPath string, all []config.SSHHost, target string) (config.SSHHost, error)
	// Add writes a host the user typed into the file ohmyssh keeps its own hosts
	// in, which Path names. It does not check for a clash of aliases: the caller
	// has the list it just read, and only it can say which file a clash is with.
	Add(h config.NewHost) error
	// Remove deletes the block naming alias from that same file. It is the only
	// file it can touch: the ssh config is the user's, and ohmyssh does not write
	// it. A host that is not there is an error, not a quiet success.
	Remove(alias string) error
	// Path is where Add writes and Remove deletes from, for the browser to show
	// before it writes there.
	Path() string
}

// NewHosts returns the ssh-config-backed implementation. extraPath is the file
// ohmyssh keeps hosts added through it in, read alongside the ssh config; an
// empty extraPath reads the ssh config alone, which is what a caller that only
// wants the user's own config — and every test — passes.
func NewHosts(extraPath string) Hosts { return hosts{extra: extraPath} }

type hosts struct {
	// extra is the ohmyssh host file. It is a field rather than a call to the
	// default path so that reading the hosts never reaches outside what the
	// caller asked for: a test listing a temporary config must not pick up the
	// hosts of whoever is running it.
	extra string
}

func (h hosts) List(configPath string) ([]config.SSHHost, error) {
	paths, err := h.paths(configPath)
	if err != nil {
		return nil, err
	}
	found, err := config.ParseSSHConfigFiles(paths...)
	if err != nil {
		return nil, err
	}
	zlog.L().Debug().Str("config", paths[0]).Str("added", h.extra).Int("hosts", len(found)).Msg("loaded hosts")
	return found, nil
}

// paths are the files read, in the order they are read in. Both are read as one,
// so the ssh config's file-scope defaults reach the hosts ohmyssh added and
// cannot be duplicated by accident.
func (h hosts) paths(configPath string) ([]string, error) {
	primary := configPath
	if primary == "" {
		path, err := config.DefaultSSHConfigPath()
		if err != nil {
			return nil, err
		}
		primary = path
	}
	if h.extra == "" {
		return []string{primary}, nil
	}
	return []string{primary, h.extra}, nil
}

func (h hosts) Add(host config.NewHost) error {
	if h.extra == "" {
		return errors.New("no file is configured for hosts added through ohmyssh")
	}
	return config.AddHost(h.extra, host)
}

func (h hosts) Remove(alias string) error {
	if h.extra == "" {
		return errors.New("no file is configured for hosts added through ohmyssh")
	}
	return config.RemoveHost(h.extra, alias)
}

func (h hosts) Path() string { return h.extra }

// Find accepts a config alias, a name the config defines by wildcard, or a bare
// [user@]host[:port] target, so ad-hoc connections work the way they do with ssh.
func (h hosts) Find(configPath string, all []config.SSHHost, target string) (config.SSHHost, error) {
	if !strings.ContainsAny(target, "@:") {
		for _, candidate := range all {
			if candidate.Name == target {
				return candidate, nil
			}
		}

		// Nothing is aliased by that name, but a wildcard block may still define
		// it: ssh resolves web1.corp.example.com through Host *.corp.example.com,
		// and reporting it as missing would send the user looking for a machine
		// whose definition is in the file they are already reading — with the
		// ProxyJump and IdentityFile that make it reachable left behind.
		host, found, err := h.resolveByName(configPath, target)
		if err != nil {
			return config.SSHHost{}, err
		}
		if found {
			return host, nil
		}

		if suggestion := closestName(all, target); suggestion != "" {
			return config.SSHHost{}, fmt.Errorf(
				"host %q not found in ssh config (did you mean %q?)", target, suggestion,
			)
		}
		return config.SSHHost{}, fmt.Errorf("host %q not found in ssh config", target)
	}

	// An explicit user@host or host:port bypasses the config entirely.
	user, rest := "", target
	if before, after, found := strings.Cut(target, "@"); found {
		user, rest = before, after
	}
	hostname, port := rest, ""
	if before, after, found := strings.Cut(rest, ":"); found && !strings.Contains(after, ":") {
		hostname, port = before, after
	}
	if hostname == "" {
		return config.SSHHost{}, fmt.Errorf("cannot parse target %q", target)
	}
	return config.SSHHost{Name: hostname, Hostname: hostname, User: user, Port: port}, nil
}

// resolveByName asks the config whether a wildcard block defines target, which
// is the one lookup the resolved host list cannot answer: a block that names a
// pattern is deliberately not offered as a host, so the name it covers is not in
// the list either.
func (h hosts) resolveByName(configPath, target string) (config.SSHHost, bool, error) {
	paths, err := h.paths(configPath)
	if err != nil {
		return config.SSHHost{}, false, err
	}
	host, found, err := config.ResolveName(target, paths...)
	if err != nil {
		return config.SSHHost{}, false, fmt.Errorf("read ssh config: %w", err)
	}
	if found {
		zlog.L().Debug().Str("host", host.Name).Str("config", paths[0]).
			Msg("resolved a host a wildcard block defines")
	}
	return host, found, nil
}

// closestName returns a config alias similar to name, for typo hints.
func closestName(all []config.SSHHost, name string) string {
	lower := strings.ToLower(name)
	for _, candidate := range all {
		alias := strings.ToLower(candidate.Name)
		if strings.HasPrefix(alias, lower) || strings.HasPrefix(lower, alias) {
			return candidate.Name
		}
	}
	return ""
}
