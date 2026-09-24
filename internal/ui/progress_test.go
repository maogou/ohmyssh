package ui

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/maogou/ohmyssh/internal/sshclient"
)

// busyProgress is a transfer partway through: two files of different sizes, a
// rate and an estimate, which is what the live lines have to fit around.
func busyProgress() sshclient.Progress {
	return sshclient.Progress{
		File:       "internal/sshclient/transfer.go",
		FileDone:   4096,
		FileTotal:  8192,
		TotalDone:  120000,
		TotalBytes: 400000,
		FilesDone:  3,
		FilesTotal: 12,
		Rate:       1_200_000,
		ETA:        95 * time.Second,
	}
}

// barStart is the display column a line's bar begins at, which is how the two
// lines are compared: a pair of bars that do not line up reads as noise.
func barStart(t *testing.T, line string) int {
	t.Helper()

	idx := strings.IndexAny(line, "█░")
	if idx < 0 {
		t.Fatalf("no bar in %q", line)
	}
	return lipgloss.Width(line[:idx])
}

func barEnd(t *testing.T, line string) int {
	t.Helper()

	idx := strings.LastIndexAny(line, "█░")
	if idx < 0 {
		t.Fatalf("no bar in %q", line)
	}
	return lipgloss.Width(line[:idx+len("░")])
}

// A live line that overruns its width wraps, and a frame that has wrapped cannot
// be rewritten in place: the redraw moves the cursor back over one row and lands
// in the middle of the line above. So each line has to fit its terminal exactly.
//
// The floor is the one exception, and it is stated rather than hidden: below
// minBarWidth + liveIndent columns there is no progress left to show, so the
// line overruns instead of drawing a bar that says nothing.
func TestLiveFrameFitsItsWidth(t *testing.T) {
	shapes := map[string]sshclient.Progress{
		"partway through":  busyProgress(),
		"before it starts": {},
		"at the end": {
			File: "a.txt", FileDone: 10, FileTotal: 10,
			TotalDone: 10, TotalBytes: 10, FilesDone: 1, FilesTotal: 1,
		},
		// A name long enough that the label has to be clipped, and a rate high
		// enough to push the tail wide.
		"a very long name": {
			File: strings.Repeat("deeply/nested/", 8) + "file.go", FileDone: 1, FileTotal: 2,
			TotalDone: 1, TotalBytes: 2, FilesDone: 0, FilesTotal: 3,
			Rate: 999_000_000, ETA: 3*time.Hour + 20*time.Minute,
		},
	}

	for name, prog := range shapes {
		for _, width := range []int{200, 120, 100, 80, 60, 40, 24, 16, 12} {
			for i, line := range liveFrame(prog, width) {
				if got := lipgloss.Width(line); got != width {
					t.Errorf("%s: line %d at width %d is %d columns: %q", name, i, width, got, line)
				}
			}
		}
	}
}

// Both lines are laid out on one grid, so the two bars start and end together
// whatever the labels and the tails take.
func TestLiveFrameBarsShareOneColumn(t *testing.T) {
	shapes := []sshclient.Progress{
		busyProgress(),
		{},
		{File: "a", FileDone: 1, FileTotal: 2, FilesDone: 0, FilesTotal: 1},
	}

	for _, prog := range shapes {
		for _, width := range []int{120, 80, 60, 40, 24} {
			lines := liveFrame(prog, width)
			if got, want := barStart(t, lines[0]), barStart(t, lines[1]); got != want {
				t.Errorf("width %d: bars start at %d and %d: %q / %q", width, got, want, lines[0], lines[1])
			}
			// The end is only comparable while the percentage is shown on both,
			// which the floor is what takes away.
			if lipgloss.Width(lines[0]) == width && lipgloss.Width(lines[1]) == width {
				if got, want := barEnd(t, lines[0]), barEnd(t, lines[1]); got != want {
					t.Errorf("width %d: bars end at %d and %d: %q / %q", width, got, want, lines[0], lines[1])
				}
			}
		}
	}
}

// A live frame is always two lines: the redraw moves the cursor back up over
// exactly that many before rewriting them.
func TestLiveFrameIsAlwaysTwoLines(t *testing.T) {
	if got := len(liveFrame(busyProgress(), 80)); got != 2 {
		t.Errorf("liveFrame returned %d lines, want 2", got)
	}
}

// What the lines give up as the terminal narrows: the rate and the estimate
// first, then the labels, and the bars only at the point where they stop
// meaning anything.
func TestLiveFrameGivesUpDecorationBeforeMeaning(t *testing.T) {
	prog := busyProgress()

	wide := liveFrame(prog, 120)
	if !strings.Contains(wide[0], "MB/s") || !strings.Contains(wide[0], "ETA") {
		t.Errorf("a wide frame dropped the rate and the estimate:\n%s\n%s", wide[0], wide[1])
	}
	if !strings.Contains(wide[0], "transfer.go") {
		t.Errorf("a wide frame dropped the file name:\n%s", wide[0])
	}

	// Narrow enough for the tails to go, but not for the labels.
	narrow := liveFrame(prog, 40)
	for i, line := range narrow {
		if strings.Contains(line, "MB/s") || strings.Contains(line, "ETA") {
			t.Errorf("line %d kept the tail at 40 columns: %q", i, line)
		}
	}
	// The bars are still the longest thing on the line, which is the trade.
	if got := barEnd(t, narrow[0]) - barStart(t, narrow[0]); got < minBarWidth {
		t.Errorf("the bar is %d columns at 40, want at least %d", got, minBarWidth)
	}
}

