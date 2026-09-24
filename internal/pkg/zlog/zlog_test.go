package zlog

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The logger is process-wide, so every test installs the one it wants through
// Setup rather than sharing whatever the last test left behind.

// read is the log file as it reads now.
func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// TestDetachSendsLogsToTheFile is the whole point of the function: while a view
// owns the terminal, a log line must not reach the terminal, because the line
// lands in the middle of a frame and shifts everything below it.
func TestDetachSendsLogsToTheFile(t *testing.T) {
	stderr := &bytes.Buffer{}
	Setup(Options{Level: "info", Stderr: stderr})
	path := filepath.Join(t.TempDir(), "ohmyssh.log")

	L().Info().Msg("before the view")
	if !strings.Contains(stderr.String(), "before the view") {
		t.Fatalf("a log line before Detach did not go to stderr: %q", stderr.String())
	}

	restore := Detach(path)
	L().Info().Str("host", "web1").Str("addr", "10.0.0.1:22").Msg("connected")

	if got := read(t, path); !strings.Contains(got, `"message":"connected"`) ||
		!strings.Contains(got, `"host":"web1"`) {
		t.Errorf("the log file does not hold the line written while detached: %q", got)
	}
	if strings.Contains(stderr.String(), "connected") {
		t.Errorf("a log line reached stderr while detached: %q", stderr.String())
	}

	// Giving the terminal back has to give the logs back with it, or everything
	// after the view would be written into a file nobody reads.
	restore()
	L().Info().Msg("after the view")
	if !strings.Contains(stderr.String(), "after the view") {
		t.Errorf("stderr did not get the logs back: %q", stderr.String())
	}
}

// TestDetachKeepsTheLevelAndFormatSetUp: being sent to a file changes where the
// logs go, not what is logged or how it reads. A --log-level warn run must not
// start writing info lines into the file, and a console run must not turn into
// JSON lines because the writer changed.
func TestDetachKeepsTheLevelAndFormatSetUp(t *testing.T) {
	Setup(Options{Level: "warn", Pretty: true, Stderr: &bytes.Buffer{}})
	path := filepath.Join(t.TempDir(), "ohmyssh.log")

	restore := Detach(path)
	defer restore()

	L().Info().Msg("an info line")
	L().Warn().Msg("a warning")

	got := read(t, path)
	if strings.Contains(got, "an info line") {
		t.Errorf("the level set up was not kept: %q", got)
	}
	if !strings.Contains(got, "a warning") {
		t.Errorf("the warning line is not in the file: %q", got)
	}
	if strings.Contains(got, `"level"`) {
		t.Errorf("the console format set up was not kept: %q", got)
	}
}

// TestDetachDatesTheLinesInTheFile: a log file outlives the day it was written
// on, so its lines say which day that was. The terminal keeps the short stamp —
// what is on screen is happening now — and the file is what gains the date.
func TestDetachDatesTheLinesInTheFile(t *testing.T) {
	stderr := &bytes.Buffer{}
	Setup(Options{Level: "info", Pretty: true, Stderr: stderr})
	path := filepath.Join(t.TempDir(), "ohmyssh.log")

	L().Info().Msg("on the terminal")
	restore := Detach(path)
	defer restore()
	L().Info().Msg("in the file")

	day := time.Now().Format(time.DateOnly)
	if got := read(t, path); !strings.Contains(got, day) {
		t.Errorf("the line in the file is not dated %s: %q", day, got)
	}
	if strings.Contains(stderr.String(), day) {
		t.Errorf("the line on the terminal gained a date it does not need: %q", stderr.String())
	}
}

// TestOutputFollowsTheLogs: a server's banner and a ProxyCommand's stderr are
// written to Output rather than to stderr, and while a view owns the terminal
// they have to end up in the file with the logs — on screen they would land in
// the middle of a frame, which is the corruption Detach exists to prevent.
// Outside a view Output is what Setup was given, so nothing a command line
// prints moves.
func TestOutputFollowsTheLogs(t *testing.T) {
	stderr := &bytes.Buffer{}
	Setup(Options{Level: "info", Stderr: stderr})
	path := filepath.Join(t.TempDir(), "ohmyssh.log")

	_, _ = fmt.Fprint(Output(), "a banner on the terminal\n")
	if !strings.Contains(stderr.String(), "a banner on the terminal") {
		t.Errorf("text written to Output did not reach the writer set up: %q", stderr.String())
	}

	restore := Detach(path)
	_, _ = fmt.Fprint(Output(), "a banner in the file\n")
	if got := read(t, path); !strings.Contains(got, "a banner in the file") {
		t.Errorf("text written to Output while detached is not in the log file: %q", got)
	}
	if strings.Contains(stderr.String(), "a banner in the file") {
		t.Errorf("text written to Output reached the terminal while detached: %q", stderr.String())
	}

	restore()
	_, _ = fmt.Fprint(Output(), "on the terminal again\n")
	if !strings.Contains(stderr.String(), "on the terminal again") {
		t.Errorf("Output did not give the terminal back: %q", stderr.String())
	}
}

// TestDetachLogsNowhereWhenThereIsNoFile: a log that cannot be written is not a
// reason to write over the frame the view is drawing, which is what falling back
// to stderr would do. It is dropped instead.
func TestDetachLogsNowhereWhenThereIsNoFile(t *testing.T) {
	dir := t.TempDir()
	// A regular file where the directory would have to be: the log can neither be
	// appended to nor have its directory created.
	blocker := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("prepare %s: %v", blocker, err)
	}

	tests := []struct {
		name string
		path string
	}{
		{"a home directory that could not be named", ""},
		{"a path under something that is not a directory", filepath.Join(blocker, "ohmyssh.log")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stderr := &bytes.Buffer{}
			Setup(Options{Level: "info", Stderr: stderr})

			restore := Detach(tt.path)
			defer restore()

			L().Info().Msg("nothing should read this")
			if stderr.Len() != 0 {
				t.Errorf("a log line reached the terminal while detached: %q", stderr.String())
			}
		})
	}
}
