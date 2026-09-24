package ui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/maogou/ohmyssh/internal/config"
)

// addForm is a browser that can add hosts. It keeps what the form handed the
// service, so that a test asserts on the host itself rather than on the block it
// would have become — and writes to a path of the test's own, so that what the
// header shows is a file this run really would have written.
type addForm struct {
	model     browserModel
	hostsFile string
	added     []config.NewHost
	result    []config.SSHHost
	err       error
}

func newAddForm(t *testing.T, hosts []config.SSHHost) *addForm {
	t.Helper()

	f := &addForm{
		hostsFile: filepath.Join(t.TempDir(), "hosts"),
		result:    hosts,
	}
	f.model = sized(BrowserOptions{
		Hosts:     hosts,
		HostsFile: f.hostsFile,
		Add: func(_ context.Context, host config.NewHost) ([]config.SSHHost, error) {
			f.added = append(f.added, host)
			if f.err != nil {
				return nil, f.err
			}
			// A real service rereads the file it has just written, so the new host
			// arrives as part of the list rather than appended to it here.
			return append(slices.Clone(f.result), config.SSHHost{
				Name:       host.Alias,
				Hostname:   host.Host,
				User:       host.User,
				Port:       host.Port,
				Tags:       host.Tags,
				SourceFile: f.hostsFile,
			}), nil
		},
	})
	return f
}

// send hands a message to the browser and keeps whatever command came back.
func (f *addForm) send(msg tea.Msg) tea.Cmd {
	updated, cmd := f.model.Update(msg)
	f.model = updated.(browserModel)
	return cmd
}

// openForm presses A, which is the only way into the form.
func (f *addForm) openForm(t *testing.T) {
	t.Helper()

	f.send(rune2('A'))
	if f.model.mode != modeForm {
		t.Fatalf("A left the browser in mode %v, want the form", f.model.mode)
	}
}

// fill types a value into each field in turn, walking the form the way tab does.
func (f *addForm) fill(values map[int]string) {
	for i := range formLabels {
		if i > 0 {
			f.send(key(tea.KeyTab))
		}
		for _, r := range values[i] {
			f.send(rune2(r))
		}
	}
}

// enter submits the form and hands the command back to the browser, which is
// what the program does with it: the add runs on a goroutine of its own and
// reports with a message.
func (f *addForm) enter(t *testing.T) {
	t.Helper()

	cmd := f.send(key(tea.KeyEnter))
	if cmd == nil {
		return
	}
	msg := cmd()
	if _, isQuit := msg.(tea.QuitMsg); isQuit {
		t.Fatal("enter quit the browser")
	}
	f.send(msg)
}

// Both frames are as tall as the terminal at every height. A frame that is
// taller has its top eaten by the alt screen — the header, not the key hints —
// and one that is shorter leaves the hints floating above the bottom.
func TestFormFrameIsExactlyTheTerminalHeight(t *testing.T) {
	for height := 1; height <= 40; height++ {
		f := newAddForm(t, testHosts())
		f.openForm(t)
		f.model.height = height
		if got := len(strings.Split(f.model.View(), "\n")); got != height {
			t.Errorf("height %d: the form is %d lines", height, got)
		}

		list := newAddForm(t, testHosts())
		list.model.height = height
		if got := len(strings.Split(list.model.View(), "\n")); got != height {
			t.Errorf("height %d: the list is %d lines", height, got)
		}
	}
}

// No line of the form is wider than the terminal, with a value in every field:
// a field with something in it is drawn by a different path from one showing its
// placeholder, and a line past the edge wraps — which costs the frame its height
// as well.
func TestFormFrameIsExactlyTheTerminalWidth(t *testing.T) {
	filled := map[int]string{
		formAlias: "web2", formHost: "deploy.internal.example", formUser: "deploy",
		formPort: "2222", formTags: "prod, web",
	}

	for width := 22; width <= 200; width++ {
		empty := newAddForm(t, testHosts())
		empty.send(tea.WindowSizeMsg{Width: width, Height: 24})
		empty.openForm(t)

		full := newAddForm(t, testHosts())
		full.send(tea.WindowSizeMsg{Width: width, Height: 24})
		full.openForm(t)
		full.fill(filled)

		for name, form := range map[string]*addForm{"empty": empty, "filled": full} {
			for i, line := range strings.Split(form.model.View(), "\n") {
				if got := lipgloss.Width(line); got > width {
					t.Fatalf("width %d (%s): line %d is %d columns, %d past the edge:\n%s",
						width, name, i, got, got-width, form.model.View())
				}
			}
		}
	}
}

