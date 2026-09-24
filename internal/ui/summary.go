package ui

import (
	"fmt"

	"github.com/maogou/ohmyssh/internal/sshclient"
)

// TransferSummary is what there is to say about a transfer that has finished.
type TransferSummary struct {
	Direction sshclient.Direction
	// From and To are the two ends as the user named them.
	From, To string
	Files    int
	Bytes    int64
	// Err, when set, is why the transfer failed. The line then reports that
	// instead of the counts: a transfer that stopped early has no total worth
	// quoting.
	Err error
}

// Line renders a transfer the way both the command line and the browser say it,
// so the same operation reads the same in either.
func (s TransferSummary) Line() string {
	head := fmt.Sprintf("%s %s → %s", s.Direction.Verb(), s.From, s.To)
	if s.Err != nil {
		return fmt.Sprintf("%s: %v", head, s.Err)
	}
	return fmt.Sprintf("%s  %s  %s", head, plural(s.Files, "file"), HumanBytes(s.Bytes))
}

// TransferEnds orders the two ends of a transfer the way the bytes travel, so a
// summary reads forwards whichever direction it went. Both front ends quote the
// ends through here, which is what keeps them saying the same thing.
func TransferEnds(direction sshclient.Direction, local, remote string) (from, to string) {
	if direction == sshclient.Download {
		return remote, local
	}
	return local, remote
}

// Report is the finished line a command prints for a transfer that worked: Line
// with the tick and the colour in front of it, both of which belong to the
// renderer rather than to the wording.
//
// A transfer that failed is not reported this way. Its error goes back up the
// call chain instead, so the runner turns it into one prefixed line and an exit
// status rather than the two lines a report of it would produce.
//
// The styles collapse to plain text when the output is not a terminal, which is
// what keeps a redirected run free of escape codes.
func (s TransferSummary) Report() string {
	return statusStyle.Render(statusMarkerOK + s.Line())
}

// HumanBytes renders a byte count the way a person reads it. The steps are the
// 1024 the sizes of files are actually counted in, labelled the way they are
// usually written.
func HumanBytes(n int64) string {
	const unit = 1024

	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	value := float64(n)
	step := -1
	for value >= unit && step < len(units)-1 {
		value /= unit
		step++
	}
	// A decimal below ten is what makes "4.2 MB" readable, and dropping it
	// above keeps "512 KB" from carrying a digit it does not have.
	if value < 10 {
		return fmt.Sprintf("%.1f %s", value, units[step])
	}
	return fmt.Sprintf("%.0f %s", value, units[step])
}

// plural counts a noun, so a one-file transfer does not read as "1 files".
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// humanRate renders a transfer rate, which is a byte count per second.
func humanRate(bytesPerSecond float64) string {
	if bytesPerSecond <= 0 {
		return ""
	}
	return HumanBytes(int64(bytesPerSecond)) + "/s"
}

// humanETA renders time remaining the way a progress bar should: coarse while
// the estimate is rough, and blank when there is nothing to estimate from.
func humanETA(seconds float64) string {
	if seconds <= 0 {
		return ""
	}
	switch {
	case seconds < 60:
		return fmt.Sprintf("%.0fs", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%dm%02ds", int(seconds)/60, int(seconds)%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(seconds)/3600, (int(seconds)%3600)/60)
	}
}
