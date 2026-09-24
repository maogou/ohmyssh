package repository

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/maogou/ohmyssh/internal/config"
)

func writeTestConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestFindFromConfigAlias(t *testing.T) {
	hosts := NewHosts("")
	path := writeTestConfig(t, `
Host web1
    HostName 10.0.0.5
    User deploy
    Port 2222
`)

	all, err := hosts.List(path)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	host, err := hosts.Find("", all, "web1")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if host.Hostname != "10.0.0.5" || host.User != "deploy" || host.Port != "2222" {
		t.Errorf("unexpected host: %+v", host)
	}
	if got, want := host.Addr(), "10.0.0.5:2222"; got != want {
		t.Errorf("Addr() = %q, want %q", got, want)
	}
}

func TestFindUnknownAliasSuggestsNearMatch(t *testing.T) {
	hosts := NewHosts("")
	path := writeTestConfig(t, "Host production\n    HostName prod.example.com\n")

	all, err := hosts.List(path)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	_, err = hosts.Find("", all, "prod")
	if err == nil {
		t.Fatal("expected an error for an unknown alias")
	}
	// The message should hint at the real alias rather than only failing.
	if got := err.Error(); !strings.Contains(got, "production") {
		t.Errorf("error %q does not suggest the near match", got)
	}
}

// A name the config defines only by wildcard is a host like any other: ssh
// resolves web1.corp.example.com through "Host *.corp.example.com", and the
// ProxyJump and User in that block are what make the machine reachable at all.
// The wildcard block is still not offered as a browsable host — it names a
// pattern, not a machine — so the resolution has to happen here instead.
func TestFindResolvesHostDefinedOnlyByWildcard(t *testing.T) {
	hosts := NewHosts("")
	path := writeTestConfig(t, `
Host *.corp.example.com
    User deploy
    ProxyJump bastion
    Port 2200
Host bastion
    HostName 10.0.0.1
`)

	all, err := hosts.List(path)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// The wildcard block is not browsable, so the name is not in the list and
	// the lookup has to reach past it.
	if slices.Contains(hostNames(all), "web1.corp.example.com") {
		t.Fatalf("a wildcard block was offered as a browsable host: %v", hostNames(all))
	}

	host, err := hosts.Find(path, all, "web1.corp.example.com")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if host.User != "deploy" || host.Port != "2200" {
		t.Errorf("host = %+v, want the wildcard block's User and Port", host)
	}
	if host.ProxyJump != "bastion" {
		t.Errorf("ProxyJump = %q, want bastion: the block's routing was dropped", host.ProxyJump)
	}
	if got, want := host.Addr(), "web1.corp.example.com:2200"; got != want {
		t.Errorf("Addr() = %q, want %q", got, want)
	}
}

// A bare "Host *" defines nothing: it is the defaults every host receives, and
// treating it as a definition would make every mistyped alias resolve to a host
// that does not exist. The suggestion is the more useful answer there.
func TestFindStillReportsAMistypedAliasDespiteACatchAll(t *testing.T) {
	hosts := NewHosts("")
	path := writeTestConfig(t, `
Host *
    User default-user
Host production
    HostName prod.example.com
`)

	all, err := hosts.List(path)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	_, err = hosts.Find(path, all, "prod")
	if err == nil {
		t.Fatal("a catch-all block made a mistyped alias resolve")
	}
	if got := err.Error(); !strings.Contains(got, "production") {
		t.Errorf("error %q does not suggest the near match", got)
	}
}

// hostNames is the aliases in a listed set, for assertions about what is offered.
func hostNames(all []config.SSHHost) []string {
	names := make([]string, 0, len(all))
	for _, h := range all {
		names = append(names, h.Name)
	}
	return names
}

// An explicit user@host bypasses the config file entirely.
func TestFindAcceptsAdHocTargets(t *testing.T) {
	cases := []struct {
		input    string
		wantUser string
		wantHost string
		wantPort string
	}{
		{"root@example.com", "root", "example.com", ""},
		{"root@example.com:2222", "root", "example.com", "2222"},
		{"example.com:2200", "", "example.com", "2200"},
	}

	hosts := NewHosts("")
	path := writeTestConfig(t, "Host unrelated\n    HostName nowhere\n")
	all, err := hosts.List(path)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	for _, tc := range cases {
		host, err := hosts.Find("", all, tc.input)
		if err != nil {
			t.Errorf("Find(%q): %v", tc.input, err)
			continue
		}
		if host.User != tc.wantUser || host.Hostname != tc.wantHost || host.Port != tc.wantPort {
			t.Errorf("Find(%q) = %+v, want user=%q host=%q port=%q",
				tc.input, host, tc.wantUser, tc.wantHost, tc.wantPort)
		}
		// With no user given, the local username is used, exactly as ssh does.
		want := displayUserFor(tc.wantUser) + "@" + tc.wantHost + ":" + portOr22(tc.wantPort)
		if got := host.String(); got != want {
			t.Errorf("String() for %q = %q, want %q", tc.input, got, want)
		}
	}
}

// displayUserFor mirrors the ssh convention this package follows: an explicit
// user wins, otherwise $USER, otherwise root.
func displayUserFor(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "root"
}

func portOr22(port string) string {
	if port == "" {
		return "22"
	}
	return port
}

func TestListMissingConfigIsNotFatal(t *testing.T) {
	hosts, err := NewHosts("").List(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("a missing config file should yield no hosts, got %v", err)
	}
	if len(hosts) != 0 {
		t.Errorf("got %d hosts, want 0", len(hosts))
	}
}

