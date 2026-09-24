package service

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/maogou/ohmyssh/internal/config"
)

// remove deletes a host through the same call the browser's X key makes.
func (a *hostFiles) remove(host config.SSHHost) ([]config.SSHHost, error) {
	return a.RemoveHost(context.Background(), ConnectOptions{ConfigPath: a.sshConfig}, host)
}

// A host added through ohmyssh can be taken back out of it, and the list that
// comes back is the file read again rather than the row dropped in memory — so
// the browser shows what the next run of ohmyssh will.
func TestRemoveHostWritesAndReturnsTheReloadedList(t *testing.T) {
	a := newHostFiles(t, "Host web1\n    HostName 10.0.0.1\n")
	if _, err := a.add(config.NewHost{Alias: "web2", Host: "10.0.0.2", Tags: []string{"prod"}}); err != nil {
		t.Fatalf("add web2: %v", err)
	}
	if _, err := a.add(config.NewHost{Alias: "web3", Host: "10.0.0.3"}); err != nil {
		t.Fatalf("add web3: %v", err)
	}

	hosts, err := a.remove(hostIn(t, a.list(t), "web2"))
	if err != nil {
		t.Fatalf("RemoveHost: %v", err)
	}
	if got, want := hostNames(hosts), []string{"web1", "web3"}; !slices.Equal(got, want) {
		t.Errorf("returned %v, want %v", got, want)
	}

	// It is gone from disk too, tags line and all, and the host that came after it
	// is exactly as it was.
	written, err := os.ReadFile(a.extra)
	if err != nil {
		t.Fatalf("read the file it wrote: %v", err)
	}
	if strings.Contains(string(written), "web2") || strings.Contains(string(written), "prod") {
		t.Errorf("the file still holds the host that was deleted:\n%s", written)
	}
	if !strings.Contains(string(written), "Host web3") {
		t.Errorf("the file lost a host that was not deleted:\n%s", written)
	}

	// The ssh config is read and never written, whatever happens here.
	primary, err := os.ReadFile(a.sshConfig)
	if err != nil {
		t.Fatalf("read the ssh config: %v", err)
	}
	if string(primary) != "Host web1\n    HostName 10.0.0.1\n" {
		t.Errorf("the ssh config changed:\n%s", primary)
	}
}

// The ssh config is the user's file, and ohmyssh only ever reads it: a host from
// there is refused with the file named, and nothing on disk changes — the check
// comes before the delete, not after it.
func TestRemoveHostRefusesAHostFromTheSSHConfig(t *testing.T) {
	a := newHostFiles(t, "Host web1\n    HostName 10.0.0.1\n")
	if _, err := a.add(config.NewHost{Alias: "web2", Host: "10.0.0.2"}); err != nil {
		t.Fatalf("add web2: %v", err)
	}
	before, err := os.ReadFile(a.extra)
	if err != nil {
		t.Fatalf("read the hosts file: %v", err)
	}

	hosts, err := a.remove(hostIn(t, a.list(t), "web1"))
	if err == nil {
		t.Fatal("a host from the ssh config was deleted")
	}
	if !strings.Contains(err.Error(), a.sshConfig) {
		t.Errorf("error %q does not name %s", err, a.sshConfig)
	}
	if hosts != nil {
		t.Errorf("a refused delete returned a list: %v", hostNames(hosts))
	}

	after, err := os.ReadFile(a.extra)
	if err != nil {
		t.Fatalf("read the hosts file: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("the hosts file changed on a refusal:\n%s", after)
	}
	// The host is still there and still resolves: a refusal is not a partial
	// delete.
	if got := hostIn(t, a.list(t), "web1").Hostname; got != "10.0.0.1" {
		t.Errorf("web1 points at %q", got)
	}
}

// A file that changed under the browser — another window, another ohmyssh — is
// reported as the failure it is, with no list: a browser told the delete worked
// would drop a row for a host that is still there.
func TestRemoveHostReportsAHostThatIsNoLongerInTheFile(t *testing.T) {
	a := newHostFiles(t, "Host web1\n    HostName 10.0.0.1\n")
	if _, err := a.add(config.NewHost{Alias: "web2", Host: "10.0.0.2"}); err != nil {
		t.Fatalf("add web2: %v", err)
	}
	// What the browser is holding, from before the file changed.
	listed := a.list(t)

	if _, err := a.remove(hostIn(t, listed, "web2")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	hosts, err := a.remove(hostIn(t, listed, "web2"))
	if err == nil {
		t.Fatal("a host that is not in the file was reported as deleted")
	}
	if hosts != nil {
		t.Errorf("a failed delete returned a list: %v", hostNames(hosts))
	}
	if !strings.Contains(err.Error(), a.extra) {
		t.Errorf("error %q does not name %s", err, a.extra)
	}
}
