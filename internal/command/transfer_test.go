package command

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"

	"github.com/maogou/ohmyssh/internal/config"
	"github.com/maogou/ohmyssh/internal/pkg/errno"
	"github.com/maogou/ohmyssh/internal/service"
	"github.com/maogou/ohmyssh/internal/sshclient"
)

// fakeConnect is the ConnectService put, get and scp are tested against. It
// keeps every request it was handed, so the mapping from the command line onto a
// transfer is asserted on the request itself rather than read back out of a
// rendered line.
type fakeConnect struct {
	requests []service.TransferRequest
	result   service.TransferResult
	err      error
	// report, when set, is called with the progress reporter the command built,
	// which is what proves the display and the transfer are wired together.
	report func(sshclient.ProgressFunc)
}

// Only Transfer is exercised by these commands; the rest exist to satisfy the
// interface.
func (f *fakeConnect) Browse(context.Context, service.ConnectOptions, string) error { return nil }

func (f *fakeConnect) Connect(context.Context, service.ConnectOptions, string, []string) error {
	return nil
}

func (f *fakeConnect) Exec(context.Context, service.ConnectOptions, string, []string, io.Writer, io.Writer) error {
	return nil
}

func (f *fakeConnect) AddHost(context.Context, service.ConnectOptions, config.NewHost) ([]config.SSHHost, error) {
	return nil, nil
}

func (f *fakeConnect) RemoveHost(context.Context, service.ConnectOptions, config.SSHHost) ([]config.SSHHost, error) {
	return nil, nil
}

func (f *fakeConnect) Transfer(
	_ context.Context,
	_ service.ConnectOptions,
	req service.TransferRequest,
	progress sshclient.ProgressFunc,
) (service.TransferResult, error) {
	f.requests = append(f.requests, req)
	if f.report != nil {
		f.report(progress)
	}
	return f.result, f.err
}

// transferCLI builds the three commands under a root whose streams are the
// test's own, so a progress frame or a summary line can be read back instead of
// landing in the test log. cli.Exit reaches os.Exit by default, which would take
// the whole test binary down with it, so the root captures exit errors instead.
func transferCLI(t *testing.T, connect service.ConnectService) (*cli.Command, *bytes.Buffer) {
	t.Helper()

	out := &bytes.Buffer{}
	return &cli.Command{
		Name:      "ohmyssh",
		Flags:     globalFlags(),
		Commands:  []*cli.Command{putCommand(connect), getCommand(connect), scpCommand(connect)},
		Writer:    out,
		ErrWriter: out,
		ExitErrHandler: func(_ context.Context, _ *cli.Command, _ error) {
			// Expected in the usage cases; Run returns the error to the caller.
		},
	}, out
}

func runTransferCLI(t *testing.T, connect service.ConnectService, argv ...string) (*bytes.Buffer, error) {
	t.Helper()

	root, out := transferCLI(t, connect)
	err := root.Run(context.Background(), append([]string{"ohmyssh"}, argv...))
	return out, err
}

// exitCode is the status a failed run carries. The usage cases are told apart
// from an ordinary failure by it, not by the wording of the message.
func exitCode(t *testing.T, err error) int {
	t.Helper()

	exitErr, ok := errors.AsType[cli.ExitCoder](err)
	if !ok {
		t.Fatalf("error is %T, want a cli.ExitCoder: %v", err, err)
	}
	return exitErr.ExitCode()
}

