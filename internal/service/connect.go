package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/maogou/ohmyssh/internal/config"
	"github.com/maogou/ohmyssh/internal/i18n"
	"github.com/maogou/ohmyssh/internal/pkg/errno"
	"github.com/maogou/ohmyssh/internal/pkg/prompt"
	"github.com/maogou/ohmyssh/internal/repository"
	"github.com/maogou/ohmyssh/internal/sshclient"
	"github.com/maogou/ohmyssh/internal/ui"
	"github.com/rs/zerolog"
)

// ConnectService covers the ways ohmyssh reaches a host: browsing them, opening
// an interactive session, running one command without a PTY, and moving files
// to and from one.
type ConnectService interface {
	// Browse shows the host browser and returns when the user quits.
	Browse(ctx context.Context, opts ConnectOptions, filter string) error
	// Connect opens an interactive session on target, running command instead
	// of a login shell when command is non-empty.
	Connect(ctx context.Context, opts ConnectOptions, target string, command []string) error
	// Exec runs command on target without a PTY, streaming its output. command
	// must be non-empty; the caller checks the command line it came from.
	Exec(ctx context.Context, opts ConnectOptions, target string, command []string, stdout, stderr io.Writer) error
	// Transfer moves a file or a directory tree between the local machine and
	// target, reporting progress as it goes.
	Transfer(ctx context.Context, opts ConnectOptions, req TransferRequest, progress sshclient.ProgressFunc) (TransferResult, error)
	// AddHost writes a host the user filled in to the file ohmyssh keeps its own
	// hosts in, and returns the reloaded list, which is what the browser shows
	// next.
	AddHost(ctx context.Context, opts ConnectOptions, host config.NewHost) ([]config.SSHHost, error)
	// RemoveHost deletes a host from that same file and returns the reloaded
	// list. A host that came from the user's ssh config is refused: ohmyssh does
	// not write that file.
	RemoveHost(ctx context.Context, opts ConnectOptions, host config.SSHHost) ([]config.SSHHost, error)
}

// NewConnectService wires the connect use cases to their dependencies.
func NewConnectService(
	logger *zerolog.Logger,
	transport repository.Transport,
	hosts repository.Hosts,
	creds repository.Credential,
) ConnectService {
	return &connectService{
		Service:     NewService(logger),
		transport:   transport,
		hosts:       hosts,
		creds:       creds,
		askPassword: prompt.Password,
	}
}

type connectService struct {
	transport repository.Transport
	hosts     repository.Hosts
	creds     repository.Credential
	// askPassword reads a password from the user. It is a field rather than a
	// direct call so tests can answer for it: the real prompt needs a terminal,
	// and the branch it guards is the least obvious in the package.
	askPassword func(label string) (string, error)
	*Service
}

func (s *connectService) Browse(ctx context.Context, opts ConnectOptions, filter string) error {
	all, err := s.hosts.List(opts.ConfigPath)
	if err != nil {
		return fmt.Errorf("read ssh config: %w", err)
	}

	dial := dialOptions(opts, all)
	return ui.RunHostBrowser(
		ui.BrowserOptions{
			Hosts:  all,
			Filter: filter,
			// The file shown in the add form is the one the service will write,
			// so the form cannot name one place and the service another.
			HostsFile: s.hosts.Path(),
			Session: func(ctx context.Context, host config.SSHHost) error {
				return s.session(ctx, host, opts, dial, nil)
			},
			Remote: func(ctx context.Context, host config.SSHHost) (ui.RemoteSession, error) {
				return s.openRemote(ctx, host, opts, dial)
			},
			Add: func(ctx context.Context, host config.NewHost) ([]config.SSHHost, error) {
				return s.AddHost(ctx, opts, host)
			},
			Remove: func(ctx context.Context, host config.SSHHost) ([]config.SSHHost, error) {
				return s.RemoveHost(ctx, opts, host)
			},
		},
	)
}

