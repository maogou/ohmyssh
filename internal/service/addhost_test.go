package service

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/maogou/ohmyssh/internal/config"
	"github.com/maogou/ohmyssh/internal/repository"
)

// These tests cover the one write ohmyssh makes to a file the user owns. Two
// files are involved and both are temporary: the ssh config the user already
// had, which is only read, and the file ohmyssh keeps its own hosts in, which is
// the only thing any of this changes.
type hostFiles struct {
	*connectService
	sshConfig string
	extra     string
}

// newHostFiles returns a service whose hosts live in a directory of the test's
// own, with sshConfig as the user's existing ~/.ssh/config.
func newHostFiles(t *testing.T, sshConfig string) *hostFiles {
	t.Helper()

	dir := t.TempDir()
	a := &hostFiles{
		connectService: newTestConnectWith(t, &fakeTransport{}, repository.NewHosts(filepath.Join(dir, ".ohmyssh", "hosts")), newFakeCredential(nil)),
		sshConfig:      filepath.Join(dir, "config"),
		extra:          filepath.Join(dir, ".ohmyssh", "hosts"),
	}
	if err := os.WriteFile(a.sshConfig, []byte(sshConfig), 0o600); err != nil {
		t.Fatalf("write the ssh config: %v", err)
	}
	return a
}

// add writes a host through the same call the browser's form makes.
func (a *hostFiles) add(host config.NewHost) ([]config.SSHHost, error) {
	return a.AddHost(context.Background(), ConnectOptions{ConfigPath: a.sshConfig}, host)
}

// list reads the hosts back, which is what the browser does on the way in.
func (a *hostFiles) list(t *testing.T) []config.SSHHost {
	t.Helper()

	all, err := a.hosts.List(a.sshConfig)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	return all
}

// What the form collects is written to the file ohmyssh keeps, and the list that
// comes back is the files read again rather than the host appended in memory —
// so the browser shows the new host exactly as the next run of ohmyssh will.
func TestAddHostWritesAndReturnsTheReloadedList(t *testing.T) {
	// A file-scope default, which is the whole reason the two files are parsed as
	// one: without it the host just added has no login name and cannot connect.
	a := newHostFiles(t, "User deploy\n\nHost web1\n    HostName 10.0.0.1\n")

	hosts, err := a.add(config.NewHost{
		Alias: "web2", Host: "10.0.0.2", Tags: []string{"prod", "web"},
	})
	if err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	if got, want := hostNames(hosts), []string{"web1", "web2"}; !slices.Equal(got, want) {
		t.Errorf("returned %v, want %v", got, want)
	}

	web2 := hostIn(t, hosts, "web2")
	if web2.Hostname != "10.0.0.2" || !slices.Equal(web2.Tags, []string{"prod", "web"}) {
		t.Errorf("web2 = %+v, want what the form collected", web2)
	}
	if web2.User != "deploy" {
		t.Errorf("web2.User = %q, want the ssh config's default", web2.User)
	}
	if web2.SourceFile != a.extra {
		t.Errorf("web2 comes from %q, want %q", web2.SourceFile, a.extra)
	}

	// The host is on disk, not only in the returned list: it has to be there for
	// the next run.
	written, err := os.ReadFile(a.extra)
	if err != nil {
		t.Fatalf("read the file it wrote: %v", err)
	}
	if !strings.Contains(string(written), "Host web2") || !strings.Contains(string(written), "HostName 10.0.0.2") {
		t.Errorf("the file holds:\n%s", written)
	}
}

// An alias that is already in use is refused by naming the file it is already
// in, because that is the only thing the user can do about it: the browser
// cannot edit that file, and a second block for the same alias would sit in the
// file looking live while every connection went to the first one.
func TestAddHostRefusesAnAliasThatIsAlreadyTaken(t *testing.T) {
	a := newHostFiles(t, "Host web1\n    HostName 10.0.0.1\n")

	_, err := a.add(config.NewHost{Alias: "web1", Host: "10.0.0.9"})
	if err == nil {
		t.Fatal("an alias already in the ssh config was accepted")
	}
	if !strings.Contains(err.Error(), a.sshConfig) {
		t.Errorf("error %q does not name %s", err, a.sshConfig)
	}
	if _, statErr := os.Stat(a.extra); !os.IsNotExist(statErr) {
		t.Errorf("a refused host was written to %s anyway", a.extra)
	}

	// The same again for an alias already in the file ohmyssh writes, which is
	// what a second attempt at the same host looks like.
	if _, err := a.add(config.NewHost{Alias: "web2", Host: "10.0.0.2"}); err != nil {
		t.Fatalf("add web2: %v", err)
	}
	_, err = a.add(config.NewHost{Alias: "web2", Host: "10.0.0.3"})
	if err == nil {
		t.Fatal("a second host with the same alias was accepted")
	}
	if !strings.Contains(err.Error(), a.extra) {
		t.Errorf("error %q does not name %s", err, a.extra)
	}

	// The refusal changed nothing: the first web2 still resolves, and the file
	// still holds one block for it.
	web2 := hostIn(t, a.list(t), "web2")
	if web2.Hostname != "10.0.0.2" {
		t.Errorf("web2 points at %q, want the host that was accepted", web2.Hostname)
	}
}

// A host the form could not have produced is refused before anything is read:
// the answer is about the field, not about a config file the user did not ask
// about.
func TestAddHostValidatesBeforeItReadsTheConfig(t *testing.T) {
	a := newHostFiles(t, "Host web1\n    HostName 10.0.0.1\n")

	// A config path that cannot be read, under a file rather than a directory, so
	// a read that happened first would fail on its own account.
	_, err := a.AddHost(context.Background(), ConnectOptions{
		ConfigPath: filepath.Join(a.sshConfig, "nested"),
	}, config.NewHost{Host: "10.0.0.2"})
	if err == nil {
		t.Fatal("a host with no alias was accepted")
	}
	if !strings.Contains(err.Error(), "alias") {
		t.Errorf("error = %v, want it to name the field that is wrong", err)
	}
	if _, statErr := os.Stat(a.extra); !os.IsNotExist(statErr) {
		t.Errorf("a refused host was written to %s anyway", a.extra)
	}
}

// A write that fails is reported as itself, with no list: a browser told the add
// succeeded would show a host that is in no file.
func TestAddHostReportsAWriteThatFailed(t *testing.T) {
	a := newHostFiles(t, "Host web1\n    HostName 10.0.0.1\n")

	// A file where the directory the host file lives in should be.
	if err := os.WriteFile(filepath.Dir(a.extra), []byte("not a directory\n"), 0o600); err != nil {
		t.Fatalf("write the blocker: %v", err)
	}

	hosts, err := a.add(config.NewHost{Alias: "web2", Host: "10.0.0.2"})
	if err == nil {
		t.Fatal("AddHost reported success with nowhere to write")
	}
	if hosts != nil {
		t.Errorf("a failed add returned a list: %v", hostNames(hosts))
	}
}

func hostNames(hosts []config.SSHHost) []string {
	out := make([]string, len(hosts))
	for i, host := range hosts {
		out[i] = host.Name
	}
	return out
}

func hostIn(t *testing.T, hosts []config.SSHHost, name string) config.SSHHost {
	t.Helper()

	for _, host := range hosts {
		if host.Name == name {
			return host
		}
	}
	t.Fatalf("no host named %q in %v", name, hostNames(hosts))
	return config.SSHHost{}
}