// The three commands differ only in how they spell the two ends, so what is
// asserted is the request each one produces: the service is handed a local path
// and a remote path whichever word the user typed first.
func TestTransferCommandsMapArgumentsToARequest(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want service.TransferRequest
	}{
		{
			name: "put uploads the two paths as written",
			argv: []string{"put", "web1", "./dist", "/opt/app"},
			want: service.TransferRequest{Target: "web1", Local: "./dist", Remote: "/opt/app", Direction: sshclient.Upload},
		},
		{
			name: "get is put with the ends swapped",
			argv: []string{"get", "web1", "/var/log/app.log", "./app.log"},
			want: service.TransferRequest{Target: "web1", Local: "./app.log", Remote: "/var/log/app.log", Direction: sshclient.Download},
		},
		{
			name: "scp with the host second uploads",
			argv: []string{"scp", "./deploy.sh", "web1:/tmp/deploy.sh"},
			want: service.TransferRequest{Target: "web1", Local: "./deploy.sh", Remote: "/tmp/deploy.sh", Direction: sshclient.Upload},
		},
		{
			name: "scp with the host first downloads",
			argv: []string{"scp", "web1:/var/log/app.log", "./app.log"},
			want: service.TransferRequest{Target: "web1", Local: "./app.log", Remote: "/var/log/app.log", Direction: sshclient.Download},
		},
		{
			name: "a bare host is the home directory",
			argv: []string{"scp", "./deploy.sh", "web1:"},
			want: service.TransferRequest{Target: "web1", Local: "./deploy.sh", Remote: ".", Direction: sshclient.Upload},
		},
		{
			name: "a login rides along with the host",
			argv: []string{"scp", "./deploy.sh", "deploy@web1:/tmp/deploy.sh"},
			want: service.TransferRequest{Target: "deploy@web1", Local: "./deploy.sh", Remote: "/tmp/deploy.sh", Direction: sshclient.Upload},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connect := &fakeConnect{}
			if _, err := runTransferCLI(t, connect, tt.argv...); err != nil {
				t.Fatalf("run %v: %v", tt.argv, err)
			}

			if len(connect.requests) != 1 {
				t.Fatalf("service saw %d transfers, want 1", len(connect.requests))
			}
			if got := connect.requests[0]; got != tt.want {
				t.Errorf("request = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// A transfer that worked says so in one line, on the error stream, so stdout
// stays free for whatever the caller pipes next.
func TestSuccessfulTransferReportsOneSummaryLine(t *testing.T) {
	connect := &fakeConnect{result: service.TransferResult{Files: 3, Bytes: 4096}}

	out, err := runTransferCLI(t, connect, "put", "web1", "./dist", "/opt/app")
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if want := "✓ put ./dist → /opt/app  3 files  4.0 KB"; !strings.Contains(out.String(), want) {
		t.Errorf("output = %q, want it to contain %q", out.String(), want)
	}
}

// A download quotes the ends in the order the bytes travelled, so a get does not
// read as though the file went the other way.
func TestDownloadSummaryReadsForwards(t *testing.T) {
	connect := &fakeConnect{result: service.TransferResult{Files: 1, Bytes: 12}}

	out, err := runTransferCLI(t, connect, "get", "web1", "/tmp/a.md", "./a.md")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if want := "✓ get /tmp/a.md → ./a.md  1 file  12 B"; !strings.Contains(out.String(), want) {
		t.Errorf("output = %q, want it to contain %q", out.String(), want)
	}
}

// A failure is reported once, by the runner, with the direction and the ends in
// front of the reason — not as a second summary line on top of it.
func TestFailedTransferIsOnePrefixedError(t *testing.T) {
	connect := &fakeConnect{err: errors.New("permission denied")}

	out, err := runTransferCLI(t, connect, "put", "web1", "./dist", "/opt/app")
	if err == nil {
		t.Fatal("a failed transfer returned no error")
	}
	if want := "put ./dist → /opt/app: permission denied"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %v, want it to contain %q", err, want)
	}
	if strings.Contains(out.String(), "✓") {
		t.Errorf("a failed transfer printed a success line: %q", out.String())
	}
}

// The reporter is handed to the service, and what it draws lands on the same
// stream as the summary. Without a terminal it degrades to one line per finished
// file, which is what a redirected run can still read.
func TestProgressReachesTheCommandOutputStream(t *testing.T) {
	connect := &fakeConnect{}
	connect.report = func(progress sshclient.ProgressFunc) {
		if progress == nil {
			t.Error("the service was handed a nil progress reporter")
			return
		}
		progress(sshclient.Progress{File: "dist/a.txt", FileTotal: 2048, Done: true})
	}

	out, err := runTransferCLI(t, connect, "put", "web1", "./dist", "/opt/app")
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if want := "2.0 KB  dist/a.txt"; !strings.Contains(out.String(), want) {
		t.Errorf("output = %q, want it to contain %q", out.String(), want)
	}
}

// scp has two ends and only one of them may name a host: there is no way to name
// the credentials for two.
func TestScpRejectsTwoRemoteEnds(t *testing.T) {
	connect := &fakeConnect{}

	_, err := runTransferCLI(t, connect, "scp", "web1:/tmp/a", "web2:/tmp/b")
	if err == nil {
		t.Fatal("scp accepted a transfer between two hosts")
	}
	if got := exitCode(t, err); got != errno.CodeUsage {
		t.Errorf("exit code = %d, want %d", got, errno.CodeUsage)
	}
	if len(connect.requests) != 0 {
		t.Errorf("a rejected scp still reached the service: %+v", connect.requests)
	}
}

func TestScpRejectsTwoLocalEnds(t *testing.T) {
	connect := &fakeConnect{}

	_, err := runTransferCLI(t, connect, "scp", "./a", "./b")
	if err == nil {
		t.Fatal("scp accepted a copy with no host on either end")
	}
	if got := exitCode(t, err); got != errno.CodeUsage {
		t.Errorf("exit code = %d, want %d", got, errno.CodeUsage)
	}
}

// A local path that happens to hold a colon must not be read as a host, or the
// Windows the flag exists for would silently copy to a machine called C.
func TestScpTreatsAWindowsPathAsLocal(t *testing.T) {
	connect := &fakeConnect{}

	_, err := runTransferCLI(t, connect, "scp", `C:\src\a.txt`, `C:\dst\a.txt`)
	if err == nil {
		t.Fatal("scp accepted two local Windows paths")
	}
	if got := exitCode(t, err); got != errno.CodeUsage {
		t.Errorf("exit code = %d, want %d", got, errno.CodeUsage)
	}
}

func TestTransferCommandsRejectTheWrongNumberOfArguments(t *testing.T) {
	tests := []struct {
		name string
		argv []string
	}{
		{"put with no args", []string{"put"}},
		{"put with two", []string{"put", "web1", "./a"}},
		{"put with four", []string{"put", "web1", "./a", "/tmp/a", "extra"}},
		{"get with no args", []string{"get"}},
		{"get with two", []string{"get", "web1", "/tmp/a"}},
		{"scp with one", []string{"scp", "web1:/tmp/a"}},
		{"scp with three", []string{"scp", "web1:/tmp/a", "./a", "./b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connect := &fakeConnect{}
			_, err := runTransferCLI(t, connect, tt.argv...)
			if err == nil {
				t.Fatalf("%v was accepted", tt.argv)
			}
			if got := exitCode(t, err); got != errno.CodeUsage {
				t.Errorf("exit code = %d, want %d (err: %v)", got, errno.CodeUsage, err)
			}
			if len(connect.requests) != 0 {
				t.Errorf("a usage error still reached the service: %+v", connect.requests)
			}
		})
	}
}

// The parser is the one place a transfer token is ambiguous, so every shape that
// could be read two ways is pinned here.
func TestParseTransferTarget(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		wantOK   bool
		wantHost string
		wantPath string
	}{
		{name: "host and path", token: "web1:/tmp/a", wantOK: true, wantHost: "web1", wantPath: "/tmp/a"},
		{name: "a login is part of the host", token: "deploy@web1:/tmp/a", wantOK: true, wantHost: "deploy@web1", wantPath: "/tmp/a"},
		{name: "a bare host is the home directory", token: "web1:", wantOK: true, wantHost: "web1", wantPath: "."},
		{name: "surrounding space is ignored", token: "  web1:/tmp/a  ", wantOK: true, wantHost: "web1", wantPath: "/tmp/a"},

		{name: "no colon is local", token: "./a.txt"},
		{name: "empty is not a target", token: ""},
		{name: "a colon first names no host", token: ":/tmp/a"},
		// A slash means this is a path that happens to hold a colon: no host name
		// has one.
		{name: "a directory holding a colon is local", token: "./dir:colon"},
		{name: "a path under one is local", token: "a/b:c"},
		// The two shapes the drive-letter test exists for.
		{name: "a Windows path is local", token: `C:\src`},
		{name: "a Windows path with slashes is local", token: "C:/src"},

		// An scp token has one colon to spend, and it is the one before the path:
		// the user@host:port form has no room here, so the port is read as part
		// of the path and the connection is refused later. Recording it keeps the
		// next person from "fixing" the parser into splitting on the last colon,
		// which would break every path that holds one.
		{name: "a port is read as part of the path", token: "web1:2222:/tmp/a", wantOK: true, wantHost: "web1", wantPath: "2222:/tmp/a"},
		// C:file is a drive-relative path, but it is indistinguishable from a
		// host called C, and scp's reading is the one that carries information.
		{name: "a drive-relative path is a host", token: "C:file", wantOK: true, wantHost: "C", wantPath: "file"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, remotePath, ok := parseTransferTarget(tt.token)
			if ok != tt.wantOK {
				t.Fatalf("parseTransferTarget(%q) ok = %v, want %v", tt.token, ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if host != tt.wantHost || remotePath != tt.wantPath {
				t.Errorf("parseTransferTarget(%q) = (%q, %q), want (%q, %q)",
					tt.token, host, remotePath, tt.wantHost, tt.wantPath)
			}
		})
	}
}

// isDriveLetter is what the parser leans on for the one ambiguity it can settle,
// so it is worth pinning on its own as well.
func TestIsDriveLetter(t *testing.T) {
	for _, token := range []string{`C:\src`, "C:/src", "c:/", "Z:"} {
		if !isDriveLetter(token) {
			t.Errorf("isDriveLetter(%q) = false, want true", token)
		}
	}
	for _, token := range []string{"", "C", "C:file", ":C", "1:/x", "web1:/tmp/a", "./a:/b"} {
		if isDriveLetter(token) {
			t.Errorf("isDriveLetter(%q) = true, want false", token)
		}
	}
}
