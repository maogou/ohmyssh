// Package logging configures the process-wide zerolog logger.
//
// Logs go to stderr so they never contaminate a remote command's stdout, which
// matters for `ohmyssh exec host -- cmd` being used in a pipeline.
package zlog

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// Options controls logger construction.
type Options struct {
	Level  string // trace, debug, info, warn, error, fatal, panic, disabled
	Pretty bool   // human-readable console output instead of JSON
	Stderr io.Writer
	// TimeFormat is how a console line stamps its time, in Go's layout syntax. It
	// is empty by default, which is time.TimeOnly: a terminal is watched as it
	// happens, so the hour and minute are what a line there needs. A file is read
	// the next day as well, so Detach asks for the date as well as the time. It
	// says nothing about the JSON format, which stamps RFC 3339 either way.
	TimeFormat string
}

// logger is the process-wide instance, swapped in by Setup. It starts at info
// so that debug output cannot leak before Setup runs.
var logger = zerolog.New(os.Stderr).Level(zerolog.InfoLevel).With().Timestamp().Logger()

// current is the last Setup, kept so that Detach can build the same logger
// against a different writer: where the logs go is the only thing a full-screen
// view changes about them.
var current Options

// output is where the logger writes, kept beside it because the two move
// together. Output hands it to the writers that are not log lines.
var output io.Writer = os.Stderr

// fileTimeFormat stamps a line in the log file with the day it was written on,
// which the hour and minute a terminal is shown do not say.
//
// The seconds are as fine as the console writer goes: it hands the time to its
// formatter as a string stamped with zerolog.TimeFieldFormat, which carries no
// fraction, so a layout asking for milliseconds would print ".000" down every
// line — a precision the file does not have. Millisecond lines would mean putting
// the fraction into that field format, which is also what a --log-format json
// run writes.
const fileTimeFormat = "2006-01-02 15:04:05"

// Setup builds the global logger and returns it. An unparsable level is
// reported by falling back to info rather than aborting, so a typo in
// --log-level never prevents a connection attempt.
func Setup(opts Options) zerolog.Logger {
	built, out, err := build(opts)
	logger = built
	output = out
	current = opts
	if err != nil {
		logger.Warn().Str("value", opts.Level).Msg("unrecognised log level, defaulting to info")
	}
	return logger
}

// Detach points the global logger at a file, for as long as something other than
// this process owns the terminal, and returns the function that gives stderr
// back.
//
// A full-screen view draws with the alt screen and reads the keyboard in raw
// mode, so a line written to the terminal lands in the middle of a frame: the
// renderer repaints only the lines that changed and moves the cursor by the count
// it believes it drew, so every write leaves a row of the frame above it on
// screen. One line of corruption per line of log, which for a view that dials on
// every open is a new line every time the user opens it. Logs go to a file while
// the view is up instead, which is also where a report of what it did is worth
// reading afterwards.
//
// The level and the console-or-JSON choice are the ones Setup installed. The time
// stamp gains the date, because a file is read on a later day than the one it was
// written on and an hour and minute would not say which. A path that cannot be
// opened silences the logs rather than falling back to stderr — the terminal is
// the one place they cannot go — and the returned function restores stderr either
// way.
func Detach(path string) func() {
	previous, previousOut := logger, output

	out, closeOut := logWriter(path)
	opts := current
	opts.Stderr = out
	opts.TimeFormat = fileTimeFormat
	// The error is the unparsable level Setup has already reported; there is
	// nowhere to report it again, and build falls back to info anyway.
	logger, output, _ = build(opts)

	return func() {
		logger, output = previous, previousOut
		closeOut()
	}
}

// build constructs the logger the options describe and hands back the writer it
// writes to as well — the one the options name, or stderr when they name none.
// An unparsable level falls back to info rather than aborting, so a typo in
// --log-level never prevents a connection attempt; the error is handed back for
// the caller to report.
func build(opts Options) (zerolog.Logger, io.Writer, error) {
	out := opts.Stderr
	if out == nil {
		out = os.Stderr
	}

	level, err := zerolog.ParseLevel(strings.ToLower(strings.TrimSpace(opts.Level)))
	if err != nil {
		level = zerolog.InfoLevel
	}

	writer := out
	if opts.Pretty {
		format := opts.TimeFormat
		if format == "" {
			format = time.TimeOnly
		}
		// NoColor keeps the console writer from emitting escapes when stderr is
		// redirected; zerolog's own detection is unreliable across platforms.
		writer = zerolog.ConsoleWriter{
			Out:        out,
			TimeFormat: format,
			NoColor:    !isTerminal(out),
		}
	}

	return zerolog.New(writer).Level(level).With().Timestamp().Logger(), out, err
}

// logWriter opens the file the logs are going to, creating the directory it sits
// in the way the rest of ohmyssh's files are created: 0700 for the directory and
// 0600 for the file, since a log names the hosts the user connects to.
//
// A path that cannot be opened — no home directory to name, a directory that
// cannot be created, a file that is not writable — leaves the logs going nowhere
// at all rather than to the terminal they were taken off. The second return is
// what gives the file back; it is never nil, so the caller can always call it.
func logWriter(path string) (io.Writer, func()) {
	if path == "" {
		return io.Discard, func() {}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return io.Discard, func() {}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return io.Discard, func() {}
	}
	return file, func() { _ = file.Close() }
}

// Output is where the logs are going now: stderr, or the file Detach opened for
// as long as a view owns the terminal.
//
// Text that is not a log line — a server's login banner, the stderr of a
// ProxyCommand — belongs here rather than straight to os.Stderr, because the
// terminal is the one place it cannot go while a view is drawing on it: the
// renderer moves the cursor by the lines it believes it drew, so anything printed
// beside the frame leaves it shifted by a row. Nothing detached hands the writer
// the options named, which is stderr itself unless a caller chose another, so a
// command line's output does not change.
func Output() io.Writer { return output }

// L returns the configured logger.
func L() *zerolog.Logger { return &logger }

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
