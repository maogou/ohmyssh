package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// names is the aliases of a host list, in order, which is what most of these
// tests are actually asking about.
func names(hosts []SSHHost) []string {
	out := make([]string, len(hosts))
	for i, h := range hosts {
		out[i] = h.Name
	}
	return out
}

func hostNamed(t *testing.T, hosts []SSHHost, name string) SSHHost {
	t.Helper()
	for _, h := range hosts {
		if h.Name == name {
			return h
		}
	}
	t.Fatalf("no host named %q in %v", name, names(hosts))
	return SSHHost{}
}

// The second file is read as if it were Included at the end of the first, so a
// default set at file scope in the first reaches a host declared in the second.
// Without this, a host added through ohmyssh would ignore the IdentityFile the
// rest of the config relies on and fail to authenticate, with nothing on screen
// to say why.
func TestDefaultsReachHostsFromTheSecondFile(t *testing.T) {
	dir := t.TempDir()
	primary := writeFile(t, dir, "config", `
User deploy
IdentityFile ~/.ssh/id_ed25519

Host web1
    HostName 10.0.0.1
`)
	extra := writeFile(t, dir, "hosts", `
Host web2
    HostName 10.0.0.2
`)

	hosts, err := ParseSSHConfigFiles(primary, extra)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got, want := names(hosts), []string{"web1", "web2"}; !slices.Equal(got, want) {
		t.Fatalf("hosts = %v, want %v", got, want)
	}

	web2 := hostNamed(t, hosts, "web2")
	if web2.User != "deploy" {
		t.Errorf("User = %q, want the file-scope default %q", web2.User, "deploy")
	}
	if web2.Identity != "~/.ssh/id_ed25519" {
		t.Errorf("IdentityFile = %q, want the file-scope default", web2.Identity)
	}
	if web2.Hostname != "10.0.0.2" {
		t.Errorf("Hostname = %q, want its own value", web2.Hostname)
	}
	if got, want := web2.SourceFile, extra; got != want {
		t.Errorf("SourceFile = %q, want %q", got, want)
	}
}

// A host named in both files resolves to the first one. The file ohmyssh writes
// is a place to add hosts, never a place to override the user's own config from.
func TestThePrimaryConfigWinsACollision(t *testing.T) {
	dir := t.TempDir()
	primary := writeFile(t, dir, "config", "Host web2\n    HostName 10.0.0.1\n    User deploy\n")
	extra := writeFile(t, dir, "hosts", "Host web2\n    HostName 192.168.1.1\n    User root\n")

	hosts, err := ParseSSHConfigFiles(primary, extra)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(hosts) != 1 {
		t.Fatalf("hosts = %v, want the alias once", names(hosts))
	}
	web2 := hosts[0]
	if web2.Hostname != "10.0.0.1" || web2.User != "deploy" {
		t.Errorf("resolved %+v, want the primary config's definition", web2)
	}
	if got, want := web2.SourceFile, primary; got != want {
		t.Errorf("SourceFile = %q, want %q", got, want)
	}
}

