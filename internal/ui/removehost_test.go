package ui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/maogou/ohmyssh/internal/config"
)

// hostsFile is where the hosts these tests delete live. It is short and outside
// the home directory, so the question naming it can be spelled out here and still
// fits the line it is drawn on.
const hostsFile = "/srv/ohmyssh/hosts"

// hostsInOurFile is a list that came from the file ohmyssh writes, which is the
// only file a delete is offered for.
func hostsInOurFile() []config.SSHHost {
	return []config.SSHHost{
		{Name: "web1", Hostname: "10.0.0.1", User: "deploy", Tags: []string{"prod"}, SourceFile: hostsFile},
		{Name: "web2", Hostname: "10.0.0.2", User: "deploy", SourceFile: hostsFile},
		{Name: "web3", Hostname: "10.0.0.3", User: "deploy", SourceFile: hostsFile},
	}
}

// removeHosts is a browser that can delete hosts. It keeps what the question
// handed the service, so a test asserts on the host itself, and it answers with
// the list the file would hold afterwards — a real service rereads the file it
// has just written, so a deleted host leaves by the list rather than by being
// dropped here.
type removeHosts struct {
	model   browserModel
	result  []config.SSHHost
	removed []config.SSHHost
	err     error
}

func newRemoveHosts(t *testing.T, hosts []config.SSHHost) *removeHosts {
	t.Helper()

	h := &removeHosts{result: hosts}
	h.model = sized(BrowserOptions{
		Hosts:     slices.Clone(hosts),
		HostsFile: hostsFile,
		Remove: func(_ context.Context, host config.SSHHost) ([]config.SSHHost, error) {
			h.removed = append(h.removed, host)
			if h.err != nil {
				return nil, h.err
			}
			return slices.DeleteFunc(slices.Clone(h.result), func(candidate config.SSHHost) bool {
				return candidate.Name == host.Name
			}), nil
		},
	})
	return h
}

// send hands a message to the browser and keeps whatever command came back.
func (h *removeHosts) send(msg tea.Msg) tea.Cmd {
	updated, cmd := h.model.Update(msg)
	h.model = updated.(browserModel)
	return cmd
}

// ask presses X, which is the only way to the question.
func (h *removeHosts) ask(t *testing.T) {
	t.Helper()

	h.send(rune2('X'))
	if h.model.mode != modeConfirm {
		t.Fatalf("X left the browser in mode %v, want the confirm question", h.model.mode)
	}
}

// answer replies to the question and checks the browser is back on the list,
// which every answer leaves it on.
func (h *removeHosts) answer(t *testing.T, msg tea.KeyMsg) {
	t.Helper()

	h.send(msg)
	if h.model.mode != modeList {
		t.Fatalf("the question was answered with %q and the browser is in mode %v", msg, h.model.mode)
	}
}

// yes answers with a y and delivers the delete's reply back, which is what the
// program does with it: the delete runs on a goroutine of its own.
func (h *removeHosts) yes(t *testing.T) {
	t.Helper()

	cmd := h.send(rune2('y'))
	if cmd == nil {
		t.Fatal("y did not start a delete")
	}
	// The question is taken down as the delete starts, so that the row on screen
	// and the host being deleted cannot come apart while it is in flight.
	if h.model.mode != modeList {
		t.Fatalf("the browser is still waiting for an answer in mode %v", h.model.mode)
	}
	h.send(cmd())
}

// visibleNames is the aliases on screen, which is what a delete is judged by.
func (h *removeHosts) visibleNames() []string {
	names := make([]string, len(h.model.visible))
	for i, host := range h.model.visible {
		names[i] = host.Name
	}
	return names
}

// A host is deleted only after the user has been asked, and the question is drawn
// over the list rather than in place of it: the one thing a confirmation has to
// show is which host it is about.
func TestBrowserDeleteAsksBeforeItDeletes(t *testing.T) {
	h := newRemoveHosts(t, hostsInOurFile())
	h.ask(t)

	if len(h.removed) != 0 {
		t.Fatalf("a delete was sent before it was confirmed: %v", h.removed)
	}
	view := h.model.View()
	// The question names the host and the file, ends with the two answers, and the
	// bar underneath lists them.
	if want := "? delete web1 from " + hostsFile + "?  y/n"; !strings.Contains(view, want) {
		t.Errorf("the question line is missing %q:\n%s", want, view)
	}
	for _, hint := range []string{"delete", "cancel"} {
		if !strings.Contains(view, hint) {
			t.Errorf("the bar does not offer %q:\n%s", hint, view)
		}
	}
	// Every row is still on screen, so the answer can be checked against the one
	// under the cursor.
	if !strings.Contains(view, rowMarker) {
		t.Errorf("the cursor is not on a row any more:\n%s", view)
	}
	for _, name := range []string{"web1", "web2", "web3"} {
		if !strings.Contains(view, name) {
			t.Errorf("host %q left the list before the question was answered:\n%s", name, view)
		}
	}
}

