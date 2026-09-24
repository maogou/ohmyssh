// Package sshclient implements the SSH transport: authentication, host key
// verification, proxy traversal and interactive sessions.
//
// The connection method follows zsuroy/ctty's internal/sftpconfig: a
// trust-on-first-use known_hosts callback, already-trusted host key algorithms
// offered first, and an authentication chain of agent, identity file, default
// keys and finally password / keyboard-interactive.
package sshclient

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/maogou/ohmyssh/internal/config"
	"github.com/maogou/ohmyssh/internal/pkg/zlog"
)

// DefaultDialTimeout bounds the TCP and handshake phases of a connection.
const DefaultDialTimeout = 15 * time.Second

// keepAliveInterval is how often an idle connection tells the far end it is
// still there.
//
// A file view is one connection held open for as long as the user browses, and
// a session may sit idle for minutes between commands. Without this, a NAT or
// firewall that drops mappings it has not seen used leaves a connection that
// looks open and answers nothing: the next thing the user runs hangs until they
// give up, instead of failing. OpenSSH's own ServerAliveInterval exists for the
// same reason.
const keepAliveInterval = 30 * time.Second

// keepAliveRequest is the global request OpenSSH both sends for this and answers.
const keepAliveRequest = "keepalive@openssh.com"

// DialOptions tunes how a connection is established.
type DialOptions struct {
	// Password enables password and keyboard-interactive auth. It also unlocks
	// passphrase-protected private keys.
	Password string
	// DisableAgent skips ~/.ssh/ssh-agent discovery.
	DisableAgent bool
	// KnownHosts overrides the default ~/.ssh/known_hosts path.
	KnownHosts string
	// InsecureIgnoreHostKey skips host key verification entirely. Intended for
	// throwaway test hosts; it makes the connection vulnerable to interception.
	InsecureIgnoreHostKey bool
	// Timeout bounds the TCP connect and SSH handshake. Zero means DefaultDialTimeout.
	Timeout time.Duration
	// ProxyJump overrides the host config's ProxyJump.
	ProxyJump string
	// ProxyCommand overrides the host config's ProxyCommand.
	ProxyCommand string
	// LookupHost resolves a ProxyJump alias against parsed SSH config. When nil,
	// the alias is treated as a literal [user@]host[:port].
	LookupHost func(alias string) (config.SSHHost, bool)
}

func (o DialOptions) timeout() time.Duration {
	if o.Timeout > 0 {
		return o.Timeout
	}
	return DefaultDialTimeout
}

// Client is an established SSH connection to a single host.
type Client struct {
	*ssh.Client
	Host config.SSHHost

	// onClose releases transport resources that outlive the SSH connection
	// itself, such as the bastion clients used for ProxyJump.
	onClose func()

	// done stops the keepalive loop.
	done chan struct{}

	// closeMu serialises the teardown. The keepalive loop is now one of the two
	// places Close comes from — it is what takes the connection down when the
	// far end stops answering — so the caller's Close and the loop's can race,
	// and the order below has to hold whichever one arrives first: the loop
	// stops, the connection goes, then what the transport opened behind it is
	// released. stopped is what turns the second call into a no-op; without it,
	// closing done twice panics.
	closeMu sync.Mutex
	stopped bool
}

// Close releases the underlying connection and any proxy resources. It is safe
// to call while the keepalive loop is also closing the connection: the first
// one in does the teardown, and the second finds it done.
func (c *Client) Close() error {
	if c == nil || c.Client == nil {
		return nil
	}
	c.closeMu.Lock()
	defer c.closeMu.Unlock()

	if !c.stopped {
		c.stopped = true
		// The keepalive loop is stopped before the connection goes: its request
		// is in flight on that connection, and there is no point in it failing
		// on the way out.
		close(c.done)
	}

	err := c.Client.Close()
	if c.onClose != nil {
		c.onClose()
		c.onClose = nil
	}
	return err
}

// keepAlive holds the connection open for as long as it is idle, and is how a
// connection that died without saying so is noticed here rather than by whatever
// the user runs next. It stops at Close.
//
// The interval is a parameter so a test can watch several of them go by; Dial
// passes keepAliveInterval, which is what production runs at. A reply is given
// twice its own interval — patience for a badly congested link, since a healthy
// server answers in a round trip — and a round trip that outlives that takes
// the connection down: see ping.
func (c *Client) keepAlive(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			if !c.ping(interval * 2) {
				return
			}
		}
	}
}