// The file ohmyssh writes is usually absent, and that is not a failure: it is
// every run before the user has added anything.
func TestAMissingSecondFileIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	primary := writeFile(t, dir, "config", "Host web1\n    HostName 10.0.0.1\n")

	hosts, err := ParseSSHConfigFiles(primary, filepath.Join(dir, "absent"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got, want := names(hosts), []string{"web1"}; !slices.Equal(got, want) {
		t.Errorf("hosts = %v, want %v", got, want)
	}
}

func TestValidateAcceptsAPlainHost(t *testing.T) {
	h := NewHost{Alias: "web2", Host: "10.0.0.5", User: "deploy", Port: "2222", Tags: []string{"prod"}}
	if err := h.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
	// Only the alias is required.
	if err := (NewHost{Alias: "web2"}).Validate(); err != nil {
		t.Errorf("Validate with only an alias: %v", err)
	}
}

func TestValidateRejectsWhatWouldNotReadBack(t *testing.T) {
	cases := map[string]NewHost{
		"no alias":              {},
		"alias with a space":    {Alias: "web 2"},
		"alias with a glob":     {Alias: "web*"},
		"alias with a negation": {Alias: "!web"},
		"alias with a comment":  {Alias: "web#2"},
		"host with a space":     {Alias: "web2", Host: "10 0 0 1"},
		"user with a space":     {Alias: "web2", User: "de ploy"},
		"port that is not one":  {Alias: "web2", Port: "ssh"},
		"port out of range":     {Alias: "web2", Port: "70000"},
		"port zero":             {Alias: "web2", Port: "0"},
		"port negative":         {Alias: "web2", Port: "-1"},
		"tag with a comma":      {Alias: "web2", Tags: []string{"pro,d"}},
		"tag with a space":      {Alias: "web2", Tags: []string{"prod web"}},
	}

	// A line break is the one that is not about tidiness: it would end the
	// directive and let the rest of the value be read as configuration of its own.
	for _, field := range []string{"alias", "host", "user", "port"} {
		for _, brk := range []string{"\n", "\r\n"} {
			h := NewHost{Alias: "web2"}
			switch field {
			case "alias":
				h.Alias = "web2" + brk + "ProxyCommand evil"
			case "host":
				h.Host = "10.0.0.5" + brk + "ProxyCommand evil"
			case "user":
				h.User = "deploy" + brk + "ProxyCommand evil"
			case "port":
				h.Port = "22" + brk + "ProxyCommand evil"
			}
			cases[field+" with a line break"] = h
		}
	}

	for name, h := range cases {
		if err := h.Validate(); err == nil {
			t.Errorf("%s: Validate accepted %+v", name, h)
		}
	}
}

func TestBlockIsAReadableHostBlock(t *testing.T) {
	h := NewHost{Alias: "web2", Host: "10.0.0.5", User: "deploy", Port: "2222", Tags: []string{"prod", "web"}}

	want := `# Tags: prod, web
Host web2
    HostName 10.0.0.5
    User deploy
    Port 2222
`
	if got := h.Block(); got != want {
		t.Errorf("Block() =\n%q\nwant\n%q", got, want)
	}

	// An empty field is left out rather than written bare.
	bare := NewHost{Alias: "web2"}.Block()
	if bare != "Host web2\n" {
		t.Errorf("Block() = %q, want just the Host line", bare)
	}
}

// What is written has to be what is read back: the round trip is the whole
// contract between the form and the parser.
func TestWhatIsWrittenIsReadBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts")
	h := NewHost{Alias: "web2", Host: "10.0.0.5", User: "deploy", Port: "2222", Tags: []string{"prod", "web"}}

	if err := AddHost(path, h); err != nil {
		t.Fatalf("AddHost: %v", err)
	}

	hosts, err := ParseSSHConfigFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(hosts) != 1 {
		t.Fatalf("parsed %d hosts, want 1", len(hosts))
	}
	got := hosts[0]
	if got.Name != h.Alias || got.Hostname != h.Host || got.User != h.User || got.Port != h.Port {
		t.Errorf("read back %+v, want %+v", got, h)
	}
	if !slices.Equal(got.Tags, h.Tags) {
		t.Errorf("tags = %v, want %v", got.Tags, h.Tags)
	}
}

func TestAddHostCreatesTheFilePrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "hosts")
	if err := AddHost(path, NewHost{Alias: "web2"}); err != nil {
		t.Fatalf("AddHost: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("hosts file mode = %o, want 600", perm)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("hosts directory mode = %o, want 700", perm)
	}
}