// X is a literal character while the filter is being typed into, the way A, U and
// D are: a bare letter would swallow the first character of a search, and a
// shifted one is the deliberate keystroke that reads as a command instead.
func TestBrowserDeleteKeyNeedsAnEmptyFilter(t *testing.T) {
	h := newRemoveHosts(t, hostsInOurFile())
	h.send(rune2('w'))
	h.send(rune2('X'))

	if h.model.mode != modeList {
		t.Error("X inside a filter put the question up")
	}
	if got := h.model.filter.Value(); got != "wX" {
		t.Errorf("filter = %q, want the X to have been typed into it", got)
	}
}

// The ssh config is the user's file and ohmyssh only reads it, so X on a host
// from there refuses and names the file instead of asking a question no answer
// could carry out.
func TestBrowserDeleteRefusesAHostItDoesNotOwn(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   string
	}{
		{"web1", "/etc/ssh/ssh_config", "/etc/ssh/ssh_config"},
		// A host with no file recorded came from the ssh config itself, and the
		// message says so rather than naming an empty path.
		{"db1", "", "the ssh config"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newRemoveHosts(t, []config.SSHHost{
				{Name: tc.name, Hostname: "10.0.0.1", SourceFile: tc.source},
			})

			h.send(rune2('X'))

			if h.model.mode != modeList {
				t.Fatalf("a host ohmyssh does not own was asked about: mode %v", h.model.mode)
			}
			if len(h.removed) != 0 {
				t.Errorf("a host ohmyssh does not own was deleted: %v", h.removed)
			}
			if !h.model.failed || !strings.Contains(h.model.status, tc.want) {
				t.Errorf("status = %q (failed %v), want it to name %s",
					h.model.status, h.model.failed, tc.want)
			}
		})
	}
}

// A browser built without a host service says so rather than putting up a
// question whose answer nothing can carry out.
func TestBrowserDeleteIsUnavailableWithoutAService(t *testing.T) {
	m := sized(BrowserOptions{Hosts: hostsInOurFile(), HostsFile: hostsFile})
	m = pressKey(t, m, rune2('X'))

	if m.mode != modeList {
		t.Error("X put the question up with no service attached")
	}
	if !m.failed || !strings.Contains(m.status, "unavailable") {
		t.Errorf("status = %q (failed %v), want it to say deleting is unavailable", m.status, m.failed)
	}
}

// Only a explicit yes deletes. Everything else cancels, which is the answer that
// costs nothing to be wrong about, and a cancelled question leaves nothing behind
// on the status line or in the list.
func TestBrowserDeleteOnlyAPlainYesGoesAhead(t *testing.T) {
	answers := map[string]tea.KeyMsg{
		"n":            rune2('n'),
		"N":            rune2('N'),
		"a stray key":  rune2('3'),
		"esc":          key(tea.KeyEsc),
		"enter":        key(tea.KeyEnter),
		"space":        rune2(' '),
		"down":         key(tea.KeyDown),
		"left":         key(tea.KeyLeft),
		"backspace":    key(tea.KeyBackspace),
		"tab":          key(tea.KeyTab),
		"unknown page": key(tea.KeyPgUp),
	}

	for name, msg := range answers {
		t.Run(name, func(t *testing.T) {
			h := newRemoveHosts(t, hostsInOurFile())
			h.ask(t)
			h.answer(t, msg)

			if len(h.removed) != 0 {
				t.Errorf("%s deleted a host: %v", name, h.removed)
			}
			if h.model.status != "" || h.model.failed {
				t.Errorf("a cancelled question left %q (failed %v) on the status line",
					h.model.status, h.model.failed)
			}
			if got, want := h.visibleNames(), []string{"web1", "web2", "web3"}; !slices.Equal(got, want) {
				t.Errorf("the list changed to %v", got)
			}
		})
	}

	// ctrl+c means the same thing in every mode: it leaves the browser rather than
	// answering the question, and it deletes nothing on the way out.
	t.Run("ctrl+c", func(t *testing.T) {
		h := newRemoveHosts(t, hostsInOurFile())
		h.ask(t)
		h.send(key(tea.KeyCtrlC))

		if !h.model.quit {
			t.Error("ctrl+c did not quit")
		}
		if len(h.removed) != 0 {
			t.Errorf("ctrl+c deleted a host: %v", h.removed)
		}
	})
}

