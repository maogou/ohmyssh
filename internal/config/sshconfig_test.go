package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeConfig writes body to a temp file and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// findByAlias returns the resolved host with the given alias.
func findByAlias(t *testing.T, hosts []SSHHost, alias string) SSHHost {
	t.Helper()
	for _, h := range hosts {
		if h.Name == alias {
			return h
		}
	}
	t.Fatalf("alias %q not found in %v", alias, hostNames(hosts))
	return SSHHost{}
}

func hostNames(hosts []SSHHost) []string {
	names := make([]string, 0, len(hosts))
	for _, h := range hosts {
		names = append(names, h.Name)
	}
	return names
}

func TestParseResolvesHostFields(t *testing.T) {
	path := writeConfig(t, `
Host web1
    HostName 10.0.0.5
    User deploy
    Port 2222
    IdentityFile ~/.ssh/deploy_ed25519
`)

	hosts, err := ParseSSHConfigFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(hosts) != 1 {
		t.Fatalf("got %d hosts, want 1: %v", len(hosts), hostNames(hosts))
	}

	h := hosts[0]
	if h.Name != "web1" || h.Hostname != "10.0.0.5" || h.User != "deploy" || h.Port != "2222" {
		t.Errorf("unexpected host: %+v", h)
	}
	if want := "deploy@10.0.0.5:2222"; h.String() != want {
		t.Errorf("String() = %q, want %q", h.String(), want)
	}
	if want := "10.0.0.5:2222"; h.Addr() != want {
		t.Errorf("Addr() = %q, want %q", h.Addr(), want)
	}
	if h.LineNumber != 2 {
		t.Errorf("LineNumber = %d, want 2", h.LineNumber)
	}
}

func TestHostNameDefaultsToAlias(t *testing.T) {
	path := writeConfig(t, "Host bare\n    User root\n")

	h := findByAlias(t, mustParse(t, path), "bare")
	if h.Hostname != "bare" {
		t.Errorf("Hostname = %q, want %q", h.Hostname, "bare")
	}
	if h.Addr() != "bare:22" {
		t.Errorf("Addr() = %q, want %q", h.Addr(), "bare:22")
	}
}

// An address is built the same way wherever one is needed — the dial target and
// the known_hosts lookup both go through this — so an IPv6 literal has to come
// out in the one spelling that reaches the machine.
func TestJoinHostPortBracketsAnIPv6LiteralOnce(t *testing.T) {
	tests := []struct {
		name string
		host string
		port string
		want string
	}{
		{"a name", "web1", "2222", "web1:2222"},
		{"no port", "web1", "", "web1:22"},
		{"a bare IPv6 literal", "2606:4700::1111", "22", "[2606:4700::1111]:22"},
		// ssh config allows either spelling, and net.JoinHostPort would bracket
		// this one a second time.
		{"an IPv6 literal already bracketed", "[2606:4700::1111]", "2222", "[2606:4700::1111]:2222"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := JoinHostPort(tt.host, tt.port); got != tt.want {
				t.Errorf("JoinHostPort(%q, %q) = %q, want %q", tt.host, tt.port, got, tt.want)
			}
		})
	}
}

// OpenSSH uses the first value obtained for any parameter, so a trailing
// "Host *" block supplies defaults without overriding earlier blocks.
func TestFirstValueWinsAcrossBlocks(t *testing.T) {
	path := writeConfig(t, `
Host web1
    HostName web1.internal
    User deploy

Host *
    User fallback
    Port 2200
    IdentityFile ~/.ssh/id_ed25519
`)

	h := findByAlias(t, mustParse(t, path), "web1")
	if h.User != "deploy" {
		t.Errorf("User = %q, want %q (Host * must not override)", h.User, "deploy")
	}
	if h.Port != "2200" {
		t.Errorf("Port = %q, want %q (default from Host *)", h.Port, "2200")
	}
	if h.Identity == "" {
		t.Error("IdentityFile from Host * was not applied as a default")
	}
}

// A "Host *" block placed first must still lose to a later specific block,
// because resolution is order-sensitive per parameter.
func TestLeadingCatchAllIsOverridden(t *testing.T) {
	path := writeConfig(t, `
Host *
    User default_user

Host db1
    User dbadmin
    HostName db1.example.com
`)

	h := findByAlias(t, mustParse(t, path), "db1")
	if h.User != "default_user" {
		t.Errorf("User = %q, want %q: a leading Host * wins under first-value-wins",
			h.User, "default_user")
	}
}

