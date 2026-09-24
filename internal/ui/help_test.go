package ui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/maogou/ohmyssh/internal/config"
)

// helpBrowser is a browser with every service attached, which is the one the
// program assembles for itself: the bar at its longest and the help at its
// fullest, and so the case both have to fit in.
func helpBrowser() browserModel {
	return sized(BrowserOptions{
		Hosts:     testHosts(),
		HostsFile: filepath.Join(homeDir, ".ohmyssh", "hosts"),
		Session:   func(context.Context, config.SSHHost) error { return nil },
		Remote: func(context.Context, config.SSHHost) (RemoteSession, error) {
			return newFakeSession(), nil
		},
		Add: func(context.Context, config.NewHost) ([]config.SSHHost, error) {
			return testHosts(), nil
		},
		Remove: func(context.Context, config.SSHHost) ([]config.SSHHost, error) {
			return testHosts(), nil
		},
	})
}

// The help is about the view it was opened from, so the list's is opened on the
// list and says what the list's keys are — including where a host added from
// here is written, which is the one thing about that key the bar has no room
// for.
func TestHelpShowsTheKeysOfTheViewItCameFrom(t *testing.T) {
	m := pressKey(t, helpBrowser(), rune2('?'))

	if m.mode != modeHelp {
		t.Fatalf("? put the browser in mode %v, want the help", m.mode)
	}
	view := m.View()
	for _, want := range []string{
		"keys · host list",
		"connect to the host under the cursor",
		"filter by name, host, user or tag",
		"add a host, written to ~/.ohmyssh/hosts",
		"delete the host under the cursor",
		"open the file view",
		"this list of keys",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the list's help does not say %q:\n%s", want, view)
		}
	}
	// The file view has a help of its own, and its keys have no business on
	// this one.
	if strings.Contains(view, "switch panes") {
		t.Errorf("the list's help lists the file view's keys:\n%s", view)
	}
}

func TestHelpFromTheFileViewIsAboutTheFileView(t *testing.T) {
	m := pressKey(t, openFileView(t, newFakeSession()), rune2('?'))

	if m.mode != modeHelp {
		t.Fatalf("? in the file view put the browser in mode %v, want the help", m.mode)
	}
	view := m.View()
	for _, want := range []string{
		"keys · file view",
		"switch panes",
		"up to the parent directory",
		"cancel the transfer",
		// The note at the right-hand end of the header is the host the panes are
		// open on, as it is in the view's own header.
		"deploy@10.0.0.1",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the file view's help does not say %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "keys · host list") {
		t.Errorf("the file view's help describes the host list:\n%s", view)
	}
}

// Closing the help puts the user back on the view it covered, which is still
// exactly as they left it — including the session the file view was browsing
// over.
func TestHelpClosesBackIntoTheViewItCameFrom(t *testing.T) {
	// ? is what opened it and what closes it again.
	m := pressKey(t, helpBrowser(), rune2('?'))
	m = pressKey(t, m, rune2('?'))
	if m.mode != modeList {
		t.Fatalf("mode = %v after ? ? , want the host list", m.mode)
	}
	if !m.filter.Focused() {
		t.Error("the host filter did not take the keyboard back")
	}

	session := newFakeSession()
	m = pressKey(t, openFileView(t, session), rune2('?'))
	m = pressKey(t, m, key(tea.KeyEsc))

	if m.mode != modeFiles {
		t.Fatalf("mode = %v after esc, want the file view", m.mode)
	}
	select {
	case <-session.closed:
		t.Error("closing the help closed the session under it")
	default:
	}
}

// A key pressed over a screen of key bindings is a key pressed by accident, and
// acting on the view the user cannot see is the one thing the help could do
// wrong. So nothing reaches that view: not the capitals, not enter, and not the
// characters of the filter.
func TestHelpSwallowsTheKeysOfTheViewUnderIt(t *testing.T) {
	m := pressKey(t, helpBrowser(), rune2('?'))

	for _, msg := range []tea.KeyMsg{
		rune2('q'), rune2('A'), rune2('X'), rune2('U'), rune2('D'), key(tea.KeyEnter),
	} {
		m = pressKey(t, m, msg)
		if m.quit {
			t.Fatalf("%q quit the browser from the help", msg)
		}
		if m.mode != modeHelp {
			t.Fatalf("%q left the help", msg)
		}
	}
	if got := m.filter.Value(); got != "" {
		t.Errorf("filter = %q, want the keystrokes kept out of the host filter", got)
	}
}

// The same, over the file view: a "c" pressed while reading the keys must not
// start the transfer it means in the panes underneath.
func TestHelpOverTheFileViewSwallowsItsKeys(t *testing.T) {
	session := newFakeSession()
	m := pressKey(t, openFileView(t, session), rune2('?'))

	for _, msg := range []tea.KeyMsg{rune2('c'), rune2('r'), key(tea.KeyEnter), key(tea.KeyTab)} {
		m = pressKey(t, m, msg)
		if m.mode != modeHelp {
			t.Fatalf("%q left the help", msg)
		}
	}
	select {
	case req := <-session.transfers:
		t.Errorf("the help let a transfer start: %+v", req)
	default:
	}
}

