<!-- The two files are the same mark: the dark one swaps the name for the violet
     the browser paints itself with on a dark theme. GitHub picks between them
     from the reader's own setting, which is why this is not one image. -->
<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo-wordmark-dark.svg">
    <img src="assets/logo-wordmark.svg" alt="ohmyssh" width="400">
  </picture>
</p>

# ohmyssh

<!-- CI and the Go version are read from the repository rather than written here,
     so they follow the workflow and go.mod instead of drifting from them. Both
     need the repository to be up at that path before they render. -->
<p align="center">
  <a href="https://github.com/maogou/ohmyssh/actions/workflows/ci.yml"><img src="https://github.com/maogou/ohmyssh/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://pkg.go.dev/github.com/maogou/ohmyssh"><img src="https://pkg.go.dev/badge/github.com/maogou/ohmyssh.svg" alt="Go Reference"></a>
  <a href="https://github.com/maogou/ohmyssh/blob/main/go.mod"><img src="https://img.shields.io/github/go-mod/go-version/maogou/ohmyssh?logo=go" alt="Go version"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License: MIT"></a>
</p>

[English](README.md) | [中文](README_CN.md)

An SSH connection manager with an interactive host browser. It reads the hosts
you already have in `~/.ssh/config` — nothing new to configure, nothing to keep
in step — and the hosts you add from the browser go to `~/.ohmyssh/hosts`, which
is read alongside it.

## Install

```sh
go install github.com/maogou/ohmyssh/cmd/ohmyssh@latest
```

Go 1.27.1 or newer, which is what `go.mod` names. An older toolchain fetches
the one the module asks for rather than failing — `GOTOOLCHAIN=auto` is the
default — at the cost of that download.

## Usage

```
ohmyssh                      browse hosts and connect with enter
ohmyssh web1                 open a shell on web1
ohmyssh web1 -- uptime       run one command under a PTY
ohmyssh exec web1 -- df -h   run one command without a PTY
ohmyssh scp ./a.sh web1:/tmp copy a file, naming the host the way scp does
ohmyssh list                 browse hosts
ohmyssh forget web1          delete the password saved for web1
```

### Browsing

`ohmyssh` with no arguments opens a host list. Type to filter by name, hostname,
user or tag, move with `↑`/`↓`, and press `enter` to connect. The session takes
over the terminal; when it ends you land back on the list with the outcome in
the status line, ready for the next host.

The list of keys along the bottom of the screen is a budget: on a narrow
terminal it gives up its rightmost hints, and the ones that go first are the ones
the screen itself already says. `?` is what to press for the whole list, for
whichever view is on screen — the host list's keys from the host list, the file
view's from the file view — and it is where the bindings the bar has no room for
are written down, along with where a host added with `A` is written.

`U` and `D` open a two-pane file view on the host under the cursor: the local
files on the left, the host's home directory on the right. `U` starts with the
cursor in the left pane, `D` in the right, but both open the same view — which
way a file moves is decided by the pane the cursor is in, not by the key it was
opened with.

```
tab          switch panes        enter   directory: walk in — file: send it
↑ ↓ k j      move                c       send the whole tree, directory or not
← h          up a level          /       filter the pane you are in
pgup pgdown  a page at a time    r       re-read the pane you are in
g G          first, last         esc     stop a transfer, then leave
?            the whole list
```

The entry under the cursor goes to whatever the *other* pane is showing, under
its own name, so a transfer is a walk through two directories rather than two
paths typed from memory: a file lands in the other pane's directory, and so does
a directory, as a directory of its own rather than spilling its contents into
one. `c` is the half of the pair `enter` cannot do: it sends a directory whole
rather than walking into it. The progress bars draw where the listing was, `esc`
stops a transfer, and where it got to is reported in the status line like
anything else.

A terminal narrower than 80 columns draws one pane at a time rather than two too
narrow to read a name in; `tab` brings the other forward. Names starting with a
dot are left out of both panes — the two columns are read side by side, and two
of them disagreeing about what a file is would read as a bug. Take `.env` and
`.ssh` along with `put` and `get`, which have no such qualms.