// AddHost writes a host the user filled in to the file ohmyssh keeps its own
// hosts in and returns the list as it reads after the write, so the browser can
// show the new host without asking for a second read.
//
// The clash check is here rather than in the form because it is the list that
// knows: an alias already in the user's ssh config would be shadowed by nothing
// and duplicated by nothing either, since the config is read first — so the new
// block would sit in the file looking live while the connection went to the old
// host. That is worth refusing, and worth naming the file it is already in.
func (s *connectService) AddHost(ctx context.Context, opts ConnectOptions, host config.NewHost) ([]config.SSHHost, error) {
	if err := host.Validate(); err != nil {
		return nil, err
	}

	all, err := s.hosts.List(opts.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("read ssh config: %w", err)
	}
	for _, existing := range all {
		if existing.Name != host.Alias {
			continue
		}
		where := existing.SourceFile
		if where == "" {
			where = i18n.M().SourceSSHConfig
		}
		return nil, fmt.Errorf(i18n.M().AliasTaken, host.Alias, where)
	}

	if err := s.hosts.Add(host); err != nil {
		return nil, err
	}
	s.logger.Info().
		Str("alias", host.Alias).
		Str("file", s.hosts.Path()).
		Msg("added host")

	all, err = s.hosts.List(opts.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("read ssh config: %w", err)
	}
	return all, nil
}

// RemoveHost deletes a host added through ohmyssh and returns the list as it
// reads after the delete, so the browser can take the row off screen without
// asking for a second read.
//
// Only the file ohmyssh keeps its own hosts in is ever written. A host that came
// from the user's ssh config is refused, with the file it is in named: that file
// is the user's, ohmyssh only ever reads it, and the delete they want there is
// an edit they make themselves — one ohmyssh cannot do for them without
// rewriting a file it has promised not to touch. The check comes first, so a
// refusal leaves the disk exactly as it was.
func (s *connectService) RemoveHost(ctx context.Context, opts ConnectOptions, host config.SSHHost) ([]config.SSHHost, error) {
	if filepath.Clean(host.SourceFile) != filepath.Clean(s.hosts.Path()) {
		where := host.SourceFile
		if where == "" {
			where = i18n.M().SourceSSHConfig
		}
		return nil, fmt.Errorf(i18n.M().NotOhmysshsFile, host.Name, where)
	}

	if err := s.hosts.Remove(host.Name); err != nil {
		return nil, err
	}
	s.logger.Info().
		Str("alias", host.Name).
		Str("file", s.hosts.Path()).
		Msg("removed host")

	all, err := s.hosts.List(opts.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("read ssh config: %w", err)
	}
	return all, nil
}

func (s *connectService) Connect(ctx context.Context, opts ConnectOptions, target string, command []string) error {
	host, all, err := s.target(opts.ConfigPath, target)
	if err != nil {
		return err
	}
	return s.session(ctx, host, opts, dialOptions(opts, all), command)
}

func (s *connectService) Exec(ctx context.Context, opts ConnectOptions, target string, command []string, stdout, stderr io.Writer) error {
	host, all, err := s.target(opts.ConfigPath, target)
	if err != nil {
		return err
	}

	client, err := s.dialWithPrompt(ctx, host, opts, dialOptions(opts, all))
	if err != nil {
		return err
	}
	// The command has already run its course by the time this fires; a failure
	// to close adds nothing the caller could act on.
	defer func() { _ = client.Close() }()

	if err := s.transport.RunCommand(ctx, client, command, stdout, stderr); err != nil {
		// A command that ran and failed is a normal outcome, and its status is
		// forwarded without a message of ours: the command has already said
		// whatever there was to say. Anything else failed on this side, and is
		// reported as an ordinary error — sshclient.ExitCode would flatten it
		// into the same silent status a failing command produces.
		if exitErr, ok := errors.AsType[*sshclient.ExitError](err); ok {
			s.logger.Debug().Int("exit_code", exitErr.Code).Msg("remote command failed")
			return errno.NewExit(exitErr.Code, "")
		}
		return err
	}
	return nil
}

// session dials host and runs an interactive session on it, mapping a non-zero
// remote status onto an exit status of our own.
func (s *connectService) session(
	ctx context.Context,
	host config.SSHHost,
	opts ConnectOptions,
	dial sshclient.DialOptions,
	command []string,
) error {
	client, err := s.dialWithPrompt(ctx, host, opts, dial)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	if err := s.transport.Interactive(client, command).Run(); err != nil {
		if exitErr, ok := errors.AsType[*sshclient.ExitError](err); ok {
			// A non-zero remote status is a normal outcome, not a CLI failure:
			// exit with the same code and no extra noise.
			if exitErr.Code == 0 {
				return nil
			}
			return errno.NewExit(exitErr.Code, "")
		}
		return err
	}
	return nil
}