// The frame is a frame like any other: exactly the terminal's height, and no
// line wider than the terminal, however short the terminal is.
func TestHelpFrameFitsTheTerminal(t *testing.T) {
	for _, size := range []struct{ width, height int }{
		{80, 24}, {100, 30}, {200, 60}, {40, 12}, {24, 8},
	} {
		m := helpBrowser()
		updated, _ := m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		m = pressKey(t, updated.(browserModel), rune2('?'))

		lines := strings.Split(m.View(), "\n")
		if len(lines) != size.height {
			t.Fatalf("%dx%d: the frame is %d lines:\n%s",
				size.width, size.height, len(lines), m.View())
		}
		for i, line := range lines {
			if got := lipgloss.Width(line); got > size.width {
				t.Fatalf("%dx%d: line %d is %d columns, %d past the edge:\n%s",
					size.width, size.height, i, got, got-size.width, m.View())
			}
		}
	}
}

// A hint too long for its row is cut with an ellipsis rather than wrapped, which
// keeps the frame's height — but the words it loses are its last ones. At the
// usual 80-column terminal, then, a hint has a length, and this is it: a word
// added to one of these rows is a word nobody at 80 columns reads.
//
// The row this was written for is the list's U/D, whose point is which key takes
// which pane, and which ended at "...D on the remote" before the hint was cut
// down. The width the rows are measured against is the same one the bar is fitted
// to, so the two are held to one budget.
func TestHelpHintsSurviveTheUsualTerminalWidth(t *testing.T) {
	m := helpBrowser()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(browserModel)

	for name, bindings := range map[string][]binding{
		"host list": m.listHelp(),
		"file view": filesHelp(),
	} {
		room := m.contentWidth() - rowIndent - helpKeyWidth(bindings) - helpGap
		for _, b := range bindings {
			if got := lipgloss.Width(b.hint); got > room {
				t.Errorf("%s: the %q hint is %d columns, %d past the %d its row has at 80 columns",
					name, b.key, got, got-room, room)
			}
		}
	}
}

// keyTokens is the keys a key cell names, however the cell spells them: the bar
// has one line to spend on a binding and the help has a whole row, so "U/D" and
// "U D" are the same two keys and this is what says so.
func keyTokens(cell string) []string {
	// The file view's find key is a slash of its own, and a slash is also what
	// separates two spellings of one binding elsewhere.
	if cell == "/" {
		return []string{"/"}
	}
	return strings.FieldsFunc(cell, func(r rune) bool { return r == '/' || unicode.IsSpace(r) })
}

// The bar gives its segments up from the right as the line runs out, so a
// binding can be missing from it — that is what the help is for. The reverse is
// the failure worth a test: a key the bar offers that the help does not list is
// a key the user is told about and cannot then look up.
//
// The form and the delete question have no help on purpose: they are typed into
// and answered, and what they answer to is on the line under them.
func TestHelpCoversEveryKeyOnTheBar(t *testing.T) {
	withServices := helpBrowser()
	fileView := openFileView(t, newFakeSession()).files

	cases := map[string]struct {
		bar  []binding
		help []binding
	}{
		"host list": {withServices.footerSegments(), withServices.listHelp()},
		"file view": {fileView.footerSegments(), filesHelp()},
	}

	for name, tc := range cases {
		listed := make(map[string]bool)
		for _, b := range tc.help {
			for _, token := range keyTokens(b.key) {
				listed[token] = true
			}
		}
		for _, b := range tc.bar {
			for _, token := range keyTokens(b.key) {
				if !listed[token] {
					t.Errorf("%s: the bar offers %q, which the help does not list", name, token)
				}
			}
		}
	}
}

// The help is only reachable if the bar says so, and the bar at 80 columns is
// where that is hardest: the whole list of bindings has to survive it with the
// help on the end.
func TestHelpIsOnTheKeyBar(t *testing.T) {
	m := helpBrowser()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(browserModel)

	last := lastLine(m.View())
	if !strings.Contains(last, "help") {
		t.Errorf("the help is not on the list's bar at 80 columns: %q", last)
	}

	// And the file view's bar has room for its own, next to the way out.
	files := openFileView(t, newFakeSession()).files
	files.resize(80, 24)
	if last := lastLine(files.View()); !strings.Contains(last, "help") {
		t.Errorf("the help is not on the file view's bar: %q", last)
	}
}

// While the file view's filter line is up the keyboard is the filter's, so "?"
// is a character like any other — the bargain the host list makes with its
// capitals, made here with the whole keyboard.
func TestFileViewFilterKeepsTheQuestionMark(t *testing.T) {
	m := openFileView(t, newFakeSession())

	m = pressKey(t, m, rune2('/'))
	m = pressKey(t, m, rune2('?'))

	if m.mode != modeFiles {
		t.Fatalf("? while the filter line was up put the browser in mode %v", m.mode)
	}
	if got := m.files.filter.Value(); got != "?" {
		t.Errorf("the pane filter holds %q, want the %q that was typed into it", got, "?")
	}
}