func TestAddHostAppendsWithoutDisturbingTheFile(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "hosts", "Host web1\n    HostName 10.0.0.1\n")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if err := AddHost(path, NewHost{Alias: "web2", Host: "10.0.0.2"}); err != nil {
		t.Fatalf("AddHost: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.HasPrefix(string(after), string(before)) {
		t.Errorf("the existing contents changed:\n%s", after)
	}
	// A blank line between the two, so the block that follows is not read as part
	// of the one above it.
	if !strings.HasPrefix(string(after), string(before)+"\n") {
		t.Errorf("no separating blank line:\n%q", after)
	}

	hosts, err := ParseSSHConfigFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got, want := names(hosts), []string{"web1", "web2"}; !slices.Equal(got, want) {
		t.Errorf("hosts = %v, want %v", got, want)
	}
}

// A file whose last line has no newline is common enough, and appending to it
// without care would put the new Host directive on the end of that line.
func TestAddHostFixesAFileWithNoTrailingNewline(t *testing.T) {
	path := writeFile(t, t.TempDir(), "hosts", "Host web1\n    HostName 10.0.0.1")

	if err := AddHost(path, NewHost{Alias: "web2", Host: "10.0.0.2"}); err != nil {
		t.Fatalf("AddHost: %v", err)
	}

	hosts, err := ParseSSHConfigFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got, want := names(hosts), []string{"web1", "web2"}; !slices.Equal(got, want) {
		t.Errorf("hosts = %v, want %v", got, want)
	}
	web1 := hostNamed(t, hosts, "web1")
	if web1.Hostname != "10.0.0.1" {
		t.Errorf("the host already in the file changed: %+v", web1)
	}
}

// A hosts file kept in a dotfiles repository and linked into place has to stay a
// link: a rename over the top would quietly replace it with a real file and stop
// the repository from seeing anything written afterwards.
func TestAddHostFollowsASymlink(t *testing.T) {
	dir := t.TempDir()
	real := writeFile(t, dir, "real-hosts", "Host web1\n    HostName 10.0.0.1\n")
	link := filepath.Join(dir, "hosts")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if err := AddHost(link, NewHost{Alias: "web2", Host: "10.0.0.2"}); err != nil {
		t.Fatalf("AddHost: %v", err)
	}

	if info, err := os.Lstat(link); err != nil {
		t.Fatalf("lstat: %v", err)
	} else if info.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced by a regular file")
	}
	data, err := os.ReadFile(real)
	if err != nil {
		t.Fatalf("read the link target: %v", err)
	}
	if !strings.Contains(string(data), "Host web2") {
		t.Errorf("the block did not land in the link's target:\n%s", data)
	}
}

// Whatever went wrong, the file that was already there is left as it was.
func TestAddHostRefusesBeforeItWrites(t *testing.T) {
	path := writeFile(t, t.TempDir(), "hosts", "Host web1\n    HostName 10.0.0.1\n")
	before, _ := os.ReadFile(path)

	if err := AddHost(path, NewHost{Alias: "web 2"}); err == nil {
		t.Fatal("AddHost accepted an alias with a space in it")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("the file changed on a rejected host:\n%s", after)
	}
}

