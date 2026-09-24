package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"

	"github.com/maogou/ohmyssh/internal/i18n"
	"github.com/maogou/ohmyssh/internal/sshclient"
)

const (
	// liveIndent is what the two live lines spend before their labels, and
	// liveGap what separates the label, the bar and the tail from each other.
	liveIndent = "  "
	liveGap    = "  "
	// minBarWidth is the narrowest a bar is allowed to get before the labels and
	// the rate are given up to make room for it.
	minBarWidth = 10
	// maxLabelWidth stops one long path from squeezing the bar off the line.
	maxLabelWidth = 24
)

// transferBar is the bar both front ends draw. It is built once because only its
// width changes from frame to frame, and liveFrame sets that.
//
// The fill is a fixed colour rather than one of the palette's adaptive pairs:
// bubbles/progress hands its colour to termenv as a plain string and will not
// take a lipgloss.AdaptiveColor, so the bar does not follow the light and dark
// halves the rest of the browser does.
var transferBar = progress.New()

// TransferProgress draws a transfer's progress on a writer.
//
// On a terminal it redraws a bar in place, one line for the file being copied
// and one for the transfer as a whole. Anywhere else — a pipe, a CI log, a file
// — a stream of cursor moves is unreadable once captured, so it prints one line
// per completed file instead.
//
// What it writes is dropped on the floor rather than reported: the bar is a
// convenience, and a terminal that has gone away — a closed pipe, a lost SSH
// session carrying the output — is not a reason to fail a transfer that is
// otherwise copying perfectly well.
type TransferProgress struct {
	out   io.Writer
	width int
	// drawn records that the live lines are on screen, so the next frame knows
	// to move back up over them.
	drawn bool
}

// NewTransferProgress returns a reporter that writes to out.
//
// The totals a bar needs are not passed in: they arrive with the first report,
// which the transfer sends once it has measured its plan.
func NewTransferProgress(out io.Writer) *TransferProgress {
	return &TransferProgress{out: out, width: terminalWidth(out)}
}

// Update draws what the report says. It is safe to call from the copy loop: the
// work is one formatted line, and a report that changes nothing visible is not
// redrawn.
func (p *TransferProgress) Update(prog sshclient.Progress) {
	if p.width <= 0 {
		if prog.Done {
			_, _ = fmt.Fprintf(p.out, "%9s  %s\n", HumanBytes(prog.FileTotal), prog.File)
		}
		return
	}

	lines := liveFrame(prog, p.width)
	var b strings.Builder
	if p.drawn {
		// Back up over the pair the previous frame left, then rewrite both.
		b.WriteString("\x1b[1A")
	}
	for i, line := range lines {
		if i > 0 {
			b.WriteString("\n")
		}
		// \r puts the cursor back at the margin and \x1b[2K wipes what the last
		// frame left there, so a shorter line does not leave a tail behind.
		b.WriteString("\r\x1b[2K")
		b.WriteString(line)
	}
	p.drawn = true
	_, _ = fmt.Fprint(p.out, b.String())
}

// Done closes the live display, leaving the cursor on a fresh line so whatever
// is printed next is not written over the progress bar.
func (p *TransferProgress) Done() {
	if p.width <= 0 || !p.drawn {
		return
	}
	_, _ = fmt.Fprint(p.out, "\n")
	p.drawn = false
}

