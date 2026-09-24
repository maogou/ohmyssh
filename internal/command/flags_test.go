package command

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"

	"github.com/maogou/ohmyssh/internal/pkg/errno"
	"github.com/maogou/ohmyssh/internal/service"
)

// optionsFor runs the global flags alone and reports what connectOptions made of
// them. The root captures exit errors instead of letting them reach os.Exit, as
// the other roots in this package do; Run still returns them to the caller.
func optionsFor(t *testing.T, in io.Reader, argv ...string) (service.ConnectOptions, error) {
	t.Helper()

	var opts service.ConnectOptions
	root := &cli.Command{
		Name:   "ohmyssh",
		Flags:  globalFlags(),
		Reader: in,
		ExitErrHandler: func(_ context.Context, _ *cli.Command, _ error) {
			// Expected in the failure cases; the error is returned by Run.
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			var err error
			opts, err = connectOptions(cmd)
			return err
		},
	}
	err := root.Run(context.Background(), append([]string{"ohmyssh"}, argv...))
	return opts, err
}

// clearPasswordEnv drops the environment's password, which is another way
// --password gets a value and would otherwise decide what these tests observe.
func clearPasswordEnv(t *testing.T) {
	t.Helper()
	t.Setenv("OHMYSSH_PASSWORD", "")
}

func TestPasswordStdinIsReadFromStandardInput(t *testing.T) {
	clearPasswordEnv(t)

	opts, err := optionsFor(t, strings.NewReader("hunter2\n"), "--password-stdin", "web1")
	if err != nil {
		t.Fatalf("--password-stdin failed: %v", err)
	}
	if opts.Password != "hunter2" {
		t.Errorf("password = %q, want %q", opts.Password, "hunter2")
	}
}

// The read stops at the newline, so anything after it stays for whoever else is
// on the other end of the pipe — a remote command's own input, for one.
func TestPasswordStdinLeavesTheRestOfTheInputAlone(t *testing.T) {
	clearPasswordEnv(t)

	in := strings.NewReader("hunter2\nuptime\n")
	if _, err := optionsFor(t, in, "--password-stdin"); err != nil {
		t.Fatalf("--password-stdin failed: %v", err)
	}

	rest, err := io.ReadAll(in)
	if err != nil {
		t.Fatalf("read the rest: %v", err)
	}
	if string(rest) != "uptime\n" {
		t.Errorf("remaining input = %q, want %q", rest, "uptime\n")
	}
}

// A line ending written on Windows is still one line: the carriage return
// belongs to the line ending, not to the password.
func TestPasswordStdinTrimsACarriageReturn(t *testing.T) {
	clearPasswordEnv(t)

	opts, err := optionsFor(t, strings.NewReader("hunter2\r\n"), "--password-stdin")
	if err != nil {
		t.Fatalf("--password-stdin failed: %v", err)
	}
	if opts.Password != "hunter2" {
		t.Errorf("password = %q, want %q", opts.Password, "hunter2")
	}
}

// An empty line is a command line that cannot be acted on, and saying so is
// better than attempting an authentication that was never given a password.
func TestPasswordStdinRejectsAnEmptyPassword(t *testing.T) {
	clearPasswordEnv(t)

	for _, tt := range []struct {
		name string
		in   string
	}{
		{"an empty line", "\n"},
		{"nothing at all", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := optionsFor(t, strings.NewReader(tt.in), "--password-stdin")
			if got := exitCode(t, err); got != errno.CodeUsage {
				t.Errorf("exit code = %d, want %d", got, errno.CodeUsage)
			}
			if !strings.Contains(err.Error(), "--password-stdin") {
				t.Errorf("error = %v, want it to name the flag", err)
			}
		})
	}
}

// Two sources for one password is a command line that says two different things,
// and the pipe is left unread because there is nothing to decide with it.
func TestPasswordFlagAndPasswordStdinAreMutuallyExclusive(t *testing.T) {
	clearPasswordEnv(t)

	in := strings.NewReader("hunter2\n")
	_, err := optionsFor(t, in, "--password", "s3cret", "--password-stdin")
	if got := exitCode(t, err); got != errno.CodeUsage {
		t.Fatalf("exit code = %d, want %d", got, errno.CodeUsage)
	}
	for _, flag := range []string{"--password", "--password-stdin"} {
		if !strings.Contains(err.Error(), flag) {
			t.Errorf("error = %v, want it to name %s", err, flag)
		}
	}

	if in.Len() != len("hunter2\n") {
		t.Error("the password was read from standard input before the flags were reconciled")
	}
}

// Standard input belongs to the command line only when the flag asks for it.
func TestStandardInputIsUnreadWithoutTheFlag(t *testing.T) {
	clearPasswordEnv(t)

	in := strings.NewReader("hunter2\n")
	opts, err := optionsFor(t, in, "--password", "s3cret")
	if err != nil {
		t.Fatalf("connectOptions failed: %v", err)
	}
	if opts.Password != "s3cret" {
		t.Errorf("password = %q, want %q", opts.Password, "s3cret")
	}
	if in.Len() != len("hunter2\n") {
		t.Error("standard input was read without --password-stdin")
	}
}

// Every command that connects is a subcommand, so the flag has to be global and
// the password has to come from the root's standard input.
func TestPasswordStdinWorksFromASubcommand(t *testing.T) {
	clearPasswordEnv(t)

	var opts service.ConnectOptions
	var inner error
	root := &cli.Command{
		Name:   "ohmyssh",
		Flags:  globalFlags(),
		Reader: strings.NewReader("hunter2\n"),
		ExitErrHandler: func(_ context.Context, _ *cli.Command, _ error) {
			// Expected in the failure cases; the error is returned by Run.
		},
		Commands: []*cli.Command{{
			Name: "get",
			Action: func(_ context.Context, cmd *cli.Command) error {
				opts, inner = connectOptions(cmd)
				return inner
			},
		}},
	}
	if err := root.Run(context.Background(), []string{"ohmyssh", "--password-stdin", "get"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if inner != nil {
		t.Fatalf("connectOptions: %v", inner)
	}
	if opts.Password != "hunter2" {
		t.Errorf("password = %q, want %q", opts.Password, "hunter2")
	}
}

// brokenReader stands in for a pipe that fails rather than ending.
type brokenReader struct{ err error }

func (r brokenReader) Read([]byte) (int, error) { return 0, r.err }

// A read failure is not a usage error: the command line was fine, the pipe was
// not, and the wrapped error keeps what the pipe said.
func TestReadPasswordReportsAReadFailure(t *testing.T) {
	boom := errors.New("the pipe broke")

	_, err := readPassword(brokenReader{boom})
	if !errors.Is(err, boom) {
		t.Errorf("error = %v, want it to wrap %v", err, boom)
	}
	if !strings.Contains(err.Error(), "--password-stdin") {
		t.Errorf("error = %v, want it to name the flag", err)
	}
}