// A is shifted and only on a bare one, like U and D: a plain letter has to stay
// available to a filter that searches for "aws".
func TestAddKeyOpensTheFormOnlyOnABareKey(t *testing.T) {
	f := newAddForm(t, testHosts())
	f.openForm(t)

	filtered := newAddForm(t, testHosts())
	filtered.send(rune2('w'))
	filtered.send(rune2('A'))
	if filtered.model.mode != modeList {
		t.Errorf("A opened the form with something in the filter")
	}
	if got := filtered.model.filter.Value(); got != "wA" {
		t.Errorf("filter = %q, want the A to have been typed into it", got)
	}
}

// A browser built without a way to write hosts says so rather than showing a
// form that could not save anything.
func TestAddKeyWithoutAServiceSaysSo(t *testing.T) {
	m := sized(BrowserOptions{Hosts: testHosts()})

	updated, cmd := m.Update(rune2('A'))
	m = updated.(browserModel)
	if m.mode != modeList {
		t.Errorf("mode = %v, want the list", m.mode)
	}
	if !m.failed || !strings.Contains(m.status, "unavailable") {
		t.Errorf("status = %q (failed %v), want it to say adding is unavailable", m.status, m.failed)
	}
	if cmd != nil {
		t.Error("a browser that cannot add hosts still asked for a command to run")
	}
	// The key is not advertised where it cannot do anything.
	if strings.Contains(lastLine(m.View()), "add") {
		t.Errorf("the hint bar offers a key that does nothing: %q", lastLine(m.View()))
	}
}

// tab and shift+tab walk the fields and wrap at either end, so the number of
// presses between two fields is always the same.
func TestFormTabWalksTheFields(t *testing.T) {
	f := newAddForm(t, testHosts())
	f.openForm(t)

	for i := range formLabels {
		if f.model.form.focus != i {
			t.Fatalf("after %d tabs the focus is on field %d, want %d", i, f.model.form.focus, i)
		}
		f.send(key(tea.KeyTab))
	}
	if f.model.form.focus != formAlias {
		t.Errorf("tab past the last field left the focus on %d, want it back at the first", f.model.form.focus)
	}

	for i := len(formLabels) - 1; i >= 0; i-- {
		f.send(key(tea.KeyShiftTab))
		if f.model.form.focus != i {
			t.Fatalf("shift+tab left the focus on %d, want %d", f.model.form.focus, i)
		}
	}
}

// Only the field the keyboard is on is ever edited, whichever way it was pointed
// at — a key meant for the form must not land in a field the user is not looking
// at.
func TestFormTypesOnlyIntoTheFocusedField(t *testing.T) {
	f := newAddForm(t, testHosts())
	f.openForm(t)

	f.send(rune2('a'))
	f.send(key(tea.KeyDown))
	f.send(rune2('h'))
	f.send(key(tea.KeyUp))
	f.send(key(tea.KeyTab))

	got := f.model.form.spec()
	if got.Alias != "a" {
		t.Errorf("alias = %q, want the key typed while it was focused and nothing else", got.Alias)
	}
	if got.Host != "h" {
		t.Errorf("host = %q, want the key typed while it was focused", got.Host)
	}
	if got.User != "" || got.Port != "" || len(got.Tags) != 0 {
		t.Errorf("a field the keyboard was never on was edited: %+v", got)
	}
}

// enter hands the service what the fields say, with the space around a value
// dropped and the tag list split the way it will be read back.
func TestFormWritesWhatWasTyped(t *testing.T) {
	f := newAddForm(t, testHosts())
	f.openForm(t)
	f.fill(map[int]string{
		formAlias: " web2 ", formHost: "10.0.0.5", formUser: "deploy",
		formPort: "2222", formTags: "prod, web",
	})
	if view := f.model.View(); !strings.Contains(view, "10.0.0.5") {
		t.Errorf("the field's value is not drawn:\n%s", view)
	}
	f.enter(t)

	if len(f.added) != 1 {
		t.Fatalf("the service was handed %d hosts, want 1", len(f.added))
	}
	got := f.added[0]
	if got.Alias != "web2" || got.Host != "10.0.0.5" || got.User != "deploy" || got.Port != "2222" {
		t.Errorf("host = %+v, want the values as typed, without the space around them", got)
	}
	if !slices.Equal(got.Tags, []string{"prod", "web"}) {
		t.Errorf("tags = %v, want the two of them", got.Tags)
	}

	// Success closes the form, leaves the cursor on the host that was added and
	// says where it went.
	if f.model.mode != modeList {
		t.Errorf("mode = %v, want the list after a successful add", f.model.mode)
	}
	host, ok := f.model.selected()
	if !ok || host.Name != "web2" {
		t.Errorf("selected = %+v, want the host just added", host)
	}
	if !strings.Contains(f.model.status, "added web2") || !strings.Contains(f.model.status, f.hostsFile) {
		t.Errorf("status = %q, want it to name the host and the file it went to", f.model.status)
	}
	if f.model.failed {
		t.Error("a successful add was reported as a failure")
	}
}

