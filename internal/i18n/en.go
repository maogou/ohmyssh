package i18n

import "fmt"

// english is the catalogue the source is written in, and the one a run falls
// back to. Every message here was read off the renderer or the command it came
// from, so this file is the same program in the same words: an English terminal
// sees what it saw before there were two languages.
var english = Messages{
	// The host list.
	HostsCount:        "%d of %d hosts",
	NoHostsConfig:     "No hosts found. Add a Host block to ~/.ssh/config.",
	NoHostsAdd:        "No hosts found. Press A to add one.",
	NoHostsMatch:      "No hosts match the filter. Press esc to clear it.",
	FilterPlaceholder: "filter by name, host, user or tag",
	MoreHosts:         "↓ %d more",
	ColumnName:        "NAME",
	ColumnUser:        "USER",
	ColumnHost:        "HOST",
	ColumnPort:        "PORT",
	ColumnTags:        "TAGS",
	FormTitle:         "new host",
	SourceSSHConfig:   "the ssh config",

	DisconnectedFrom:    "disconnected from %s",
	AddedTo:             "added %s to %s",
	Added:               "added %s",
	DeletedFrom:         "deleted %s from %s",
	Deleted:             "deleted %s",
	PasswordKeptSuffix:  " (saved password kept)",
	NoTransferService:   "transfers are unavailable: no file service is attached",
	NoHostServiceAdd:    "adding hosts is unavailable: no host service is attached",
	NoHostServiceRemove: "deleting hosts is unavailable: no host service is attached",
	HostNotWritable:     "%s is in %s, which ohmyssh does not write; edit it there",
	ConfirmDelete:       "delete %s?  y/n",
	ConfirmDeleteFrom:   "delete %s from %s?  y/n",

	// The key bar.
	HintConnect: "connect",
	HintMove:    "move",
	HintClear:   "clear",
	HintQuit:    "quit",
	HintAdd:     "add",
	HintDelete:  "delete",
	HintFiles:   "files",
	HintHelp:    "help",
	HintFilter:  "filter",
	HintPane:    "pane",
	HintSend:    "send",
	HintCopy:    "copy",
	HintFind:    "find",
	HintReload:  "reload",
	HintBack:    "back",
	HintKeep:    "keep",
	HintCancel:  "cancel",
	HintSave:    "save",
	HintField:   "field",

	// The help.
	HelpKeys:         "keys · %s",
	HelpHostList:     "host list",
	HelpFileView:     "file view",
	HelpConnect:      "connect to the host under the cursor",
	HelpMove:         "move the cursor",
	HelpPageHosts:    "a page of hosts at a time",
	HelpEndsHosts:    "the first and the last host",
	HelpType:         "filter by name, host, user or tag",
	HelpEsc:          "clear the filter, or quit when it is already empty",
	HelpQuit:         "quit",
	HelpQuitAnywhere: "quit from anywhere, whatever is on screen",
	HelpAdd:          "add a host",
	HelpAddWrittenTo: "add a host, written to %s",
	HelpDelete:       "delete the host under the cursor, after asking",
	HelpFiles:        "open the file view on it: U the local pane, D the remote one",
	HelpTab:          "switch panes",
	HelpMoveEntries:  "move the cursor",
	HelpPageEntries:  "a page of entries at a time",
	HelpEndsEntries:  "the first and the last entry",
	HelpEnterEntry:   "walk into a directory; send a file to the other pane",
	HelpCopyEntry:    "send the entry whole, directory or not",
	HelpParent:       "up to the parent directory",
	HelpFind:         "filter the pane that has the keyboard",
	HelpReload:       "read that pane's directory again",
	HelpEscFiles:     "cancel the transfer, clear the filter, then leave the view",
	HelpTheseKeys:    "show key function help",
	HelpClose:        "close",

	// The file view.
	FilesTitle:            "files · %s",
	PaneLocal:             "[LOCAL]",
	PaneRemote:            "[REMOTE]",
	PaneConnecting:        "connecting…",
	PaneNoSession:         "no session",
	PaneLoading:           "loading…",
	PaneNoMatch:           "no match",
	PaneEmpty:             "empty",
	PaneFilterPlaceholder: "filter this pane",
	StatusCancelling:      "cancelling…",
	TransferRunning:       "a transfer is already running",
	NoSessionYet:          "no session yet: still connecting",
	TransferCancelled:     "cancelled %s %s → %s",

	// The add form.
	FormAlias:            "alias",
	FormHost:             "host",
	FormUser:             "user",
	FormPort:             "port",
	FormTags:             "tags",
	FormAliasPlaceholder: "required; the name you type",
	FormHostPlaceholder:  "address; the alias when empty",
	FormUserPlaceholder:  "login name; yours when empty",
	FormPortPlaceholder:  "22",
	FormTagsPlaceholder:  "prod, web",

	// Counts and rates.
	Files: func(n int) string {
		if n == 1 {
			return "1 file"
		}
		return fmt.Sprintf("%d files", n)
	},
	ETA: "ETA %s",

	// The command line.
	RootUsage: "SSH connection manager with an interactive host browser",
	RootDescription: `ohmyssh reads hosts from your OpenSSH config and connects to them over SSH.

Run it without arguments to browse hosts interactively, or name a host to
connect straight away:

  ohmyssh                  browse hosts and connect with enter
  ohmyssh web1             open a shell on web1
  ohmyssh web1 -- uptime   run one command under a PTY
  ohmyssh exec web1 -- df -h   run one command without a PTY
  ohmyssh put web1 ./out/ /opt/app/   copy files to web1

A password can be piped in rather than passed as an argument, which keeps it out
of the process list and the shell's history:

  echo "$PW" | ohmyssh --password-stdin web1 -- uptime

Host key verification uses trust-on-first-use against ~/.ssh/known_hosts:
unknown hosts are recorded, changed keys are rejected.`,

	ListUsage:       "Browse hosts interactively and connect with enter",
	ListFilterUsage: "seed the host filter",

	ConnectUsage: "Open an interactive shell (or run a command under a PTY)",
	ConnectDescription: `Connect to a host from your ssh config and open a login shell.

A trailing command runs under a PTY, like ssh -t, so interactive programs work:

  ohmyssh connect web1
  ohmyssh connect web1 -- htop

Use -- before the command so its own flags are not parsed by ohmyssh.

A password can be piped in rather than typed, which keeps it out of a process
list it would otherwise be visible in:

  echo "$PW" | ohmyssh connect --password-stdin web1

Running connect with no host opens the interactive host browser.`,

	ExecUsage: "Run a command on a remote host without a PTY",
	ExecDescription: `Run one command on a host and return its exit status.

Output is streamed straight to stdout and stderr, so exec composes in pipelines:

  ohmyssh exec web1 -- df -h
  ohmyssh exec web1 -- cat /etc/hostname | tr -d '\n'

The remote exit status becomes ohmyssh's exit status.

A password can be piped in rather than typed, which keeps it out of a process
list it would otherwise be visible in:

  echo "$PW" | ohmyssh exec --password-stdin web1 -- uptime`,

	PutUsage: "Upload a file or a directory tree to a host",
	PutDescription: `Copy local to remote, recursively when local is a directory.

Progress is reported on stderr, so stdout stays clean for the summary line:

  ohmyssh put web1 ./deploy.sh /tmp/deploy.sh
  ohmyssh put web1 ./out/ /opt/app/

A directory copies its contents into the remote path, not into a directory named
after it: the two lines above leave the files directly under /opt/app. A remote
path ending in a slash is a directory, so a single file keeps its own name
inside it. Existing files are overwritten.

A password can be piped in rather than typed, which keeps it out of a process
list it would otherwise be visible in:

  echo "$PW" | ohmyssh put --password-stdin web1 ./out/ /opt/app/`,

	GetUsage: "Download a file or a directory tree from a host",
	GetDescription: `Copy remote to local, recursively when remote is a directory.

It is put with the ends swapped, and the same rules apply:

  ohmyssh get web1 /var/log/app.log ./app.log
  ohmyssh get web1 /opt/app/ ./app/

A password can be piped in rather than typed, which keeps it out of a process
list it would otherwise be visible in:

  echo "$PW" | ohmyssh get --password-stdin web1 /var/log/app.log ./app.log

A directory copies its contents into the local path. A local path ending in a
separator is a directory, so a single file keeps its own name inside it.
Existing files are overwritten.`,

	ScpUsage: "Copy between here and a host, naming each end the way scp does",
	ScpDescription: `Copy two paths, exactly one of which names a host as host:path.

  ohmyssh scp ./deploy.sh web1:/tmp/deploy.sh
  ohmyssh scp web1:/var/log/app.log ./app.log

The direction follows from which side names a host, so scp is put and get with
nothing to decide. A path with no colon is local; a path with an empty one,
web1:, is the host's home directory.

A password can be piped in rather than typed, which keeps it out of a process
list it would otherwise be visible in:

  echo "$PW" | ohmyssh scp --password-stdin web1:/var/log/app.log ./app.log`,

	ForgetUsage:    "Delete a saved password",
	ForgetAllUsage: "forget every saved password",
	ForgetDescription: `Delete the password ohmyssh saved for a host.

Connecting saves a password once it works, so that later connections do not ask
for one. forget is the undo:

  ohmyssh forget web1        forget the password for web1
  ohmyssh forget --all       forget every saved password
  ohmyssh forget             list the hosts with a saved password

Saved passwords live in an encrypted file; run --log-level debug to see where.`,

	ExecMissing: "exec requires a host and a command: ohmyssh exec <host> -- <command>",
	PutMissing:  "put requires a host, a local path and a remote path: ohmyssh put <host> <local> <remote>",
	GetMissing:  "get requires a host, a remote path and a local path: ohmyssh get <host> <remote> <local>",
	ScpMissing:  "scp requires a source and a destination: ohmyssh scp <src> <dst>",
	ScpNoHost:   "scp needs one end to name a host: %s and %s are both local",
	ScpTwoHosts: "scp copies between here and a host, not between two hosts: %s and %s both name one",

	FlagConfigUsage:         "path to the ssh config file (default ~/.ssh/config)",
	FlagKnownHostsUsage:     "path to known_hosts (default ~/.ssh/known_hosts)",
	FlagLogLevelUsage:       "log level: trace, debug, info, warn, error, disabled",
	FlagLogFormatUsage:      "log format: console or json",
	FlagDebugUsage:          "shortcut for --log-level debug",
	FlagInsecureUsage:       "skip host key verification (unsafe: allows interception)",
	FlagNoAgentUsage:        "do not authenticate with ssh-agent",
	FlagPasswordUsage:       "password for password/keyboard-interactive auth (prompted when omitted)",
	FlagPasswordStdinUsage:  "read the password from standard input, one line",
	FlagNoPromptUsage:       "never prompt for a password; fail instead",
	FlagNoSavePasswordUsage: "do not remember passwords that work (saved ones are still used)",
	FlagTimeoutUsage:        "connection and handshake timeout",
	FlagLangUsage:           "language: en or zh, or auto to follow the environment",
	PasswordBoth:            "use --password or --password-stdin, not both",
	PasswordStdinEmpty:      "--password-stdin read no password from standard input",

	ForgetNone:  "no saved passwords",
	ForgotOne:   "forgot saved password for %s",
	ForgotAll:   "forgot %d saved password(s)",
	SavedIn:     "saved passwords in %s:",
	ForgetHowTo: "\nforget one with: ohmyssh forget <host>",

	// What the service layer says when a host cannot be added or deleted. These
	// are shown by the command line and in the browser's status line alike.
	AliasTaken:      "alias %q is already in %s",
	NotOhmysshsFile: "host %q is in %s, which ohmyssh does not write; remove it there",
	NoSavedPassword: "no saved password for %s",
	NoSavedPasswordTransfer: "no saved password for %s: connect to it once, " +
		"or use ohmyssh put or get from a shell",

	UnsupportedLanguage: "unsupported language %q; this ohmyssh has %s",
}