// liveFrame renders the two live lines — the file being copied and the transfer
// as a whole — fitted to width columns.
//
// Both front ends draw through here: the command line writes the lines to stderr
// and the browser puts them where the host table was, which is what makes a
// transfer look the same in either. The lines carry their own indent, so width is
// the whole line and not just the part to the right of the margin.
//
// Two lines is part of the contract: the live display rewrites a frame by moving
// the cursor back up over exactly that many, so changing the count means changing
// Update too.
func liveFrame(prog sshclient.Progress, width int) []string {
	fileLabel := clip(shortPath(prog.File), maxLabelWidth)
	allLabel := i18n.M().Files(prog.FilesTotal)

	fileTail := strings.TrimSpace(humanRate(prog.Rate) + "  " + etaText(prog.ETA))
	allTail := fmt.Sprintf("%s / %s", HumanBytes(prog.TotalDone), HumanBytes(prog.TotalBytes))
	if prog.FilesTotal > 0 {
		allTail = fmt.Sprintf("%d/%d  %s", prog.FilesDone, prog.FilesTotal, allTail)
	}

	// Both lines share a label width and a tail width so the two bars start and
	// end at the same column: a pair of bars that do not line up reads as noise.
	labelWidth := min(max(lipgloss.Width(fileLabel), lipgloss.Width(allLabel)), maxLabelWidth)
	tailWidth := max(lipgloss.Width(fileTail), lipgloss.Width(allTail))

	// What is left for a bar once the labels and the tails have taken their
	// columns, gaps included. A live line that overruns its width wraps, and a
	// frame that has wrapped cannot be rewritten in place: the cursor moves back
	// over one row and lands in the middle of the line above.
	gap := lipgloss.Width(liveGap)
	fitting := func(labels, tails bool) int {
		left := width - lipgloss.Width(liveIndent)
		if labels {
			left -= labelWidth + gap
		}
		if tails {
			left -= tailWidth + gap
		}
		return left
	}

	barWidth := fitting(true, true)
	if barWidth < minBarWidth {
		// No room for the rate and the ETA; the bars matter more.
		fileTail, allTail = "", ""
		tailWidth = 0
		barWidth = fitting(true, false)
	}
	if barWidth < minBarWidth {
		// Not even the labels fit. The bars carry the meaning on their own.
		fileLabel, allLabel = "", ""
		labelWidth = 0
		barWidth = fitting(false, false)
	}
	// The floor is deliberate: below it there is no progress left to show, so
	// the line overruns rather than drawing a bar that says nothing.
	barWidth = max(barWidth, minBarWidth)

	bar := transferBar
	bar.Width = barWidth
	return []string{
		liveLine(fileLabel, labelWidth, bar.ViewAs(percent(prog.FileDone, prog.FileTotal)), fileTail, tailWidth),
		liveLine(allLabel, labelWidth, bar.ViewAs(percent(prog.TotalDone, prog.TotalBytes)), allTail, tailWidth),
	}
}

// liveLine lays one line out on the shared grid.
func liveLine(label string, labelWidth int, bar, tail string, tailWidth int) string {
	line := liveIndent
	if labelWidth > 0 {
		line += padTo(label, labelWidth, false) + liveGap
	}
	line += bar
	if tailWidth > 0 {
		line += liveGap + padTo(tail, tailWidth, false)
	}
	return line
}

// percent is a fraction guarded against the two ways it can have no meaning: a
// transfer with nothing to move, and a file that grew while it was being read.
func percent(done, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return min(float64(done)/float64(total), 1)
}

// etaText says how long is left, or nothing while the estimate is still too
// rough to be worth reading.
func etaText(eta time.Duration) string {
	if text := humanETA(eta.Seconds()); text != "" {
		return fmt.Sprintf(i18n.M().ETA, text)
	}
	return ""
}

// shortPath trims a path to its last two elements, which is what tells one file
// in a transfer apart from the next without the line being nothing but names.
func shortPath(path string) string {
	parts := strings.Split(strings.TrimSuffix(path, "/"), "/")
	if len(parts) <= 2 {
		return path
	}
	return strings.Join(parts[len(parts)-2:], "/")
}

// terminalWidth is the width to draw a live line to, or zero when out is not a
// terminal — which is the signal to fall back to one line per file.
func terminalWidth(out io.Writer) int {
	file, ok := out.(*os.File)
	if !ok {
		return 0
	}
	width, _, err := term.GetSize(int(file.Fd()))
	if err != nil || width <= 0 {
		return 0
	}
	return width
}