// ping sends one keepalive and reports whether the connection answered within
// wait.
//
// The request runs on a goroutine of its own because SendRequest blocks until
// the reply or a connection error, and on the failure that matters it gets
// neither: a NAT or firewall that dropped the mapping delivers no response and
// no reset, so the call would hang until a TCP timeout measured in minutes, or
// forever. The wait below is what bounds it.
//
// A miss takes the connection down, which is what makes the verdict everyone's
// problem rather than this loop's: Close fails every session and transfer
// blocked on the connection now, instead of leaving them hanging on a socket
// that will never answer. The abandoned goroutine ends when that close makes
// its SendRequest return; its verdict lands in a buffered channel and is
// dropped, the connection having already been judged.
func (c *Client) ping(wait time.Duration) bool {
	replied := make(chan error, 1)
	go func() {
		_, _, err := c.SendRequest(keepAliveRequest, true, nil)
		replied <- err
	}()

	select {
	case err := <-replied:
		// A server that answers that it does not know the request is still a
		// server that answered, which is all this asks of it.
		if err == nil {
			return true
		}
		zlog.L().Debug().Err(err).Str("host", c.Host.Name).
			Msg("keepalive failed; the connection is gone")
	case <-time.After(wait):
		zlog.L().Debug().Str("host", c.Host.Name).
			Msg("keepalive went unanswered; the connection is gone")
	}

	_ = c.Close()
	return false
}

// Dial establishes a connection to host, traversing ProxyJump or ProxyCommand
// when the host config specifies one.
func Dial(ctx context.Context, host config.SSHHost, opts DialOptions) (*Client, error) {
	cfg, authCleanup, err := buildSSHConfig(host, opts)
	if err != nil {
		return nil, err
	}

	addr := host.Addr()
	zlog.L().Debug().
		Str("host", host.Name).
		Str("addr", addr).
		Str("user", cfg.User).
		Strs("auth", authLabelsFor(host, opts)).
		Msg("dialing")

	conn, closer, err := dialTransport(ctx, host, addr, opts)
	if err != nil {
		authCleanup()
		return nil, err
	}

	sshConn, chans, reqs, err := handshake(ctx, conn, addr, cfg)
	if err != nil {
		_ = conn.Close()
		closer()
		authCleanup()
		return nil, fmt.Errorf("ssh handshake with %s: %w", addr, err)
	}

	zlog.L().Info().
		Str("host", host.Name).
		Str("addr", addr).
		Msg("connected")

	// Both of these release resources that outlive the handshake: what the
	// transport opened for a proxy, and the ssh-agent connection the auth chain
	// read its signers from. They belong to the connection, so they go when it
	// goes rather than when this function returns.
	client := &Client{
		Client: ssh.NewClient(sshConn, chans, reqs),
		Host:   host,
		done:   make(chan struct{}),
		onClose: func() {
			closer()
			authCleanup()
		},
	}
	// The loop is left running for the life of the connection: it is what tells
	// a connection that has quietly gone away from one that is merely idle.
	go client.keepAlive(keepAliveInterval)
	return client, nil
}

