package command

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/urfave/cli/v3"
)

// captureArgs builds a command shaped like exec that records the positional
// arguments urfave/cli resolves, so the "--" contract can be asserted directly.
func captureArgs(t *testing.T, argv []string) []string {
	t.Helper()

	var captured []string
	cmd := &cli.Command{
		Name:  "ohmyssh",
		Flags: globalFlags(),
		Commands: []*cli.Command{{
			Name: "exec",
			Flags: []cli.Flag{
				&cli.BoolFlag{Name: "no-prompt"},
			},
			Action: func(_ context.Context, c *cli.Command) error {
				captured = c.Args().Slice()
				return nil
			},
		}},
	}
	if err := cmd.Run(context.Background(), argv); err != nil {
		t.Fatalf("run %v: %v", argv, err)
	}
	return captured
}

// exec relies on "--" stopping flag parsing so the remote command keeps its own flags.
func TestDoubleDashProtectsRemoteCommandFlags(t *testing.T) {
	got := captureArgs(t, []string{"ohmyssh", "exec", "web1", "--", "ls", "-la", "/tmp"})
	want := []string{"web1", "ls", "-la", "/tmp"}

	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

// Flags before "--" belong to ohmyssh, not to the remote command.
func TestFlagsBeforeDoubleDashAreConsumed(t *testing.T) {
	got := captureArgs(t, []string{"ohmyssh", "--debug", "exec", "--no-prompt", "web1", "--", "uptime", "-p"})
	if len(got) != 3 || got[0] != "web1" || got[1] != "uptime" || got[2] != "-p" {
		t.Errorf("args = %v, want [web1 uptime -p]", got)
	}
}

func TestGlobalFlagsReachSubcommands(t *testing.T) {
	var configPath string
	cmd := &cli.Command{
		Name:  "ohmyssh",
		Flags: globalFlags(),
		Commands: []*cli.Command{{
			Name: "list",
			Action: func(_ context.Context, c *cli.Command) error {
				// Resolved through the command lineage, not a local flag.
				configPath = c.String("config")
				return nil
			},
		}},
	}
	if err := cmd.Run(context.Background(), []string{"ohmyssh", "--config", "/tmp/custom", "list"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if configPath != "/tmp/custom" {
		t.Errorf("subcommand saw config = %q, want %q", configPath, "/tmp/custom")
	}
}

// writeTestConfig writes a throwaway ssh config for the tests in this package.
// The host-resolution tests live with the code they cover, in
// internal/repository.
func writeTestConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
