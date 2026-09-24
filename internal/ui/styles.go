// Package ui renders the interactive host browser and hands the terminal over
// to an SSH session when a host is chosen.
package ui

import "github.com/charmbracelet/lipgloss"

// Adaptive colours keep the browser legible on both light and dark terminals.
var (
	colorAccent  = lipgloss.AdaptiveColor{Light: "#6C4FD8", Dark: "#B39DFF"}
	colorMuted   = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#8B8B8B"}
	colorSubtle  = lipgloss.AdaptiveColor{Light: "#D1D5DB", Dark: "#4A4A4A"}
	colorSuccess = lipgloss.AdaptiveColor{Light: "#0F7B3F", Dark: "#5FD787"}
	colorDanger  = lipgloss.AdaptiveColor{Light: "#B91C1C", Dark: "#FF7B72"}

	// The selected row is painted as a bar rather than merely coloured text, so
	// it needs its own pair: a foreground accent on an accent background would
	// have no contrast.
	colorBarBg = lipgloss.AdaptiveColor{Light: "#EDE9FE", Dark: "#332C4D"}
	colorBarFg = lipgloss.AdaptiveColor{Light: "#4C1D95", Dark: "#F0EBFF"}
)

// Markers leading the status line, kept here so the renderer reads as layout
// rather than as glyphs.
const (
	statusMarkerOK     = "✓ "
	statusMarkerFailed = "✗ "
	// statusMarkerAsk leads a question the browser is waiting on an answer to.
	// The line it leads holds until it is answered, so it is marked as something
	// outstanding rather than as something that happened.
	statusMarkerAsk = "? "
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAccent)

	countStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	// cursorStyle marks the row the user is about to connect to.
	cursorStyle = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true)

	nameStyle = lipgloss.NewStyle().
			Bold(true)

	// rowSelectedStyle paints the whole selected row, padding included, so the
	// bar reaches the right edge of the content column.
	rowSelectedStyle = lipgloss.NewStyle().
				Background(colorBarBg).
				Foreground(colorBarFg).
				Bold(true)

	metaStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	tagStyle = lipgloss.NewStyle().
			Foreground(colorAccent).
			Faint(true)

	// headerStyle labels the table columns. It stays quieter than the data under
	// it so the eye lands on the hosts first.
	headerStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Bold(true)

	// ruleStyle draws the hairlines above and below the table header; they stay
	// quieter than the text they separate.
	ruleStyle = lipgloss.NewStyle().
			Foreground(colorSubtle)

	hintStyle = lipgloss.NewStyle().
			Foreground(colorSubtle)

	keyStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Bold(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(colorDanger).
			Bold(true)

	// questionStyle marks the line the browser is waiting on an answer to. It is
	// neither the green of something done nor the red of something failed, but it
	// is the only thing on screen asking for a keypress, so it carries the accent.
	questionStyle = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true)

	emptyStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Italic(true)

	// statusStyle reports the outcome of the session that just ended.
	statusStyle = lipgloss.NewStyle().
			Foreground(colorSuccess)
)