// handshake runs the SSH handshake over conn, bounded in two ways the plain
// call is not.
//
// A wall-clock timer covers the whole exchange — banner, key exchange,
// authentication — because ClientConfig.Timeout bounds only the TCP dial that
// preceded it: a server that accepts the connection and then never answers the
// version exchange would otherwise hang the dial for as long as the socket
// stays open. And a cancelled context ends the attempt now rather than when the
// far end gets round to answering: ctrl+c in the middle of connecting should not
// have to wait for a server that has stopped saying anything.
//
// Both bounds close the connection rather than set a deadline on it. The mux
// that ssh.NewClientConn builds underneath reads from conn in its own goroutine,
// started before that call returns, and takes any read error as the connection
// being gone — so a deadline can expire in the moment between the handshake
// succeeding and this function clearing it, and would then take down a
// connection that is perfectly alive. Closing has the further advantage of
// working where a deadline cannot: the pipes behind a ProxyCommand transport
// carry no deadlines at all.
func handshake(ctx context.Context, conn net.Conn, addr string, cfg *ssh.ClientConfig) (ssh.Conn, <-chan ssh.NewChannel, <-chan *ssh.Request, error) {
	// A nil channel blocks forever, so a configuration with no timeout simply
	// never takes this arm of the select below.
	var timeout <-chan time.Time
	if cfg.Timeout > 0 {
		timer := time.NewTimer(cfg.Timeout)
		defer timer.Stop()
		timeout = timer.C
	}

	type outcome struct {
		conn  ssh.Conn
		chans <-chan ssh.NewChannel
		reqs  <-chan *ssh.Request
		err   error
	}
	// Buffered, so the goroutine always lands its result whether or not anyone
	// is still reading: the two abort paths below read it only to know the
	// goroutine is done.
	done := make(chan outcome, 1)
	go func() {
		c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
		done <- outcome{conn: c, chans: chans, reqs: reqs, err: err}
	}()

	// abort ends a handshake that is stuck: the goroutine is blocked in a read or
	// a write on conn, and closing it is what lets that call return. Waiting for
	// the goroutine keeps it from racing whoever owns the connection next — the
	// caller closes it again on this error path — and from leaking past here.
	abort := func(err error) error {
		_ = conn.Close()
		<-done
		return err
	}

	select {
	case res := <-done:
		return res.conn, res.chans, res.reqs, res.err
	case <-timeout:
		return nil, nil, nil, abort(fmt.Errorf("timed out after %s", cfg.Timeout))
	case <-ctx.Done():
		return nil, nil, nil, abort(ctx.Err())
	}
}

// buildSSHConfig assembles the client configuration for host together with a
// cleanup for what the configuration holds open.
//
// That is the ssh-agent connection: it has to stay open until the handshake has
// used its signers, so it cannot be closed here. It is handed back for whoever
// ends up owning the connection it authenticated, which is what makes an agent
// socket not outlive a failed handshake or a connection that never opened.
func buildSSHConfig(host config.SSHHost, opts DialOptions) (cfg *ssh.ClientConfig, cleanup func(), err error) {
	chain := buildAuthChain(host, opts)
	cleanup = chain.cleanup
	// Every failure below leaves an agent connection with nobody to close it,
	// since the caller only takes the cleanup on success. The release path is
	// therefore held in a variable of its own: the failures below return through
	// the named result, and a return statement assigns to it before a deferred
	// call reads it, so reading cleanup here would release a nil and panic — with
	// the error that caused it never reaching the user.
	release := cleanup
	defer func() {
		if err != nil {
			release()
		}
	}()

	if len(chain.methods) == 0 {
		return nil, nil, fmt.Errorf(
			"no authentication methods available for %s: no ssh-agent keys, no readable private keys, and no password supplied",
			host.Name,
		)
	}

	cfg = &ssh.ClientConfig{
		User:    host.DisplayUser(),
		Auth:    chain.methods,
		Timeout: opts.timeout(),
		BannerCallback: func(message string) error {
			// The banner is the server's own text rather than a log line, so it
			// goes where the logs are going: it is often several lines, and a view
			// drawing on the terminal would have them land in the middle of the
			// frame. A file keeps it readable afterwards either way.
			//
			// A write that fails is not worth refusing the connection over, which
			// is all returning the error here would do.
			_, _ = fmt.Fprint(zlog.Output(), message)
			return nil
		},
	}

	if opts.InsecureIgnoreHostKey {
		zlog.L().Warn().Str("host", host.Name).Msg("host key verification disabled")
		cfg.HostKeyCallback = ssh.InsecureIgnoreHostKey()
		return cfg, cleanup, nil
	}

	knownHostsPath := opts.KnownHosts
	if knownHostsPath == "" {
		p, pathErr := DefaultKnownHostsPath()
		if pathErr != nil {
			return nil, nil, fmt.Errorf("locate known_hosts: %w", pathErr)
		}
		knownHostsPath = p
	}
	callback, err := AcceptNewHostKeyCallback(knownHostsPath)
	if err != nil {
		return nil, nil, err
	}
	cfg.HostKeyCallback = callback

	// Prefer the key types already recorded for either the alias or the real
	// hostname, so the server cannot present a different type that would be
	// reported as a mismatch.
	addrs := []string{host.Addr()}
	if host.Name != "" && host.Name != host.Hostname {
		addrs = append(addrs, config.JoinHostPort(host.Name, host.Port))
	}
	cfg.HostKeyAlgorithms = HostKeyAlgorithms(knownHostsPath, addrs...)
	return cfg, cleanup, nil
}

