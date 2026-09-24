package service

import "time"

// ConnectOptions is what the command line decided about a connection: the
// service layer's view of the global flags, so that service never sees a
// *cli.Command.
type ConnectOptions struct {
	// ConfigPath is the ssh config to read; empty means the default.
	ConfigPath string
	// Password is the one given on the command line. It wins over a saved one.
	Password string
	// NoPrompt refuses to ask for a password, so a host that needs one fails.
	NoPrompt bool
	// NoSavePassword uses saved passwords but does not add new ones.
	NoSavePassword bool
	// NoAgent skips ssh-agent authentication.
	NoAgent bool
	// KnownHosts overrides the known_hosts path.
	KnownHosts string
	// Insecure disables host key verification.
	Insecure bool
	// Timeout bounds the TCP connect and the SSH handshake.
	Timeout time.Duration
}