func TestWildcardsAreNotConnectableHosts(t *testing.T) {
	path := writeConfig(t, `
Host *.example.com
    User edge

Host *.internal !secret.internal
    User internal

Host !excluded
    User nobody

Host real
    HostName real.example.com
`)

	hosts := mustParse(t, path)
	for _, h := range hosts {
		if h.Name != "real" {
			t.Errorf("unexpected connectable host %q; wildcard/negated patterns must not be listed", h.Name)
		}
	}
}

func TestNegationVetoesMatchingBlock(t *testing.T) {
	path := writeConfig(t, `
Host *.example.com !skip.example.com
    User edge

Host skip.example.com
    User skipper
`)

	// skip.example.com is vetoed from the first block but named by the second,
	// so it is still a valid host with the second block's settings.
	h := findByAlias(t, mustParse(t, path), "skip.example.com")
	if h.User != "skipper" {
		t.Errorf("User = %q, want %q", h.User, "skipper")
	}
}

func TestMultipleAliasesOnOneLine(t *testing.T) {
	path := writeConfig(t, "Host a1 a2 a3\n    HostName shared.example.com\n")

	hosts := mustParse(t, path)
	if len(hosts) != 3 {
		t.Fatalf("got %d hosts, want 3: %v", len(hosts), hostNames(hosts))
	}
	for _, alias := range []string{"a1", "a2", "a3"} {
		h := findByAlias(t, hosts, alias)
		if h.Hostname != "shared.example.com" {
			t.Errorf("%s: Hostname = %q, want shared.example.com", alias, h.Hostname)
		}
	}
}

func TestTagsFromComment(t *testing.T) {
	path := writeConfig(t, `
# Tags: prod, web
Host web1
    HostName web1.example.com

#tags: db
Host db1
    HostName db1.example.com

Host untagged
    HostName plain.example.com
`)

	hosts := mustParse(t, path)

	web := findByAlias(t, hosts, "web1")
	if len(web.Tags) != 2 || web.Tags[0] != "prod" || web.Tags[1] != "web" {
		t.Errorf("web1 tags = %v, want [prod web]", web.Tags)
	}

	db := findByAlias(t, hosts, "db1")
	if len(db.Tags) != 1 || db.Tags[0] != "db" {
		t.Errorf("db1 tags = %v, want [db]", db.Tags)
	}

	if len(findByAlias(t, hosts, "untagged").Tags) != 0 {
		t.Error("untagged host gained tags")
	}
}

func TestCommentsAndQuoting(t *testing.T) {
	path := writeConfig(t, `
# a leading comment
Host quoted
    HostName "spaced.example.com"   # trailing comment
    User "user name"
    ProxyCommand ssh -W %h:%p bastion.example.com#notacomment

Host #notahost
    User ignored
`)

	hosts := mustParse(t, path)
	h := findByAlias(t, hosts, "quoted")

	if h.Hostname != "spaced.example.com" {
		t.Errorf("Hostname = %q, want spaced.example.com", h.Hostname)
	}
	if h.User != "user name" {
		t.Errorf("User = %q, want %q", h.User, "user name")
	}
	// A '#' only starts a comment at a token boundary; this one is inside a value.
	if h.ProxyCommand == "" {
		t.Error("ProxyCommand was truncated at an embedded '#'")
	}
}

func TestEqualToSeparator(t *testing.T) {
	path := writeConfig(t, "Host eq\n  HostName=eq.example.com\n  User=equser\n  Port = 2022\n")

	h := findByAlias(t, mustParse(t, path), "eq")
	if h.Hostname != "eq.example.com" || h.User != "equser" || h.Port != "2022" {
		t.Errorf("unexpected host: %+v", h)
	}
}