// authLabelsFor re-derives the auth labels for logging without dialing the
// agent a second time.
func authLabelsFor(host config.SSHHost, opts DialOptions) []string {
	// buildAuthChain is cheap for everything except the agent dial, so only the
	// non-agent portion is described here.
	var labels []string
	if host.Identity != "" {
		labels = append(labels, "publickey:file")
	}
	if !opts.DisableAgent && (host.IdentityAgent != "" || os.Getenv("SSH_AUTH_SOCK") != "") {
		labels = append(labels, "publickey:agent")
	}
	if opts.Password != "" {
		labels = append(labels, "password", "keyboard-interactive")
	}
	return labels
}

// dialTransport returns a raw connection to addr, possibly tunnelled through a
// proxy. The returned closer releases proxy resources once the SSH connection
// is torn down.
func dialTransport(ctx context.Context, host config.SSHHost, addr string, opts DialOptions) (net.Conn, func(), error) {
	proxyCommand := firstNonEmpty(opts.ProxyCommand, host.ProxyCommand)
	proxyJump := firstNonEmpty(opts.ProxyJump, host.ProxyJump)

	if proxyCommand != "" {
		conn, err := proxyCommandConn(ctx, proxyCommand)
		if err != nil {
			return nil, nil, err
		}
		zlog.L().Debug().Str("addr", addr).Msg("connecting via ProxyCommand")
		return conn, func() {}, nil
	}

	if proxyJump != "" {
		bastion, closeBastion, err := dialBastionChain(ctx, proxyJump, opts, host)
		if err != nil {
			return nil, nil, err
		}
		conn, err := hopDial(ctx, bastion, addr, opts.timeout())
		if err != nil {
			closeBastion()
			return nil, nil, fmt.Errorf("dial %s through %s: %w", addr, proxyJump, err)
		}
		zlog.L().Debug().Str("addr", addr).Str("via", proxyJump).Msg("connecting via ProxyJump")
		return conn, closeBastion, nil
	}

	dialer := net.Dialer{Timeout: opts.timeout()}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return conn, func() {}, nil
}

// dialBastionChain connects to the last host in a comma-separated ProxyJump
// chain, returning the client whose Dial tunnels onward to the target.
func dialBastionChain(ctx context.Context, chain string, opts DialOptions, target config.SSHHost) (*ssh.Client, func(), error) {
	hops := splitTrim(chain, ",")
	if len(hops) == 0 {
		return nil, nil, fmt.Errorf("empty ProxyJump for %s", target.Name)
	}

	var (
		current *ssh.Client
		closers []func()
	)
	closeAll := func() {
		for _, closeHop := range slices.Backward(closers) {
			closeHop()
		}
	}

	for i, hop := range hops {
		hopHost := resolveHop(hop, opts)
		// Each hop after the first is reached through the previous connection.
		hopOpts := opts
		if i > 0 {
			hopOpts.ProxyJump = ""
			hopOpts.ProxyCommand = ""
		}

		cfg, hopCleanup, err := buildSSHConfig(hopHost, hopOpts)
		if err != nil {
			closeAll()
			return nil, nil, fmt.Errorf("proxy jump %s: %w", hop, err)
		}
		// The agent connection behind this hop is released with the hop, so it
		// has to be registered before the failure paths below can run closeAll.
		// Backward iteration then closes the hop first and the agent after it,
		// which is the order that lets the signers outlive their last use.
		closers = append(closers, hopCleanup)

		hopAddr := hopHost.Addr()
		var conn net.Conn
		if current == nil {
			dialer := net.Dialer{Timeout: opts.timeout()}
			conn, err = dialer.DialContext(ctx, "tcp", hopAddr)
		} else {
			conn, err = hopDial(ctx, current, hopAddr, opts.timeout())
		}
		if err != nil {
			closeAll()
			return nil, nil, fmt.Errorf("dial proxy jump %s: %w", hop, err)
		}

		sshConn, chans, reqs, err := handshake(ctx, conn, hopAddr, cfg)
		if err != nil {
			_ = conn.Close()
			closeAll()
			return nil, nil, fmt.Errorf("proxy jump handshake with %s: %w", hopAddr, err)
		}
		current = ssh.NewClient(sshConn, chans, reqs)
		// Capture the value, not the variable: `current` is reassigned on the
		// next hop, so a closure over it would close only the final bastion.
		hop := current
		closers = append(closers, func() { _ = hop.Close() })
	}
	return current, closeAll, nil
}