// Nothing partial is left behind: a temporary file beside the hosts file would
// be picked up by a glob-based Include, and one that fails to parse takes the
// whole config with it.
func TestAddHostLeavesNoTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts")
	for i, alias := range []string{"web1", "web2"} {
		if err := AddHost(path, NewHost{Alias: alias, Host: "10.0.0.1"}); err != nil {
			t.Fatalf("AddHost %d: %v", i, err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "hosts" {
		var found []string
		for _, e := range entries {
			found = append(found, e.Name())
		}
		t.Errorf("directory holds %v, want just the hosts file", found)
	}
}

// assertRemoved writes before to a fresh hosts file, removes alias from it and
// returns what the file holds afterwards.
func assertRemoved(t *testing.T, before, alias, want string) {
	t.Helper()

	path := writeFile(t, t.TempDir(), "hosts", before)
	if err := RemoveHost(path, alias); err != nil {
		t.Fatalf("RemoveHost(%q): %v", alias, err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(after) != want {
		t.Errorf("the file holds\n%q\nwant\n%q", after, want)
	}
	// A file that no longer parses is worse than one that still names the host,
	// so every case ends by reading it back the way the browser will.
	if hosts, err := ParseSSHConfigFile(path); err != nil {
		t.Errorf("the file no longer parses: %v", err)
	} else if slices.Contains(names(hosts), alias) {
		t.Errorf("%q is still a host: %v", alias, names(hosts))
	}
}

// Removing one block leaves everything the user wrote around it — comments,
// spacing, the order of the directives — byte for byte as it was. That is what
// editing by line buys over writing the file back from what was parsed out of
// it: the file stays theirs to read, edit and version control.
func TestRemoveHostTakesOutTheBlockAndNothingElse(t *testing.T) {
	assertRemoved(t, `# my own note about the rack
Host web1
    HostName 10.0.0.1
    User deploy

# Tags: staging
Host web2
    HostName 10.0.0.2
    ProxyJump web1

# Tags: prod, db
Host db1
    HostName 10.0.0.3
`, "web2", `# my own note about the rack
Host web1
    HostName 10.0.0.1
    User deploy

# Tags: prod, db
Host db1
    HostName 10.0.0.3
`)
}

// A "# Tags:" line labels the block below it, so it goes when that block goes.
// Left behind, it labels whichever block follows instead — a host nobody tagged
// wearing the tags of the one that was deleted.
func TestRemoveHostTakesTheTagsLineWithTheBlock(t *testing.T) {
	path := writeFile(t, t.TempDir(), "hosts", `# Tags: staging
Host web2
    HostName 10.0.0.2

Host db1
    HostName 10.0.0.3
`)

	if err := RemoveHost(path, "web2"); err != nil {
		t.Fatalf("RemoveHost: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(after), "staging") {
		t.Errorf("the tags of the removed host are still in the file:\n%s", after)
	}
	hosts, err := ParseSSHConfigFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got, want := names(hosts), []string{"db1"}; !slices.Equal(got, want) {
		t.Fatalf("hosts = %v, want %v", got, want)
	}
	if tags := hostNamed(t, hosts, "db1").Tags; len(tags) != 0 {
		t.Errorf("db1 came back tagged %v, want it untagged", tags)
	}
}

// The join the removal leaves behind: a block taken from the top of a file must
// not leave the file starting on a blank line, one taken from the bottom must not
// leave it ending on one, and the blocks left behind still have to be separated
// from each other — two Host blocks written against each other read as one.
func TestRemoveHostClosesTheGapItLeaves(t *testing.T) {
	cases := []struct {
		name   string
		before string
		want   string
	}{
		{
			name:   "the first of two blocks",
			before: "\n\n# Tags: prod\nHost web2\n    HostName 10.0.0.2\n\nHost web1\n    HostName 10.0.0.1\n",
			want:   "Host web1\n    HostName 10.0.0.1\n",
		},
		{
			name:   "the last of two blocks",
			before: "Host web1\n    HostName 10.0.0.1\n\n\nHost web2\n    HostName 10.0.0.2\n",
			want:   "Host web1\n    HostName 10.0.0.1\n",
		},
		{
			name:   "a block between two others",
			before: "Host web1\n    HostName 10.0.0.1\nHost web2\n    HostName 10.0.0.2\n\nHost web3\n    HostName 10.0.0.3\n",
			want:   "Host web1\n    HostName 10.0.0.1\n\nHost web3\n    HostName 10.0.0.3\n",
		},
		{
			name:   "the only block in the file",
			before: "Host web2\n    HostName 10.0.0.2\n",
			want:   "",
		},
		{
			// The file's own shape is kept, including a missing final newline: it
			// was that way before, and a delete has no business reformatting it.
			name:   "a file that does not end on a newline",
			before: "Host web1\n    HostName 10.0.0.1\n\nHost web2\n    HostName 10.0.0.2",
			want:   "Host web1\n    HostName 10.0.0.1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertRemoved(t, tc.before, "web2", tc.want)
		})
	}
}

// A Host line naming several aliases is hand-written — ohmyssh writes one alias
// per block — and the block belongs to the aliases that are still there, so the
// line loses the one name and keeps everything else, indentation included.
func TestRemoveHostDropsOneAliasFromALineNamingSeveral(t *testing.T) {
	cases := []struct {
		name   string
		before string
		want   string
	}{
		{
			name:   "two aliases",
			before: "Host web1 web2\n    HostName 10.0.0.2\n    User deploy\n",
			want:   "Host web1\n    HostName 10.0.0.2\n    User deploy\n",
		},
		{
			name:   "an indented line",
			before: "  Host web1 web2\n    HostName 10.0.0.2\n",
			want:   "  Host web1\n    HostName 10.0.0.2\n",
		},
		{
			name:   "the alias in the middle",
			before: "Host web1 web2 web3\n    HostName 10.0.0.2\n",
			want:   "Host web1 web3\n    HostName 10.0.0.2\n",
		},
		{
			// Every mention goes: a line naming the same alias twice would keep the
			// host alive on the strength of the second one.
			name:   "the alias named twice",
			before: "Host web1 web2 web2\n    HostName 10.0.0.2\n",
			want:   "Host web1\n    HostName 10.0.0.2\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertRemoved(t, tc.before, "web2", tc.want)

			// The alias that stayed kept the block with it, directives and all.
			path := writeFile(t, t.TempDir(), "hosts", tc.before)
			if err := RemoveHost(path, "web2"); err != nil {
				t.Fatalf("RemoveHost: %v", err)
			}
			hosts, err := ParseSSHConfigFile(path)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := names(hosts); !slices.Contains(got, "web1") {
				t.Fatalf("hosts = %v, want web1 to still be one", got)
			}
			if got := hostNamed(t, hosts, "web1").Hostname; got != "10.0.0.2" {
				t.Errorf("web1 resolves to %q, want the HostName of the block it kept", got)
			}
		})
	}
}

// Rebuilding a Host line drops anything on it that is not a bare alias — quotes,
// a trailing comment — so a line that has either is refused and the file is
// named instead. Only a hand-edited file can hold one, and only its author can
// say what it meant.
func TestRemoveHostRefusesALineItWouldHaveToRewrite(t *testing.T) {
	for _, line := range []string{`Host "web1" web2`, "Host web1 web2  # keep these together"} {
		t.Run(line, func(t *testing.T) {
			before := line + "\n    HostName 10.0.0.2\n"
			path := writeFile(t, t.TempDir(), "hosts", before)

			err := RemoveHost(path, "web2")
			if err == nil {
				t.Fatal("RemoveHost rewrote a line it cannot rebuild")
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("error = %v, want it to name %s", err, path)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if string(after) != before {
				t.Errorf("the file changed on a refusal:\n%s", after)
			}
		})
	}
}

// The same alias in two blocks is possible by hand, and removing one of them
// would leave the host still resolving, still on the list. Both go.
func TestRemoveHostRemovesEveryBlockNamingTheAlias(t *testing.T) {
	assertRemoved(t, `# Tags: prod
Host web2
    HostName 10.0.0.2

Host web1
    HostName 10.0.0.1

Host web2
    HostName 10.0.0.9
`, "web2", "Host web1\n    HostName 10.0.0.1\n")
}

// A block runs to the next Host or Match directive. What follows a Match belongs
// to it, so a delete that ran to the end of the file would take the user's
// conditional settings with it.
func TestRemoveHostStopsAtTheNextDirective(t *testing.T) {
	assertRemoved(t, `Host web2
    HostName 10.0.0.2

Host web1
    HostName 10.0.0.1

Match host *.internal
    User deploy

Host web3
    HostName 10.0.0.3
`, "web2", `Host web1
    HostName 10.0.0.1

Match host *.internal
    User deploy

Host web3
    HostName 10.0.0.3
`)
}

// The keyword is matched the way the parser matches it, whatever case the file
// happens to be written in.
func TestRemoveHostMatchesTheKeywordWhateverItsCase(t *testing.T) {
	for _, keyword := range []string{"Host", "host", "HOST"} {
		t.Run(keyword, func(t *testing.T) {
			assertRemoved(t,
				keyword+" web1\n    HostName 10.0.0.1\n\nHost web2\n    HostName 10.0.0.2\n",
				"web1",
				"Host web2\n    HostName 10.0.0.2\n")
		})
	}
}

// A file written on Windows keeps its line endings: the edits are line based and
// the "\r" at the end of a line is part of that line, not something to normalise
// away on the way through.
func TestRemoveHostKeepsTheLineEndings(t *testing.T) {
	assertRemoved(t,
		"Host web1\r\n    HostName 10.0.0.1\r\n\r\nHost web2\r\n    HostName 10.0.0.2\r\n",
		"web2",
		"Host web1\r\n    HostName 10.0.0.1\r\n")
}

// A host that is not in the file is a failure with the file's name in it, not a
// quiet success, and nothing on disk changes.
func TestRemoveHostWithoutTheHostNamesTheFile(t *testing.T) {
	const before = "Host web1\n    HostName 10.0.0.1\n"
	path := writeFile(t, t.TempDir(), "hosts", before)

	err := RemoveHost(path, "nosuchhost")
	if err == nil {
		t.Fatal("RemoveHost reported success for a host that is not in the file")
	}
	if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "nosuchhost") {
		t.Errorf("error = %v, want it to name the host and %s", err, path)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(after) != before {
		t.Errorf("the file changed on a failed delete:\n%s", after)
	}
}

// A hosts file that is not there at all is an error too: there is nothing to
// delete from, and creating an empty file would only hide that.
func TestRemoveHostFromAMissingFileIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "hosts")

	if err := RemoveHost(path, "web1"); err == nil {
		t.Fatal("RemoveHost reported success for a file that does not exist")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a file was created at %s", path)
	}
	if err := RemoveHost(writeFile(t, t.TempDir(), "hosts", "Host web1\n"), ""); err == nil {
		t.Error("RemoveHost accepted an empty alias")
	}
}

// A hosts file kept in a dotfiles repository and linked into place has to stay a
// link: a rename over the top would replace it with a real file and stop the
// repository from seeing what happens to it afterwards.
func TestRemoveHostFollowsASymlink(t *testing.T) {
	dir := t.TempDir()
	real := writeFile(t, dir, "real-hosts", "Host web1\n    HostName 10.0.0.1\n\nHost web2\n    HostName 10.0.0.2\n")
	link := filepath.Join(dir, "hosts")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if err := RemoveHost(link, "web2"); err != nil {
		t.Fatalf("RemoveHost: %v", err)
	}

	if info, err := os.Lstat(link); err != nil {
		t.Fatalf("lstat: %v", err)
	} else if info.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced by a regular file")
	}
	data, err := os.ReadFile(real)
	if err != nil {
		t.Fatalf("read the link target: %v", err)
	}
	if want := "Host web1\n    HostName 10.0.0.1\n"; string(data) != want {
		t.Errorf("the link's target holds\n%q\nwant\n%q", data, want)
	}
}

// Nothing partial is left behind: a temporary file beside the hosts file would
// be picked up by a glob-based Include, and one that fails to parse takes the
// whole config with it.
func TestRemoveHostLeavesNoTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts")
	for _, alias := range []string{"web1", "web2"} {
		if err := AddHost(path, NewHost{Alias: alias, Host: "10.0.0.1"}); err != nil {
			t.Fatalf("AddHost %s: %v", alias, err)
		}
	}
	if err := RemoveHost(path, "web2"); err != nil {
		t.Fatalf("RemoveHost: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "hosts" {
		var found []string
		for _, e := range entries {
			found = append(found, e.Name())
		}
		t.Errorf("directory holds %v, want just the hosts file", found)
	}
}