// names is the aliases of a host list, which is what the merge tests are asking
// about: whether a host from either file is in the list at all.
func names(hosts []config.SSHHost) []string {
	out := make([]string, len(hosts))
	for i, h := range hosts {
		out[i] = h.Name
	}
	return out
}

// The ssh config is read, and the file ohmyssh keeps its own hosts in is read
// with it. Without the second file, a host added from the browser would be in a
// list the browser never shows again once it restarts.
func TestListReadsBothFiles(t *testing.T) {
	dir := t.TempDir()
	sshConfig := writeTestConfig(t, "Host web1\n    HostName 10.0.0.1\n")
	extra := filepath.Join(dir, "hosts")
	if err := config.AddHost(extra, config.NewHost{Alias: "web2", Host: "10.0.0.2"}); err != nil {
		t.Fatalf("add host: %v", err)
	}

	hosts, err := NewHosts(extra).List(sshConfig)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if got, want := names(hosts), []string{"web1", "web2"}; !slices.Equal(got, want) {
		t.Errorf("hosts = %v, want %v", got, want)
	}
	if got := hostNamed(t, hosts, "web2"); got.SourceFile != extra {
		t.Errorf("web2 comes from %q, want %q — the header says which file a host is in", got.SourceFile, extra)
	}
}

// A browser that was never told where to write hosts reads the ssh config alone:
// listing hosts must not reach outside what the caller asked for.
func TestListWithoutAnExtraFileReadsTheSSHConfigAlone(t *testing.T) {
	sshConfig := writeTestConfig(t, "Host web1\n    HostName 10.0.0.1\n")

	hosts, err := NewHosts("").List(sshConfig)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if got, want := names(hosts), []string{"web1"}; !slices.Equal(got, want) {
		t.Errorf("hosts = %v, want %v", got, want)
	}
}

// What is added is what is read back, through the same door the browser uses.
func TestAddIsReadBackByList(t *testing.T) {
	sshConfig := writeTestConfig(t, "Host web1\n    HostName 10.0.0.1\n")
	extra := filepath.Join(t.TempDir(), ".ohmyssh", "hosts")
	hosts := NewHosts(extra)

	if got := hosts.Path(); got != extra {
		t.Errorf("Path() = %q, want %q", got, extra)
	}
	if err := hosts.Add(config.NewHost{Alias: "web2", Host: "10.0.0.2", User: "deploy", Port: "2222"}); err != nil {
		t.Fatalf("add: %v", err)
	}

	listed, err := hosts.List(sshConfig)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	web2 := hostNamed(t, listed, "web2")
	if web2.Hostname != "10.0.0.2" || web2.User != "deploy" || web2.Port != "2222" {
		t.Errorf("read back %+v, want the host that was added", web2)
	}
}

// Writing needs somewhere to write: a browser with no extra file says so rather
// than quietly writing nothing.
func TestAddWithoutAFileIsRefused(t *testing.T) {
	if err := NewHosts("").Add(config.NewHost{Alias: "web2"}); err == nil {
		t.Error("Add with no file configured succeeded")
	}
}

// A host added through ohmyssh can be taken back out again, and the list reads
// the file after the delete the way the browser will.
func TestRemoveIsNotReadBackByList(t *testing.T) {
	sshConfig := writeTestConfig(t, "Host web1\n    HostName 10.0.0.1\n")
	extra := filepath.Join(t.TempDir(), ".ohmyssh", "hosts")
	hosts := NewHosts(extra)
	for _, alias := range []string{"web2", "web3"} {
		if err := hosts.Add(config.NewHost{Alias: alias, Host: "10.0.0.2"}); err != nil {
			t.Fatalf("add %s: %v", alias, err)
		}
	}

	if err := hosts.Remove("web2"); err != nil {
		t.Fatalf("remove: %v", err)
	}

	listed, err := hosts.List(sshConfig)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if got, want := names(listed), []string{"web1", "web3"}; !slices.Equal(got, want) {
		t.Errorf("hosts = %v, want %v", got, want)
	}
}

// Deleting needs somewhere to delete from: a browser with no extra file says so
// rather than reporting a host that was never there as gone.
func TestRemoveWithoutAFileIsRefused(t *testing.T) {
	if err := NewHosts("").Remove("web2"); err == nil {
		t.Error("Remove with no file configured succeeded")
	}
}

// A host the ssh config holds is not ohmyssh's to delete, and the file it was
// looked for in says why nothing happened.
func TestRemoveOfAHostThatIsNotThereNamesTheFile(t *testing.T) {
	extra := filepath.Join(t.TempDir(), ".ohmyssh", "hosts")
	hosts := NewHosts(extra)
	if err := hosts.Add(config.NewHost{Alias: "web2"}); err != nil {
		t.Fatalf("add: %v", err)
	}

	err := hosts.Remove("web1")
	if err == nil {
		t.Fatal("Remove reported success for a host that is not in the file")
	}
	if !strings.Contains(err.Error(), extra) {
		t.Errorf("error = %v, want it to name %s", err, extra)
	}
}

func hostNamed(t *testing.T, hosts []config.SSHHost, name string) config.SSHHost {
	t.Helper()
	for _, h := range hosts {
		if h.Name == name {
			return h
		}
	}
	t.Fatalf("no host named %q in %v", name, names(hosts))
	return config.SSHHost{}
}