// hopDial opens a connection to addr through an established SSH connection,
// bounded the way the direct dial already is.
//
// ssh.Client.Dial takes no context and sets no deadline: it asks the far end to
// open a channel and waits for the answer. A bastion that accepts the request and
// then says nothing — a firewall dropping the onward connection, a host out of
// file descriptors — leaves that wait open forever, and the interrupt the user
// gave at the terminal goes with it, because the cancellation only ever reached
// the dial that built the first hop.
//
// ssh.Client.DialContext is not used because it bounds only the wait: it returns
// on the context while the dial it started is still in flight, which leaves a
// goroutine parked on a bastion that is not answering, for whoever closes the
// connection next to release. Closing here instead is what lets the dial return,
// and the result is collected before this function does — the same shape as
// handshake above, and for the same reasons.
//
// ssh.Client offers nothing finer to set than that close, and closing is enough:
// the mux underneath drops every channel it is holding when its read loop ends,
// so the pending request ends with it rather than at the far end's convenience.
func hopDial(ctx context.Context, client *ssh.Client, addr string, timeout time.Duration) (net.Conn, error) {
	// A nil channel blocks forever, so no timeout means this arm is never taken.
	var expired <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		expired = timer.C
	}

	type outcome struct {
		conn net.Conn
		err  error
	}
	// Buffered, so the goroutine always lands its result whether or not anyone is
	// still reading: the abort paths below read it only to know it is done.
	done := make(chan outcome, 1)
	go func() {
		conn, err := client.Dial("tcp", addr)
		done <- outcome{conn: conn, err: err}
	}()

	select {
	case res := <-done:
		return res.conn, res.err
	case <-expired:
		_ = client.Close()
		<-done
		return nil, fmt.Errorf("timed out after %s", timeout)
	case <-ctx.Done():
		_ = client.Close()
		<-done
		return nil, ctx.Err()
	}
}

// resolveHop turns a ProxyJump entry into a host definition, preferring an
// alias defined in the user's SSH config over a literal address.
func resolveHop(hop string, opts DialOptions) config.SSHHost {
	hop = strings.TrimSpace(hop)
	if opts.LookupHost != nil {
		if h, ok := opts.LookupHost(hop); ok {
			return h
		}
	}

	// Fall back to [user@]host[:port].
	user, rest := "", hop
	if before, after, found := strings.Cut(hop, "@"); found {
		user, rest = before, after
	}
	hostname, port := rest, ""
	if h, p, err := net.SplitHostPort(rest); err == nil {
		hostname, port = h, p
	}
	return config.SSHHost{Name: hop, Hostname: hostname, User: user, Port: port}
}

// proxyCommandConn runs command and treats its stdio as the transport, which is
// how OpenSSH's ProxyCommand works.
func proxyCommandConn(ctx context.Context, command string) (net.Conn, error) {
	// ProxyCommand is a shell fragment by definition; it comes from the user's
	// own SSH config, so shell evaluation matches OpenSSH's behaviour.
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("proxy command stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("proxy command stdout: %w", err)
	}
	// The proxy command's own stderr goes where the logs go. It is ordinary text
	// to a terminal user, and a ProxyCommand can just as well be started by a view
	// that owns the terminal, where it would be printed into the frame.
	//
	// Output is a *os.File in both cases, so the command inherits the descriptor
	// itself — stderr for a command line, the log file while a view is up — and
	// gets no pipe and copying goroutine it did not have before.
	cmd.Stderr = zlog.Output()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start proxy command: %w", err)
	}
	return &cmdConn{cmd: cmd, stdin: stdin, stdout: stdout}, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func splitTrim(s, sep string) []string {
	var out []string
	for part := range strings.SplitSeq(s, sep) {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
