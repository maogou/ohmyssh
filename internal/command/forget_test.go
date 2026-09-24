package command

import (
	"context"
	"strings"
	"testing"

	"github.com/maogou/ohmyssh/internal/credential"
	"github.com/maogou/ohmyssh/internal/repository"
	"github.com/maogou/ohmyssh/internal/service"
	"github.com/urfave/cli/v3"
)

// isolateStore points ohmyssh's own files at a fresh temporary directory, so a
// test never reads or writes the real ones.
//
// The store lives under the home directory — ~/.ohmyssh/credentials.json, which
// is what os.UserHomeDir reports — and not under the XDG config directory where
// it lived before it moved. Redirecting XDG_CONFIG_HOME alone therefore stopped
// isolating anything: these tests read and wrote the store of whoever ran them,
// and TestForgetAllClearsTheStore ends by deleting every password in it. All
// three variables are set because they cover the three ways a path is reached:
// HOME is the home directory on Unix and USERPROFILE is the one on Windows, and
// XDG_CONFIG_HOME is the pre-move location, which a read adopts — and deletes —
// when the store in the home directory is missing.
func isolateStore(t *testing.T) {
	t.Helper()

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)

	// Whatever moves next, the isolation is asserted rather than assumed: a store
	// that resolves outside the temporary directory fails here, on the first run,
	// rather than passing quietly while it deletes the passwords of the person
	// running the tests.
	if got := credential.Path(); !strings.HasPrefix(got, tmp) {
		t.Fatalf("the credential store is not isolated: %s is outside %s", got, tmp)
	}
}

// forgetRoot builds the command tree under test. cli.Exit reaches os.Exit by
// default, which would take the whole test binary down with it, so the root
// captures exit errors instead; Run still returns them to the caller.
func forgetRoot(t *testing.T) *cli.Command {
	t.Helper()
	credentials := service.NewCredentialService(repository.NewHosts(""), repository.NewCredential())
	return &cli.Command{
		Name:     "ohmyssh",
		Flags:    globalFlags(),
		Commands: []*cli.Command{forgetCommand(credentials)},
		ExitErrHandler: func(_ context.Context, _ *cli.Command, _ error) {
			// Expected in the failure cases; the error is returned by Run.
		},
	}
}

func runForget(t *testing.T, argv ...string) error {
	t.Helper()
	args := append([]string{"ohmyssh", "forget"}, argv...)
	return forgetRoot(t).Run(context.Background(), args)
}

// forget resolves the host exactly as connect does, so an alias reaches the
// credential saved under that login identity.
func TestForgetResolvesAliasesToTheLoginIdentity(t *testing.T) {
	isolateStore(t)
	config := writeTestConfig(t, `
Host web1
    HostName 10.0.0.1
    User deploy
    Port 2222
`)
	if err := credential.Set("deploy@10.0.0.1:2222", "hunter2"); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	if err := runForget(t, "--config", config, "web1"); err != nil {
		t.Fatalf("forget: %v", err)
	}

	if _, ok := credential.Get("deploy@10.0.0.1:2222"); ok {
		t.Error("the password is still saved after forget")
	}
}

// Two aliases for one login share a credential, so forgetting either clears it.
func TestForgetThroughASecondAlias(t *testing.T) {
	isolateStore(t)
	config := writeTestConfig(t, `
Host web1
    HostName 10.0.0.1
    User deploy

Host frontend
    HostName 10.0.0.1
    User deploy
`)
	if err := credential.Set("deploy@10.0.0.1:22", "hunter2"); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	if err := runForget(t, "--config", config, "frontend"); err != nil {
		t.Fatalf("forget: %v", err)
	}

	if keys := credential.Keys(); len(keys) != 0 {
		t.Errorf("Keys() = %v, want none", keys)
	}
}

func TestForgetAllClearsTheStore(t *testing.T) {
	isolateStore(t)
	for _, key := range []string{"a@x:22", "b@y:22"} {
		if err := credential.Set(key, "pw"); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}

	if err := runForget(t, "--all"); err != nil {
		t.Fatalf("forget --all: %v", err)
	}

	if keys := credential.Keys(); len(keys) != 0 {
		t.Errorf("Keys() = %v after --all, want none", keys)
	}
}

// Forgetting a host with nothing saved is a failure the caller can see, not a
// silent success.
func TestForgetWithoutASavedPasswordReportsFailure(t *testing.T) {
	isolateStore(t)
	config := writeTestConfig(t, "Host web1\n    HostName 10.0.0.1\n")

	err := runForget(t, "--config", config, "web1")
	if err == nil {
		t.Fatal("forget on an empty store returned no error")
	}
	if !strings.Contains(err.Error(), "no saved password") {
		t.Errorf("error = %v, want it to mention no saved password", err)
	}
}

func TestForgetRejectsAnUnknownAlias(t *testing.T) {
	isolateStore(t)
	config := writeTestConfig(t, "Host web1\n    HostName 10.0.0.1\n")

	err := runForget(t, "--config", config, "nosuchhost")
	if err == nil {
		t.Fatal("forget accepted an alias that is not in the config")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want it to mention the host was not found", err)
	}
}

// Saving is opt-out, since the point is not to retype a password. The flag is
// global, so it has to be reachable from a subcommand too.
func TestSavingPasswordsIsOnByDefault(t *testing.T) {
	parse := func(argv ...string) bool {
		t.Helper()

		var got bool
		cmd := &cli.Command{
			Name:  "ohmyssh",
			Flags: globalFlags(),
			Commands: []*cli.Command{{
				Name: "exec",
				Action: func(_ context.Context, c *cli.Command) error {
					got = c.Bool("no-save-password")
					return nil
				},
			}},
		}
		if err := cmd.Run(context.Background(), append([]string{"ohmyssh", "exec"}, argv...)); err != nil {
			t.Fatalf("run %v: %v", argv, err)
		}
		return got
	}

	if parse() {
		t.Error("passwords would never be remembered: no-save-password defaults to true")
	}
	if !parse("--no-save-password") {
		t.Error("--no-save-password did not take effect")
	}
}