// A confirmed delete takes the row off the list, says what it did and where, and
// leaves the cursor on the host that followed the one that went.
func TestBrowserDeleteYesRemovesTheRowAndSaysSo(t *testing.T) {
	h := newRemoveHosts(t, hostsInOurFile())
	h.ask(t)
	h.yes(t)

	if len(h.removed) != 1 || h.removed[0].Name != "web1" {
		t.Fatalf("deleted %v, want web1", h.removed)
	}
	if got, want := h.visibleNames(), []string{"web2", "web3"}; !slices.Equal(got, want) {
		t.Errorf("the list holds %v, want %v", got, want)
	}
	if host, ok := h.model.selected(); !ok || host.Name != "web2" {
		t.Errorf("the cursor is on %+v, want the host that followed the deleted one", host)
	}
	if want := "deleted web1 from " + hostsFile + " (saved password kept)"; h.model.status != want {
		t.Errorf("status = %q, want %q", h.model.status, want)
	}
	if h.model.failed {
		t.Error("a completed delete was reported as a failure")
	}
	// The row is gone from the table. The name is still in the status line saying
	// what happened, so it is the address — which is on the row and nowhere else —
	// that tells the two apart.
	if view := h.model.View(); strings.Contains(view, "10.0.0.1") {
		t.Errorf("the deleted host is still a row:\n%s", view)
	}
}

// A delete that failed leaves the list exactly as it was: nothing was deleted, so
// nothing on screen is wrong, and the reason goes where the outcome would have.
func TestBrowserDeleteReportsAFailure(t *testing.T) {
	h := newRemoveHosts(t, hostsInOurFile())
	h.err = errors.New("no host named \"web1\" in " + hostsFile)
	h.ask(t)
	h.yes(t)

	if !h.model.failed {
		t.Error("a failed delete was reported as done")
	}
	if !strings.Contains(h.model.status, "no host named") {
		t.Errorf("status = %q, want the reason the delete failed", h.model.status)
	}
	if got, want := h.visibleNames(), []string{"web1", "web2", "web3"}; !slices.Equal(got, want) {
		t.Errorf("a failed delete changed the list to %v", got)
	}
}

// Deleting from the middle of a long list takes out a row rather than resetting
// the view: the cursor stays where it was, on the host that followed the one that
// went, and the window does not jump back to the top.
func TestBrowserDeleteKeepsTheCursorAndTheWindow(t *testing.T) {
	hosts := make([]config.SSHHost, 30)
	for i := range hosts {
		hosts[i] = config.SSHHost{
			Name:       fmt.Sprintf("web%02d", i),
			Hostname:   "10.0.0.1",
			SourceFile: hostsFile,
		}
	}

	h := newRemoveHosts(t, hosts)
	// A window small enough that the list has to scroll to reach the cursor.
	h.send(tea.WindowSizeMsg{Width: 100, Height: 14})
	for range 20 {
		h.send(key(tea.KeyDown))
	}

	offset, deleted := h.model.offset, h.model.visible[h.model.cursor].Name
	if deleted != "web20" {
		t.Fatalf("the cursor is on %s, want web20", deleted)
	}
	if offset == 0 {
		t.Fatal("the window did not scroll; this test proves nothing about it")
	}

	h.ask(t)
	h.yes(t)

	if got := h.model.visible[h.model.cursor].Name; got != "web21" {
		t.Errorf("the cursor is on %s, want the host that followed the deleted one", got)
	}
	if h.model.offset != offset {
		t.Errorf("the window jumped from offset %d to %d", offset, h.model.offset)
	}
}

// The question is drawn on the list's own frame, so it keeps the list's
// promises: exactly the terminal's height, and no line wider than the terminal.
func TestBrowserConfirmFrameIsExactlyTheTerminal(t *testing.T) {
	h := newRemoveHosts(t, hostsInOurFile())
	h.ask(t)

	for height := 1; height <= 40; height++ {
		h.send(tea.WindowSizeMsg{Width: 100, Height: height})
		if got := len(strings.Split(h.model.View(), "\n")); got != height {
			t.Fatalf("height %d: the frame is %d lines", height, got)
		}
	}

	// From the width the table's columns stop fitting at, which is where the
	// list's own width invariant starts; below it the list is unreadable whatever
	// it draws.
	for width := 22; width <= 200; width++ {
		h.send(tea.WindowSizeMsg{Width: width, Height: 24})
		for i, line := range strings.Split(h.model.View(), "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Fatalf("width %d: line %d is %d columns, %d past the edge:\n%s",
					width, i, got, got-width, h.model.View())
			}
		}
	}
}