// A transfer's first report has no history behind it, so the rate and the
// estimate are empty rather than zero: a bar reading "0 B/s" would be a lie.
func TestLiveFrameOmitsAnUnknownRate(t *testing.T) {
	for _, line := range liveFrame(sshclient.Progress{}, 80) {
		if strings.Contains(line, "B/s") || strings.Contains(line, "ETA") {
			t.Errorf("a transfer that has not started reports a rate: %q", line)
		}
	}
}

// A long path is cut from the left of the label rather than pushing the bar
// along: the last two elements are what tell one file apart from the next.
func TestShortPathKeepsTheEndOfAPath(t *testing.T) {
	tests := []struct{ path, want string }{
		{"a.txt", "a.txt"},
		{"dist/a.txt", "dist/a.txt"},
		{"deep/dist/a.txt", "dist/a.txt"},
		{"/var/log/app.log", "log/app.log"},
		// Two elements is already as short as it gets, trailing slash included.
		{"dist/", "dist/"},
	}
	for _, tt := range tests {
		if got := shortPath(tt.path); got != tt.want {
			t.Errorf("shortPath(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

// percent guards the two ways a fraction can have no meaning: nothing to move,
// and a file that grew while it was being read.
func TestPercentIsBounded(t *testing.T) {
	tests := []struct {
		done, total int64
		want        float64
	}{
		{0, 0, 0},
		{5, 0, 0},
		{0, 10, 0},
		{5, 10, 0.5},
		{10, 10, 1},
		{15, 10, 1},
	}
	for _, tt := range tests {
		if got := percent(tt.done, tt.total); got != tt.want {
			t.Errorf("percent(%d, %d) = %v, want %v", tt.done, tt.total, got, tt.want)
		}
	}
}

// Redirection, a pipe and a CI log all get the same thing: a cursor moving up
// and rewriting lines is unreadable once it has been captured, so the display
// falls back to one line per finished file.
func TestProgressWithoutATerminalPrintsOneLinePerFile(t *testing.T) {
	var out bytes.Buffer
	p := NewTransferProgress(&out)

	// The intermediate reports of a file are not worth a line each.
	p.Update(sshclient.Progress{File: "a.txt", FileDone: 512, FileTotal: 2048, Done: false})
	if out.Len() != 0 {
		t.Fatalf("a part-way report was printed: %q", out.String())
	}

	p.Update(sshclient.Progress{File: "a.txt", FileDone: 2048, FileTotal: 2048, Done: true})
	p.Update(sshclient.Progress{File: "b.txt", FileDone: 4096, FileTotal: 4096, Done: true})
	p.Done()

	// The size is right-aligned so the names line up down the column.
	want := "   2.0 KB  a.txt\n   4.0 KB  b.txt\n"
	if got := out.String(); got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
	if strings.ContainsAny(out.String(), "\r\x1b") {
		t.Errorf("the fallback wrote terminal control codes: %q", out.String())
	}
}

// On a terminal the frame is rewritten in place, which is what the escape
// sequences are for. The width is set directly: a test has no terminal to
// measure, and NewTransferProgress is the only thing that looks for one.
func TestProgressRedrawsInPlaceOnATerminal(t *testing.T) {
	var out bytes.Buffer
	p := &TransferProgress{out: &out, width: 80}

	p.Update(busyProgress())
	first := out.String()
	if strings.Contains(first, "\x1b[1A") {
		t.Errorf("the first frame moved the cursor up over nothing: %q", first)
	}
	if got := strings.Count(first, "\r\x1b[2K"); got != 2 {
		t.Errorf("the first frame cleared %d lines, want 2: %q", got, first)
	}
	// Both lines are on screen, so this is where the cursor ends up.
	if !p.drawn {
		t.Error("the display does not know it has drawn a frame")
	}

	out.Reset()
	p.Update(busyProgress())
	second := out.String()
	if !strings.HasPrefix(second, "\x1b[1A") {
		t.Errorf("the second frame did not move back up over the first: %q", second)
	}
	if got := strings.Count(second, "\r\x1b[2K"); got != 2 {
		t.Errorf("the second frame cleared %d lines, want 2: %q", got, second)
	}
}

// Whatever is printed after the bar has to start on a line of its own, or the
// summary lands on top of the progress display.
func TestDoneLeavesTheCursorOnAFreshLine(t *testing.T) {
	var out bytes.Buffer
	p := &TransferProgress{out: &out, width: 80}

	p.Update(busyProgress())
	out.Reset()
	p.Done()
	if got := out.String(); got != "\n" {
		t.Errorf("Done() wrote %q, want a single newline", got)
	}
	if p.drawn {
		t.Error("the display still thinks a frame is on screen")
	}

	// Done is called on every failing path too, including ones that drew
	// nothing. A blank line before the error would be noise.
	out.Reset()
	p.Done()
	if out.Len() != 0 {
		t.Errorf("a second Done() wrote %q, want nothing", out.String())
	}
}