// The host just written lands at the end of the list, which is past the bottom
// of a window that is full: an add that leaves the cursor where it was reads as
// an add that did nothing.
func TestFormScrolsToTheHostItAdded(t *testing.T) {
	hosts := make([]config.SSHHost, 40)
	for i := range hosts {
		hosts[i] = config.SSHHost{Name: fmt.Sprintf("web%02d", i), Hostname: "10.0.0.1", User: "root", Port: "22"}
	}

	f := newAddForm(t, hosts)
	f.openForm(t)
	f.fill(map[int]string{formAlias: "standby"})
	f.enter(t)

	host, ok := f.model.selected()
	if !ok || host.Name != "standby" {
		t.Fatalf("selected = %+v, want the host just added", host)
	}
	if view := f.model.View(); !strings.Contains(view, "standby") {
		t.Errorf("the host just added is not on screen:\n%s", view)
	}
}

// A refused add leaves the form up with everything in it. What is wrong with a
// host is usually one field, and retyping the other four to fix it is the worst
// thing a form can ask for.
func TestFormKeepsWhatWasTypedWhenTheAddIsRefused(t *testing.T) {
	f := newAddForm(t, testHosts())
	f.err = errors.New(`alias "web1" is already in ~/.ssh/config`)
	f.openForm(t)
	f.fill(map[int]string{
		formAlias: "web1", formHost: "10.0.0.9", formUser: "deploy",
		formPort: "22", formTags: "prod",
	})
	f.enter(t)

	if f.model.mode != modeForm {
		t.Fatalf("mode = %v, want the form to stay up", f.model.mode)
	}
	if !f.model.failed || !strings.Contains(f.model.status, "already in") {
		t.Errorf("status = %q (failed %v), want the reason it was refused", f.model.status, f.model.failed)
	}
	got := f.model.form.spec()
	if got.Alias != "web1" || got.Host != "10.0.0.9" || got.User != "deploy" || got.Port != "22" {
		t.Errorf("the form lost what was typed: %+v", got)
	}
	if !slices.Equal(got.Tags, []string{"prod"}) {
		t.Errorf("tags = %v, want the one that was typed", got.Tags)
	}
}

// An empty alias is the one thing the form can see is wrong before the service
// is asked, so it is answered without the round trip — and named.
func TestFormRefusesAnEmptyAliasWithoutAsking(t *testing.T) {
	f := newAddForm(t, testHosts())
	f.openForm(t)
	f.enter(t)

	if len(f.added) != 0 {
		t.Errorf("the service was handed %+v, want nothing", f.added)
	}
	if f.model.mode != modeForm || !f.model.failed {
		t.Errorf("mode = %v (failed %v), want the form still up and showing the reason", f.model.mode, f.model.failed)
	}
	if !strings.Contains(f.model.status, "alias") {
		t.Errorf("status = %q, want it to name the field", f.model.status)
	}
}

// esc leaves the list exactly as it was: nothing written, nothing said, and the
// cursor still on the host the user was looking at.
func TestFormEscReturnsToTheListUnchanged(t *testing.T) {
	f := newAddForm(t, testHosts())
	f.openForm(t)
	f.fill(map[int]string{formAlias: "web2"})
	f.send(key(tea.KeyEsc))

	if f.model.mode != modeList {
		t.Fatalf("mode = %v, want the list", f.model.mode)
	}
	if len(f.added) != 0 {
		t.Errorf("cancelling still wrote %+v", f.added)
	}
	if f.model.status != "" || f.model.failed {
		t.Errorf("status = %q (failed %v), want nothing said", f.model.status, f.model.failed)
	}
	if host, ok := f.model.selected(); !ok || host.Name != "web1" {
		t.Errorf("selected = %+v, want the list's own cursor", host)
	}
}

// The form says which file the host will be written to, so a host that does not
// turn up in the user's ssh config is at least a host the browser said where it
// would put.
func TestFormNamesWhereItWrites(t *testing.T) {
	f := newAddForm(t, testHosts())
	// A terminal wide enough for the path: a header that cannot hold both says
	// which it gave up, and what it gives up is the note, not the title.
	f.send(tea.WindowSizeMsg{Width: 200, Height: 30})
	f.openForm(t)

	view := f.model.View()
	if !strings.Contains(view, "new host") {
		t.Errorf("the form's header does not say what it is:\n%s", view)
	}
	if !strings.Contains(view, f.hostsFile) {
		t.Errorf("the form's header does not name %s:\n%s", f.hostsFile, view)
	}
	// The list's own header is replaced rather than stacked on top of.
	if strings.Contains(view, " of ") {
		t.Errorf("the form drew the list's count as well:\n%s", view)
	}
}