// target resolves target against the config, returning the host together with
// the full list, which dialOptions needs to resolve ProxyJump aliases.
func (s *connectService) target(configPath, target string) (config.SSHHost, []config.SSHHost, error) {
	all, err := s.hosts.List(configPath)
	if err != nil {
		return config.SSHHost{}, nil, fmt.Errorf("read ssh config: %w", err)
	}
	host, err := s.hosts.Find(configPath, all, target)
	if err != nil {
		return config.SSHHost{}, nil, err
	}
	return host, all, nil
}

// dialWithPrompt connects to host, asking for a password only when it has to.
//
// A password given by flag wins; otherwise a saved one is used. That is what
// makes the second connection to a host silent. A password that works is then
// remembered, so it is typed once rather than every time. A saved password that
// has stopped working is not reported as the failure — it is replaced by asking,
// because a stale entry should not be able to lock the user out.
func (s *connectService) dialWithPrompt(
	ctx context.Context,
	host config.SSHHost,
	opts ConnectOptions,
	dial sshclient.DialOptions,
) (*sshclient.Client, error) {
	key := s.creds.Key(host)
	save := !opts.NoSavePassword

	fromFlag := dial.Password != ""
	if !fromFlag {
		if saved, ok := s.creds.Get(key); ok {
			s.logger.Debug().Str("host", host.Name).Msg("using saved password")
			dial.Password = saved
		}
	}

	client, err := s.transport.Dial(ctx, host, dial)
	if err == nil {
		// Re-save only what the user just supplied, not what we read back.
		if save && fromFlag {
			s.remember(key, host, dial.Password)
		}
		return client, nil
	}

	if !sshclient.IsAuthFailure(err) || opts.NoPrompt {
		return nil, err
	}
	if dial.Password != "" {
		s.logger.Debug().Str("host", host.Name).Msg("password rejected; asking for another")
	}

	password, promptErr := s.askPassword(passwordLabel(host))
	if promptErr != nil {
		// Nothing to prompt on (piped stdin, no TTY); report the real failure.
		s.logger.Debug().Err(promptErr).Msg("password prompt unavailable")
		return nil, err
	}

	dial.Password = password
	client, err = s.transport.Dial(ctx, host, dial)
	if err != nil {
		return nil, err
	}
	if save {
		s.remember(key, host, password)
	}
	return client, nil
}

// remember stores a password that has just been shown to work.
func (s *connectService) remember(key string, host config.SSHHost, password string) {
	if password == "" {
		return
	}
	if err := s.creds.Set(key, password); err != nil {
		s.logger.Warn().Err(err).Str("host", host.Name).Msg("could not save password")
		return
	}
	s.logger.Info().
		Str("host", key).
		Msg("saved password; --no-save-password disables this, ohmyssh forget removes it")
}

// passwordLabel names the host in a password prompt. It uses the hostname
// rather than the alias: the alias is a local nickname, and the user is being
// asked about a credential on a specific machine.
func passwordLabel(host config.SSHHost) string {
	name := host.Hostname
	if name == "" {
		name = host.Name
	}
	return host.DisplayUser() + "@" + name
}

// dialOptions derives connection settings from the options the command line
// produced. LookupHost lets a ProxyJump entry name another config alias rather
// than a literal address.
func dialOptions(opts ConnectOptions, all []config.SSHHost) sshclient.DialOptions {
	return sshclient.DialOptions{
		Password:              opts.Password,
		DisableAgent:          opts.NoAgent,
		KnownHosts:            opts.KnownHosts,
		InsecureIgnoreHostKey: opts.Insecure,
		Timeout:               opts.Timeout,
		LookupHost: func(alias string) (config.SSHHost, bool) {
			for _, candidate := range all {
				if candidate.Name == alias {
					return candidate, true
				}
			}
			return config.SSHHost{}, false
		},
	}
}