`A` adds a host, which saves leaving the browser to write one into a config file
by hand: five fields, `tab` between them, `enter` to save and `esc` to cancel.
Only the alias is required — an empty field is left out of the block and takes
ssh's own default — and the block goes to `~/.ohmyssh/hosts`, which the browser
reads along with your ssh config. [Adding hosts](#adding-and-removing-hosts) has
the details.

`X` deletes the host under the cursor, after asking. The question is drawn on the
status line with the list still on screen — the one thing a confirmation has to
show is which host it is about — and `y` goes ahead while *any* other key
(`n`, `esc`, `enter`, anything) leaves the host alone:

```
? delete web2 from ~/.ohmyssh/hosts?  y/n
```

Only hosts in `~/.ohmyssh/hosts` can be deleted: the ones you added with `A`. A
host from your ssh config is refused and the message names that file, since
ohmyssh reads it and never writes it. Deleting a host keeps the password saved
for it — a password belongs to a login (`user@host:port`) that another alias may
well share — so `ohmyssh forget <alias>` is what clears it, if that is what you
meant. [Adding and removing hosts](#adding-and-removing-hosts) has the details.

`A`, `U`, `D` and `X` are capital letters for a reason, and they follow `q`'s
rule: they act on an empty filter only. A plain `a`, `u`, `d` or `x` would
otherwise be swallowed out of the first host name you searched for that starts
with one. `?` needs no such rule, and is a binding whatever the filter says: no
host has a question mark in a name, an address, a user or a tag, so the character
is no use to a search. The file view never prompts for a password — it owns the
terminal — so a host with no saved password is told to connect to it once from a
shell instead.

Unlisted targets work too, in the shape ssh accepts:

```sh
ohmyssh deploy@10.0.0.5
ohmyssh deploy@10.0.0.5:2222
```

### Commands

| Command | Aliases | Purpose |
| --- | --- | --- |
| `list` | `ls`, `browse` | Browse hosts interactively (`--filter` seeds the search) |
| `connect` | `ssh` | Open a shell, or run a command under a PTY |
| `exec` | | Run a command without a PTY, streaming stdout/stderr |
| `put` | | Upload a file or a directory tree: `put <host> <local> <remote>` |
| `get` | | Download one: `get <host> <remote> <local>` |
| `scp` | | Copy either way, naming the host as `host:path` |
| `forget` | | Delete saved passwords (`--all`, or list them with no argument) |

`connect` and `exec` differ in whether a pseudo-terminal is requested. Use
`connect` for interactive programs and `exec` when the output is piped
somewhere: `exec` leaves stdout and stderr untouched, so it composes.

Put `--` before a remote command so its flags are not parsed by ohmyssh.

### Transfers

`put`, `get` and `scp` move files over SFTP on the connection ohmyssh already
knows how to make: a host behind a `ProxyJump`, with a saved password or with a
host key you have already trusted transfers without any of that being set up
again.

```sh
ohmyssh put web1 ./deploy.sh /tmp/deploy.sh   # host, local, remote
ohmyssh get web1 /var/log/app.log ./app.log   # host, remote, local
ohmyssh scp ./deploy.sh web1:/tmp/deploy.sh   # host:path, either way round
```

`scp` reads the direction off which side names a host, so it is `put` or `get`
with nothing to decide. A path with no colon is local, a bare `web1:` is that
host's home directory, and both ends naming a host is an error rather than a
guess — there is no one credential to send a file between two machines with.

Directories go recursively, and what lands where follows `cp` and `rsync`:

| Command | Result |
| --- | --- |
| `put web1 ./dist/ /opt/app/` | the contents of `dist` into `/opt/app` |
| `put web1 ./dist /opt/app` | the same: a directory's *contents* go into the destination, not a directory named after it |
| `put web1 ./a.txt /opt/app/` | `/opt/app/a.txt` |
| `put web1 ./a.txt /opt/app` | the same: `/opt/app` is a directory, and a file cannot be written over one |
| `put web1 ./a.txt /opt/app/renamed.txt` | `/opt/app/renamed.txt`, overwritten |

A file takes its own name whenever the destination is a directory — saying so
with a trailing slash or by the directory already being there, which is how `cp`
and `scp` read it too. A destination that is not there is the name of the file
being made: `put web1 ./a.txt /opt/new.txt` writes a file called `new.txt`.

Existing files are overwritten, as `scp` does. A transfer that is stopped part way
— `esc` in the browser, ctrl+c on the command line — leaves what had already been
written at the far end, since bytes already sent cannot be recalled. The file is
there but incomplete; `scp` behaves the same way, so check for a truncated file
before resuming one.

Progress is drawn on stderr, so stdout stays clean and the result can be piped
somewhere. On a terminal the bars are rewritten in place; anywhere else — a pipe,
a CI log — they give way to one line per file, because a cursor moving up and
rewriting lines is unreadable once it has been captured.

```
  deploy.sh  ████████████████████████░░░░░░░░  83%  12 MB/s  ETA 1s
  3 files    ████████████████░░░░░░░░░░░░░░░░  61%  2/3  4.0 MB / 6.6 MB
✓ put ./dist → /opt/app  3 files  6.6 MB
```

Symlinks are never recreated at the far end. Uploading, a link that points at a
regular file is copied as the bytes behind it, and one that points at a directory
is left out — following it would let a link pointing back up the tree recurse
without end. Downloading, every link is left out: the listing the transfer is
measured from carries each entry's type, and resolving one would cost a round trip
per link. Anything that is not a regular file — a socket, a device, a named pipe —
is left out either way, having no bytes to send.

Both ends of a transfer are measured before any of it is copied, which is what
lets the overall bar have a denominator and what makes a mistyped path fail
without moving anything. It also means a very large directory spends a moment
being walked before the first byte goes; a download walks the remote tree over the
connection to do it.

### Exit status

The remote exit status becomes ohmyssh's exit status, so both of these work as
expected:

```sh
ohmyssh exec web1 -- systemctl is-active nginx
ohmyssh exec web1 -- failing-command && echo ok
```

### Global flags

| Flag | Default | Purpose |
| --- | --- | --- |
| `--config`, `-F` | `~/.ssh/config` | Config file to read (`OHMYSSH_CONFIG`) |
| `--known-hosts` | `~/.ssh/known_hosts` | Known-hosts file (`OHMYSSH_KNOWN_HOSTS`) |
| `--password`, `-p` | | Password for password/keyboard-interactive auth (`OHMYSSH_PASSWORD`) |
| `--password-stdin` | | Read the password from standard input, one line |
| `--no-prompt` | | Never prompt for a password; fail instead |
| `--no-save-password` | | Do not remember passwords that work |
| `--no-agent` | | Do not authenticate with ssh-agent |
| `--insecure` | | Skip host key verification (unsafe) |
| `--timeout` | `15s` | Connection and handshake timeout |
| `--log-level` | `info` | `trace`, `debug`, `info`, `warn`, `error`, `disabled` |
| `--log-format` | `console` | `console` or `json` |
| `--debug`, `-d` | | Shortcut for `--log-level debug` |

Flags are global, so they can appear before or after the subcommand. Logs go to
stderr, which keeps `exec` output safe to pipe. The browser is the exception:
while it is up its logs go to `~/.ohmyssh/ohmyssh.log`, because the terminal then
belongs to the frame and a log line written into it lands in the middle of one.
Text that is not a log line goes the same way — a server's login banner, a
`ProxyCommand`'s own stderr — which would otherwise be the same problem with a
different name on it.

## What it reads from your config

The parser follows OpenSSH's own rules, including the ones that are easy to get
wrong:

- **First value wins** per parameter, so a specific `Host` block beats a
  catch-all `Host *` that appears later in the file.
- **File-scope directives** (before any `Host` or `Match`) act as global
  defaults.
- `Include` globs, `Match` blocks, `~` expansion, `=` as a separator, and
  quoting all behave as they do in ssh.
- `*` and `?` wildcards, `!` negation in a `Host` list, and multi-alias lines
  like `Host web1 web2` are all resolved per target.

Wildcard-only blocks are not offered as browsable hosts, since they name a
pattern rather than a machine.

### Tags

A `# Tags:` comment attaches tags to the block that follows, and tags are
searchable in the browser:

```sshconfig
# Tags: prod, web
Host web1
    HostName 10.0.0.1
    User deploy
```

## Adding and removing hosts

`A` in the browser — an empty filter, like `U`, `D` and `X` — opens a form for a
host worth keeping:

```
alias  required; the name you type
host   address; the alias when empty
user   login name; yours when empty
port   22
tags   prod, web
```

`tab` or `↑`/`↓` moves between the fields, `enter` saves, `esc` cancels. What is
written is an ordinary `Host` block with a `# Tags:` line above it, in the same
syntax as your ssh config, so the file can be read, edited and version controlled
like any other config you keep. A refused add leaves the form up with everything
in it: what is wrong with a host is usually one field, and retyping the other four
to fix it is the worst thing a form can ask for.

**Where it goes.** `~/.ohmyssh/hosts` (`%USERPROFILE%\.ohmyssh\hosts` on
Windows), written 0600 in a directory 0700 — the same place and the same
permissions as the saved passwords. Your ssh config is never written to, and what
is already in the file is left exactly as it is: the block is appended at the end,
by way of a temporary file, so an interrupted write cannot leave half a block
behind for everything after it in the file to be parsed against.

**How it is read.** The two files are parsed as one, as if your ssh config ended
with `Include ~/.ohmyssh/hosts`. A file-scope `User`, `IdentityFile` or
`ProxyJump` in your ssh config therefore applies to a host added here exactly as
it does to one you wrote by hand — which is what keeps such a host connectable
without anything else being set up. The order matters in one direction only:
first value wins across both files and your ssh config is read first, so nothing
in `~/.ohmyssh/hosts` can shadow a host you already had. An alias that is already
taken is refused, and the message names the file it is already in. `--config`
replaces the ssh config being read; the hosts added here are read either way,
being your own state rather than part of any one config file.

**What it is not.** These hosts are ohmyssh's, and `ssh web2` does not know about
them — ssh reads `~/.ssh/config` and nothing else. A host you want in both places
belongs in `~/.ssh/config`, where you would have written it anyway; ohmyssh reads
that file first and browses it as it always has.

**Removing.** `X` takes one back out. It is the same file and the same block the
add wrote: the `Host` line that names the alias, its directives, and the
`# Tags:` line above it, which belongs to the block under it and would otherwise
end up tagging the host below. Everything else in the file is left exactly as it
is — the delete works line by line rather than by rewriting what it parsed, so
your own comments, indentation and blank lines survive untouched, and the write
goes through the same temporary file and rename as the add, so an interrupted
delete cannot truncate the file.

A host from your ssh config is refused rather than deleted, and the message names
that file: ohmyssh reads it and never writes it, so the only thing it could do
with a `y` there is fail. The password saved for the host is kept — it is saved
against `user@host:port`, which another alias may share, so deleting a host is
one decision and forgetting its password is another. `ohmyssh forget <alias>`
makes the second one, and the status line says so when a host goes.

## Authentication

Methods are tried in the order ssh uses:

1. ssh-agent
2. A key named by `IdentityFile`
3. The default keys: `id_ed25519`, `id_ecdsa`, `id_rsa`, `id_dsa`
4. Password and keyboard-interactive: a saved password first, otherwise a prompt

An encrypted key is unlocked with the password given by `--password`, or the one
prompted for after authentication fails, so `-p` doubles as the key passphrase.
A key that cannot be unlocked is skipped rather than aborting the attempt, so
the remaining methods still get their turn.

`ProxyJump` chains and `ProxyCommand` are both supported, including a jump host
that is itself another alias in the config.

### Saved passwords

A password is typed once. When one works, it is saved and used from then on, so
the second connection to a host asks for nothing:

```
$ ohmyssh web1                # asks for the password, then saves it
$ ohmyssh web1                # connects straight away
```

Passwords are keyed by login identity (`deploy@10.0.0.1:22`), not by alias, so
two aliases for the same machine share one entry, and changing `User` or `Port`
asks for the new password rather than silently reusing the old one. A saved
password that has stopped working is replaced by a prompt — a stale entry can
never lock you out.

To manage what has been saved:

```sh
ohmyssh forget                # list the hosts with a saved password
ohmyssh forget web1           # forget one
ohmyssh forget --all          # forget all of them
```

`--no-save-password` turns off saving for a run while still using what is
already saved. `--password` still works and takes precedence, which is what you
want in a script.

**In a script, pipe the password rather than passing it.** An argument is
public: anything that can list the processes on the machine can read a command
line, and the shell keeps it in its history. What arrives on standard input is
neither. `--password-stdin` reads one line and leaves the rest of the input
alone:

```sh
echo "$PW" | ohmyssh --password-stdin web1 -- uptime
ohmyssh --password-stdin web1 < ~/.secrets/web1
```

The two flags cannot be combined in one run, and an empty line is refused rather
than attempted.

**Where they are kept.** Passwords are stored in `~/.ohmyssh/credentials.json`
(`%USERPROFILE%\.ohmyssh\credentials.json` on Windows), written mode 0600 in a
directory mode 0700 — which Windows applies as the read-only attribute and
nothing else, so there the encryption is the whole of the protection. A store
left at the old `~/.config/ohmyssh/` location is moved into place the first time
one is read, passwords intact.

**What the encryption is worth.** The file is sealed with AES-256-GCM under a
key derived from your user, hostname and OS. That keeps the passwords out of
sight of anything that merely reads the file — a backup, a sync client, a stray
`cat` — but the same machine and account can derive the key, so it is not a
defence against a compromised account. It is obfuscation with a clean interface,
not a secret store. A copy of the file on another machine will not decrypt;
those passwords read as absent and you are prompted again.

## Host keys

Unknown hosts are trusted on first use and appended to `known_hosts` (mode
0600). A host whose key has *changed* is rejected — that is the case where
trust-on-first-use stops protecting you, so it stops rather than asking.
`--insecure` disables verification entirely and says so in the log.

## Development

```sh
make build      # build ./ohmyssh
make test       # go test ./...
make lint       # golangci-lint run ./...
make coverage   # write coverage.html and open it
```

Without make:

```sh
go build ./...
go vet ./...
go test ./...
```

The test suite stands up a real in-process SSH server, so dialling, host key
verification, authentication, PTY handling, exit status propagation and SFTP
transfers are all exercised against the actual protocol rather than mocks. The
server serves a temporary directory as its SFTP root, which is what lets an
upload be asserted on by reading the files back off the filesystem. The password
policy — which password is tried, when you are asked, what gets saved — is
covered separately in `internal/service`, against a transport that answers dials
instead of opening sockets, so each rule is asserted on its own.

`make lint` needs golangci-lint v2:

```sh
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
```

### Layout

```
cmd/ohmyssh/           entry point
internal/command/      the command tree, and flags → options
internal/service/      orchestration and the password policy
internal/repository/   interfaces over dialling, host lookup and the store
internal/sshclient/    SSH itself: sessions, PTY, auth, known_hosts, SFTP
```

`command` only turns a command line into options. `service` never sees a
`*cli.Command`. `repository` puts `sshclient`, `config` and `credential` behind
interfaces, which is what lets the password policy be tested without a
connection.

## License

[MIT](LICENSE). Use it, change it, ship it — the licence asks one thing, that
the copyright notice travels with it, and withholds one, any warranty. The
files under `assets/` are the project's own and carry the same licence.
