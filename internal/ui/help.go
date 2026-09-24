package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/maogou/ohmyssh/internal/i18n"
)

// The help screen is the key bar written out in full. The bar is a budget: it
// gives its segments up from the right as the line runs out, so at 80 columns
// the binding the user cannot guess can be the one missing from it. This is the
// list they are missing from, for the view they are looking at.
//
// It is a whole frame of its own rather than a region of the one under it —
// like the form and the file view — and the view it covers is left exactly as
// it was: closing the help is what goes back to it.
//
// Only the list and the file view have a help. The form and the delete question
// are typed into and answered rather than driven by keys, and what they answer
// to is the line under them.

// binding is one key and what it does. Both the key bar and the help are built
// out of them, so the two cannot come to describe the same key differently.
type binding struct {
	key  string
	hint string
}

// keyHint renders one binding. The key is the loud half and the hint the quiet
// one, so a bar reads as keys with explanations rather than as a sentence.
func keyHint(b binding) string {
	return keyStyle.Render(b.key) + " " + hintStyle.Render(b.hint)
}

const (
	// helpGap is the space between the key column and what the key does, wide
	// enough to read as a gap rather than as part of either.
	helpGap = 2
)

// listHelp is every key the host list answers to. The bar's order is the order
// its segments are given up in; this is the order they are read in: what moves
// the cursor, what a keystroke does to the filter, then what can be done to the
// host under it.
func (m browserModel) listHelp() []binding {
	messages := i18n.M()
	help := []binding{
		{"enter", messages.HelpConnect},
		{"↑↓", messages.HelpMove},
		{"pgup pgdown", messages.HelpPageHosts},
		{"home end", messages.HelpEndsHosts},
		{"type", messages.HelpType},
		{"esc", messages.HelpEsc},
		{"q", messages.HelpQuit},
		{"ctrl+c", messages.HelpQuitAnywhere},
	}
	if m.add != nil {
		hint := messages.HelpAdd
		if m.hostsFile != "" {
			hint = fmt.Sprintf(messages.HelpAddWrittenTo, abbreviated(m.hostsFile))
		}
		help = append(help, binding{"A", hint})
	}
	if m.remove != nil {
		help = append(help, binding{"X", messages.HelpDelete})
	}
	if m.remote != nil {
		// The bar has room for "U/D files" and no more, so which of the two keys
		// takes which pane is said here and nowhere else. This is the longest hint
		// in the table: a word more and an 80-column terminal cuts the pane off
		// the end of it, which is the part the row exists to name.
		help = append(help, binding{"U/D", messages.HelpFiles})
	}
	return append(help, binding{"?", messages.HelpTheseKeys})
}

// filesHelp is every key the file view answers to. The bindings that are not in
// its bar are here too, since the help is read for the keys a user has not
// found rather than for the ones they have.
func filesHelp() []binding {
	m := i18n.M()
	return []binding{
		{"tab", m.HelpTab},
		{"↑↓ k j", m.HelpMoveEntries},
		{"pgup pgdown", m.HelpPageEntries},
		{"g G home end", m.HelpEndsEntries},
		{"enter", m.HelpEnterEntry},
		{"c", m.HelpCopyEntry},
		{"← h backspace", m.HelpParent},
		{"/", m.HelpFind},
		{"r", m.HelpReload},
		{"esc", m.HelpEscFiles},
		{"ctrl+c", m.HelpQuitAnywhere},
		{"?", m.HelpTheseKeys},
	}
}

// helpBindings is the keys of the view the help was opened from.
func (m browserModel) helpBindings() []binding {
	if m.helpFor == modeFiles {
		return filesHelp()
	}
	return m.listHelp()
}

// helpFooterSegments are the ways out of the help. Both of them are listed: esc
// is what leaves anything in this program, and ? is what the user pressed to
// get here and is the first thing they are likely to press again.
func helpFooterSegments() []binding {
	m := i18n.M()
	return []binding{
		{"esc", m.HelpClose},
		{"?", m.HelpClose},
	}
}

// helpView draws the help on the frame of the view it describes: the same
// header, the same rule, the same key hints, with the keys where the content
// was. It holds the terminal's height like every other frame.
func (m browserModel) helpView() string {
	width := m.contentWidth()
	lines := []string{
		margin + m.helpHeader(width),
		rule(width),
	}
	lines = append(lines, m.helpLines(width, 3+m.listHeight())...)
	lines = append(lines, margin+m.footer(width))
	return strings.Join(cutFrame(lines, m.height), "\n")
}

// helpHeader names the help and the view it is of, with the same note that view
// carries at its own right-hand end: the files the hosts came from, or the
// address the panes are open on.
func (m browserModel) helpHeader(width int) string {
	left := titleStyle.Render("ohmyssh") + "  " +
		countStyle.Render(fmt.Sprintf(i18n.M().HelpKeys, m.helpSubject()))
	return headerLine(left, m.helpNote(), width)
}

// helpSubject is the view being described, in the words the view's own header
// uses for itself.
func (m browserModel) helpSubject() string {
	if m.helpFor == modeFiles {
		return i18n.M().HelpFileView
	}
	return i18n.M().HelpHostList
}

// helpNote is the note at the right-hand end of the header, which for the file
// view is the host the panes are open on.
func (m browserModel) helpNote() string {
	if m.helpFor != modeFiles {
		return sourceHint(m.hosts)
	}
	note := m.files.host.DisplayUser() + "@" + hostName(m.files.host)
	if note == "@" {
		return ""
	}
	return note
}

// helpLines draws the bindings into the region of the frame the table takes in
// the list, and pads it to exactly rows so that a frame whose height was
// counted without them keeps its ends.
//
// A binding that does not fit is left out, which is the bargain the form makes
// with its fields as well: a terminal too short for the whole list is too short
// for any of it, and the first rows are the ones worth reading.
func (m browserModel) helpLines(width, rows int) []string {
	bindings := m.helpBindings()
	keyWidth := helpKeyWidth(bindings)
	// The hint is clipped rather than left to wrap: a line wider than the frame
	// wraps, and a wrapped line pushes everything below it past the bottom of a
	// frame whose height was counted without it.
	room := max(width-rowIndent-keyWidth-helpGap, 0)

	lines := []string{""}
	for _, b := range bindings {
		if len(lines) >= rows {
			break
		}
		lines = append(lines, margin+strings.Repeat(" ", rowIndent)+
			keyStyle.Render(padTo(b.key, keyWidth, false))+
			strings.Repeat(" ", helpGap)+
			metaStyle.Render(clip(b.hint, room)))
	}
	for len(lines) < rows {
		lines = append(lines, "")
	}
	return lines
}

// helpKeyWidth is the column the keys are padded to, measured from the bindings
// themselves so that a longer one cannot push the hints out of line.
func helpKeyWidth(bindings []binding) int {
	width := 0
	for _, b := range bindings {
		width = max(width, lipgloss.Width(b.key))
	}
	return width
}