// The list and the form are drawn on the same grid, so opening one does not move
// what the other put on the last line.
func TestFormKeepsTheHintsOnTheLastLine(t *testing.T) {
	f := newAddForm(t, testHosts())
	listHints := lastLine(f.model.View())
	f.openForm(t)
	formHints := lastLine(f.model.View())

	if listHints == formHints {
		t.Errorf("the form kept the list's hints: %q", formHints)
	}
	for _, binding := range []string{"save", "field", "cancel"} {
		if !strings.Contains(formHints, binding) {
			t.Errorf("the form's hints are missing %q: %q", binding, formHints)
		}
	}
}

func lastLine(view string) string {
	lines := strings.Split(view, "\n")
	return lines[len(lines)-1]
}

// A frame too short for five fields draws a window of them, and the field being
// typed on is always in it: a form that hides its own cursor is worse than one
// that hides a label.
func TestFormLinesFollowTheFocusWhenThereIsNoRoom(t *testing.T) {
	form := newHostForm(80)

	full := form.lines(80, formFields+1)
	if len(full) != formFields+1 {
		t.Fatalf("drew %d lines, want the fields and the rule", len(full))
	}
	if !strings.Contains(full[formFields], "─") {
		t.Errorf("no rule under the fields:\n%s", strings.Join(full, "\n"))
	}

	form.focusOn(formTags)
	cramped := strings.Join(form.lines(80, 3), "\n")
	if got := len(strings.Split(cramped, "\n")); got != 3 {
		t.Fatalf("drew %d lines, want 3", got)
	}
	if !strings.Contains(cramped, formLabels[formTags].label) {
		t.Errorf("the field being typed on is not in the window:\n%s", cramped)
	}
	if strings.Contains(cramped, "─") {
		t.Errorf("a rule was drawn with lines to spare for a field:\n%s", cramped)
	}
}

// Each host's file is named once, however many hosts are in it, and a host whose
// file is unknown says nothing rather than saying "".
func TestSourceHintNamesEachFileOnce(t *testing.T) {
	cases := []struct {
		name  string
		hosts []config.SSHHost
		want  string
	}{
		{"nothing to name", nil, ""},
		{"one file", []config.SSHHost{{SourceFile: "/etc/ssh/config"}, {SourceFile: "/etc/ssh/config"}}, "/etc/ssh/config"},
		{"two files", []config.SSHHost{{SourceFile: "/etc/ssh/config"}, {SourceFile: "/home/x/.ohmyssh/hosts"}}, "/etc/ssh/config + /home/x/.ohmyssh/hosts"},
		{"a host with no file", []config.SSHHost{{Name: "10.0.0.1"}}, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sourceHint(tc.hosts); got != tc.want {
				t.Errorf("sourceHint = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAbbreviatedShortensTheHomeDirectory(t *testing.T) {
	original := homeDir
	homeDir = filepath.FromSlash("/home/deploy")
	defer func() { homeDir = original }()

	cases := map[string]string{
		filepath.FromSlash("/home/deploy/.ssh/config"): "~/.ssh/config",
		filepath.FromSlash("/home/deploy"):             "~",
		filepath.FromSlash("/etc/ssh/ssh_config"):      filepath.FromSlash("/etc/ssh/ssh_config"),
		"": "",
	}
	for path, want := range cases {
		if got := abbreviated(path); got != want {
			t.Errorf("abbreviated(%q) = %q, want %q", path, got, want)
		}
	}
}

// splitTags reads the field the way the parser reads the line it is written on,
// so what the form shows is what the hosts file will hold.
func TestSplitTags(t *testing.T) {
	cases := map[string][]string{
		"":                nil,
		"prod":            {"prod"},
		"prod, web":       {"prod", "web"},
		"prod,web":        {"prod", "web"},
		"  prod ,  web  ": {"prod", "web"},
		"prod web":        {"prod", "web"},
		"prod,,web":       {"prod", "web"},
		"prod, web, prod": {"prod", "web", "prod"},
	}
	for value, want := range cases {
		if got := splitTags(value); !slices.Equal(got, want) {
			t.Errorf("splitTags(%q) = %v, want %v", value, got, want)
		}
	}
}