func TestIncludePullsInAnotherFile(t *testing.T) {
	dir := t.TempDir()
	included := filepath.Join(dir, "conf.d")
	if err := os.MkdirAll(included, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(included, "extra"),
		[]byte("Host frominclude\n    HostName inc.example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	main := filepath.Join(dir, "config")
	body := "Include " + filepath.Join(included, "*") + "\n\nHost local\n    HostName local.example.com\n"
	if err := os.WriteFile(main, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	hosts, err := ParseSSHConfigFile(main)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(hosts) != 2 {
		t.Fatalf("got %v, want [frominclude local]", hostNames(hosts))
	}
	if h := findByAlias(t, hosts, "frominclude"); h.Hostname != "inc.example.com" {
		t.Errorf("Hostname = %q, want inc.example.com", h.Hostname)
	}
}

// An Include is spliced in where the line is, not hoisted above the block that
// asked for it. Left hoisted, a defaults file holding `Host *` comes out ahead
// of the specific block, and first-value-wins hands the host the default's User,
// Port and ProxyJump instead of its own — which is how a config that ssh reads
// correctly gets ohmyssh authenticating as the wrong account, or tunnelling
// through the wrong jump host, with nothing said about it.
func TestIncludeKeepsTheEnclosingBlockAheadOfItself(t *testing.T) {
	dir := t.TempDir()
	defaults := filepath.Join(dir, "defaults")
	body := "Host *\n    User frominc\n    Port 2200\n    ProxyJump jumpinc\n"
	if err := os.WriteFile(defaults, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	main := filepath.Join(dir, "config")
	// The shape the README's own rules are written for: a specific block, an
	// Include of a conf.d defaults file, and the rest of the config below it.
	body = "Host web1\n" +
		"    HostName 10.0.0.5\n" +
		"    User explicit\n" +
		"    Port 2222\n" +
		"    ProxyJump jumpown\n" +
		"Include " + defaults + "\n" +
		"Host web2\n" +
		"    HostName 10.0.0.6\n"
	if err := os.WriteFile(main, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	hosts, err := ParseSSHConfigFile(main)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Checked against ssh -G, which resolves this same file to
	// "user explicit / port 2222 / proxyjump jumpown".
	h := findByAlias(t, hosts, "web1")
	if h.User != "explicit" {
		t.Errorf("User = %q, want explicit: the Included Host * overrode the block that included it", h.User)
	}
	if h.Port != "2222" {
		t.Errorf("Port = %q, want 2222", h.Port)
	}
	if h.ProxyJump != "jumpown" {
		t.Errorf("ProxyJump = %q, want jumpown", h.ProxyJump)
	}

	// The directives after the Include still belong to web1: reading the
	// included file does not end the block it was read inside.
	if h.Hostname != "10.0.0.5" {
		t.Errorf("Hostname = %q, want 10.0.0.5", h.Hostname)
	}

	// web2 is a host the enclosing block does not cover, so ssh never applies
	// the file to it and the defaults must leave it alone. ssh -G reports
	// "user lelesky / port 22" and no proxyjump for web2 in this same file.
	web2 := findByAlias(t, hosts, "web2")
	if web2.User != "" || web2.Port != "" || web2.ProxyJump != "" {
		t.Errorf("web2 = %+v, want the Included defaults to stay with web1", web2)
	}
}

// A directive written after the Include belongs to the enclosing block, and
// still lands after it in file order rather than jumping the queue.
func TestDirectivesAfterAnIncludeStayWithTheirBlock(t *testing.T) {
	dir := t.TempDir()
	included := filepath.Join(dir, "inc")
	if err := os.WriteFile(included, []byte("Host *\n    Port 2200\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	main := filepath.Join(dir, "config")
	body := "Host web1\n" +
		"    User before\n" +
		"Include " + included + "\n" +
		"    User after\n"
	if err := os.WriteFile(main, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	hosts, err := ParseSSHConfigFile(main)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// "User before" is read first and wins; "User after" is later in the file
	// than the Included block, so it loses to nothing but does not displace it.
	h := findByAlias(t, hosts, "web1")
	if h.User != "before" {
		t.Errorf("User = %q, want before", h.User)
	}
	if h.Port != "2200" {
		t.Errorf("Port = %q, want 2200 from the Included block", h.Port)
	}
}

// An Include inside a Host block is conditional: ssh reads the named file when
// that block matches the host it was asked for, and passes over the file when it
// does not. What the file holds therefore reaches web1 — the host its enclosing
// block is written for — and stops there.
//
// Checked against ssh -G, which resolves web1 to "user explicit / port 2222 /
// proxyjump jumpown" and web2 to the User its own block gives it, with no port
// and no jump of its own to report.
func TestIncludeAppliesOnlyToTheHostsItsEnclosingBlockCovers(t *testing.T) {
	dir := t.TempDir()
	defaults := filepath.Join(dir, "defaults")
	body := "Host *\n    User frominc\n    Port 2200\n    ProxyJump jumpinc\n"
	if err := os.WriteFile(defaults, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	main := filepath.Join(dir, "config")
	body = "Host web1\n" +
		"    User explicit\n" +
		"    Port 2222\n" +
		"    ProxyJump jumpown\n" +
		"Include " + defaults + "\n" +
		"Host web2\n" +
		"    User other\n"
	if err := os.WriteFile(main, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	hosts, err := ParseSSHConfigFile(main)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	h := findByAlias(t, hosts, "web1")
	if h.User != "explicit" || h.Port != "2222" || h.ProxyJump != "jumpown" {
		t.Errorf("web1 = %+v, want its own explicit/2222/jumpown", h)
	}

	web2 := findByAlias(t, hosts, "web2")
	if web2.User != "other" || web2.Port != "" || web2.ProxyJump != "" {
		t.Errorf("web2 = %+v, want other with nothing from the Included defaults", web2)
	}
}

// A file Included inside a Host block hands its leading directives — the ones
// before its first Host line — to that block, the way ssh reads them in the
// block's scope. They are as conditional as the rest of the file.
//
// Checked against ssh -G: web1 reports "port 9999" and web2, which the enclosing
// block does not cover, reports port 22.
func TestIncludeOfLeadingDirectivesStaysWithTheEnclosingBlock(t *testing.T) {
	dir := t.TempDir()
	fragment := filepath.Join(dir, "port")
	if err := os.WriteFile(fragment, []byte("Port 9999\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	main := filepath.Join(dir, "config")
	body := "Host web1\n" +
		"    Include " + fragment + "\n" +
		"Host web2\n" +
		"    User other\n"
	if err := os.WriteFile(main, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	hosts, err := ParseSSHConfigFile(main)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if h := findByAlias(t, hosts, "web1"); h.Port != "9999" {
		t.Errorf("web1 Port = %q, want 9999 from the Included fragment", h.Port)
	}
	if web2 := findByAlias(t, hosts, "web2"); web2.Port != "" {
		t.Errorf("web2 Port = %q, want none: the fragment belongs to web1's block", web2.Port)
	}
}

// The same condition decides which names exist. ssh resolves the config for the
// host it was asked for, so a Host line inside an Include is reached only where
// the block around the Include covers that host: corp1.corp.example.com is
// defined because the wildcard does cover it, and other1 is not, however plainly
// the included file names it.
//
// Checked against ssh -G: corp1.corp.example.com resolves to "user frominc",
// other1 to the default user with nothing from the file.
func TestIncludeDefinesAliasesOnlyUnderItsEnclosingBlock(t *testing.T) {
	dir := t.TempDir()
	corp := filepath.Join(dir, "corp")
	body := "Host corp1.corp.example.com\n    User frominc\n" +
		"Host other1\n    User other\n"
	if err := os.WriteFile(corp, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	main := filepath.Join(dir, "config")
	if err := os.WriteFile(main, []byte("Host *.corp.example.com\n    Include "+corp+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	hosts, err := ParseSSHConfigFile(main)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := hostNames(hosts); len(got) != 1 || got[0] != "corp1.corp.example.com" {
		t.Errorf("hosts = %v, want only corp1.corp.example.com: other1 is defined nowhere its Include applies", got)
	}
	if h := findByAlias(t, hosts, "corp1.corp.example.com"); h.User != "frominc" {
		t.Errorf("User = %q, want frominc", h.User)
	}

	// The same lookup the user reaches by typing the name: a wildcard block
	// covers a host the list does not offer, and must not cover one the file
	// only defines where that wildcard does not reach.
	if _, found, err := ResolveName("other1", main); err != nil {
		t.Fatalf("resolve other1: %v", err)
	} else if found {
		t.Error("other1 resolved through a file its enclosing block does not cover")
	}
	if h, found, err := ResolveName("corp1.corp.example.com", main); err != nil {
		t.Fatalf("resolve corp1: %v", err)
	} else if !found || h.User != "frominc" {
		t.Errorf("corp1 = %+v found=%v, want it resolved with User frominc", h, found)
	}
}

// A wildcard block that Includes a file of defaults passes them on to the hosts
// it covers, which is the shape a conf.d directory usually takes.
//
// Checked against ssh -G: corp1.corp.example.com reports "user frominc / port
// 2200", and web1 keeps the default user and port 22.
func TestIncludeBehindAWildcardBlockAppliesWhereTheWildcardMatches(t *testing.T) {
	dir := t.TempDir()
	defaults := filepath.Join(dir, "defaults")
	body := "Host *\n    User frominc\n    Port 2200\n"
	if err := os.WriteFile(defaults, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	main := filepath.Join(dir, "config")
	if err := os.WriteFile(main, []byte("Host *.corp.example.com\n    Include "+defaults+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	h, found, err := ResolveName("corp1.corp.example.com", main)
	if err != nil {
		t.Fatalf("resolve corp1: %v", err)
	}
	if !found || h.User != "frominc" || h.Port != "2200" {
		t.Errorf("corp1 = %+v found=%v, want frominc on port 2200", h, found)
	}

	if h, found, err := ResolveName("web1", main); err != nil {
		t.Fatalf("resolve web1: %v", err)
	} else if found {
		t.Errorf("web1 = %+v, want it left undefined: the wildcard does not cover it", h)
	}
}

// Includes nest, and every Host block one is read inside stays a condition on
// the files below it. Here the outer block covers a, the `Host *` inside the
// first file covers it too, and only then does the innermost file apply: a takes
// its leading Port, while the Host line it also carries for web1 stays inert,
// since the outermost block is not about web1.
//
// Checked against ssh -G: a reports "port 7777" with the default user, and web1
// reports port 22.
func TestNestedIncludesAreConditionalAtEveryLevel(t *testing.T) {
	dir := t.TempDir()
	inner := filepath.Join(dir, "inner")
	if err := os.WriteFile(inner, []byte("Port 7777\nHost web1\n    User inner\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outer := filepath.Join(dir, "outer")
	if err := os.WriteFile(outer, []byte("Host *\n    Include "+inner+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "config")
	if err := os.WriteFile(main, []byte("Host a\n    Include "+outer+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	h, found, err := ResolveName("a", main)
	if err != nil {
		t.Fatalf("resolve a: %v", err)
	}
	if !found || h.Port != "7777" {
		t.Errorf("a = %+v found=%v, want port 7777 from the nested include", h, found)
	}

	hosts, err := ParseSSHConfigFile(main)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := hostNames(hosts); len(got) != 1 || got[0] != "a" {
		t.Errorf("hosts = %v, want only a: an alias two Includes deep is defined only where both enclosing blocks reach it", got)
	}
	if h, found, err := ResolveName("web1", main); err != nil {
		t.Fatalf("resolve web1: %v", err)
	} else if found {
		t.Errorf("web1 = %+v, want it left undefined: no enclosing block covers it", h)
	}
}

func TestProxySettingsAreCaptured(t *testing.T) {
	path := writeConfig(t, `
Host behind
    HostName behind.example.com
    ProxyJump bastion
    IdentityAgent ~/.ssh/agent.sock
`)

	h := findByAlias(t, mustParse(t, path), "behind")
	if h.ProxyJump != "bastion" {
		t.Errorf("ProxyJump = %q, want bastion", h.ProxyJump)
	}
	if h.IdentityAgent != "~/.ssh/agent.sock" {
		t.Errorf("IdentityAgent = %q", h.IdentityAgent)
	}
}

func TestMissingFileYieldsNoHosts(t *testing.T) {
	hosts, err := ParseSSHConfigFile(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if len(hosts) != 0 {
		t.Errorf("got %d hosts, want 0", len(hosts))
	}
}

func TestMatchBlocksDoNotLeakDirectives(t *testing.T) {
	path := writeConfig(t, `
Host real
    HostName real.example.com

Match host *.internal
    User matched
`)

	h := findByAlias(t, mustParse(t, path), "real")
	if h.User != "" {
		t.Errorf("User = %q, want empty: directives inside Match must not apply", h.User)
	}
}

func TestMatchPattern(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"*", "anything", true},
		{"web1", "web1", true},
		{"web1", "web2", false},
		{"web*", "web1", true},
		{"web*", "db1", false},
		{"*.example.com", "a.example.com", true},
		{"*.example.com", "example.com", false},
		{"web?", "web1", true},
		{"web?", "web12", false},
		{"*a*b*", "xxayybzz", true},
		{"*a*b*", "xxayyzz", false},
		{"a*c", "abbbc", true},
		{"a*c", "abbbd", false},
	}
	for _, tc := range cases {
		if got := matchPattern(tc.pattern, tc.name); got != tc.want {
			t.Errorf("matchPattern(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got, want := ExpandPath("~/.ssh/id_ed25519"), filepath.Join(home, ".ssh/id_ed25519"); got != want {
		t.Errorf("ExpandPath(~/.ssh/id_ed25519) = %q, want %q", got, want)
	}
	// Relative paths resolve against ~/.ssh, as OpenSSH does.
	if got, want := ExpandPath("id_rsa"), filepath.Join(home, ".ssh/id_rsa"); got != want {
		t.Errorf("ExpandPath(id_rsa) = %q, want %q", got, want)
	}
	if got := ExpandPath("/absolute/key"); got != "/absolute/key" {
		t.Errorf("ExpandPath(/absolute/key) = %q, want unchanged", got)
	}
}

func mustParse(t *testing.T, path string) []SSHHost {
	t.Helper()
	hosts, err := ParseSSHConfigFile(path)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return hosts
}
