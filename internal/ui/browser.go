package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/maogou/ohmyssh/internal/config"
	"github.com/maogou/ohmyssh/internal/pkg/appdir"
	"github.com/maogou/ohmyssh/internal/pkg/zlog"
)

const (
	// defaultBrowserWidth is the layout width used before the terminal reports
	// its real size.
	defaultBrowserWidth = 80

	// margin indents every line, and filterPrompt marks the filter box. Together
	// with filterPromptWidth they describe the grid the view is drawn on: a line
	// is margin + content, and the filter's text area is the content width minus
	// its prompt.
	margin            = "  "
	filterPrompt      = "› "
	filterPromptWidth = 2 // display columns taken by filterPrompt
	minContentWidth   = 10

	// rowIndent is what a table row spends before its first column: the cursor
	// marker, which is the same width selected or not so the columns line up
	// down the table.
	rowIndent = 2
	rowMarker = "▸ "
	columnGap = 2

	// maxColumnWidth stops one long alias or address from crowding every other
	// column off the line; minColumnWidth is the floor the layout trims to
	// before giving up and letting a column be clipped.
	maxColumnWidth = 32
	minColumnWidth = 6

	// tableHeaderLines is what the table spends on its column labels and the rule
	// under them before the first host row.
	tableHeaderLines = 2

	// listChrome is the number of lines spent on everything that is not the
	// table: header, rule, filter, spacer, status and key hints.
	listChrome = 6
	// defaultListHeight applies before the terminal reports its size.
	defaultListHeight = 10
)

// SessionFunc runs a session on a host and returns when it ends. The browser
// calls it only after the terminal has been released for the session; dialling,
// the command to run and the exit status are all its business, not the
// browser's.
type SessionFunc func(ctx context.Context, host config.SSHHost) error

// BrowserOptions configures the host browser.
type BrowserOptions struct {
	Hosts []config.SSHHost
	// Session runs the session behind a selected host.
	Session SessionFunc
	// Remote opens the file view's session on the host under the cursor, which
	// the U and D keys show. A nil Remote leaves those keys saying so.
	Remote RemoteSessionFunc
	// Add writes a host typed into the form, which the A key shows. A nil Add
	// leaves that key saying so.
	Add AddHostFunc
	// Remove deletes a host from the list, which the X key asks about. A nil
	// Remove leaves that key saying so.
	Remove RemoveHostFunc
	// HostsFile is the file Add writes. The form shows it before it writes there,
	// so that a host the user cannot then find is at least a host the browser
	// said where it would put.
	HostsFile string
	// Filter seeds the search box.
	Filter string
}

// RemoveHostFunc deletes a host and returns the hosts as they read afterwards, so
// the browser can take the row off screen without reading the config again.
// Which hosts may be deleted at all — the file ohmyssh writes, and only that one
// — is the service's business; asking the user whether they meant this one is the
// browser's.
type RemoveHostFunc func(ctx context.Context, host config.SSHHost) ([]config.SSHHost, error)

// browserMode is which of the five things the browser is showing. The file
// view, the form and the help are whole frames of their own rather than regions
// of the list's, and the confirm question is the list with a different line
// under it, so the five are told apart here rather than inside the renderer.
type browserMode int

const (
	modeList browserMode = iota
	modeFiles
	modeForm
	// modeConfirm is the list with a question in place of the status line: the
	// row the user is asking to delete is still on screen, under the cursor, which
	// is what makes the question answerable.
	modeConfirm
	// modeHelp is the keys of the view it was opened from, which helpFor still
	// names: the help is a frame of its own, but it is about one of the other
	// views, and closing it goes back to that one.
	modeHelp
)

// sessionFinishedMsg reports the outcome of a session that ran in the terminal.
type sessionFinishedMsg struct {
	host config.SSHHost
	err  error
}

// hostRemovedMsg reports the outcome of a delete, which is the only thing that
// can take a host back out of the list.
type hostRemovedMsg struct {
	host  config.SSHHost
	hosts []config.SSHHost
	err   error
}

// RunHostBrowser shows the host list and blocks until the user quits. Selecting
// a host releases the terminal, runs an SSH session, then returns to the list.
func RunHostBrowser(opts BrowserOptions) error {
	m := newBrowserModel(opts)

	// The view owns the terminal from here on: the alt screen is up and the
	// keyboard is in raw mode, so a log line written to stderr lands in the middle
	// of a frame and leaves the frame above it shifted by one. The view dials on
	// every open, so that is one line of the previous frame left on screen per
	// open unless the logs go somewhere else for as long as it is up.
	restoreLogs := zlog.Detach(logPath())
	defer restoreLogs()

	program := tea.NewProgram(m, tea.WithAltScreen())
	final, err := program.Run()

	// A transfer outlives the frame that started it, and so does the session the
	// file view browsed over. Either one left running would keep a goroutine and
	// a connection behind after the terminal has been handed back.
	if closer, ok := final.(viewCloser); ok {
		closer.closeView()
	}
	return err
}

// logFileName is what the browser's log is called, inside the directory appdir
// names. It is a file of its own rather than lines mixed into anything else: it
// is written while the view owns the terminal, and read afterwards.
const logFileName = "ohmyssh.log"

// logPath is where the view's logs go while it owns the terminal, or the empty
// string when the home directory cannot be named. The view then logs nowhere,
// which is what Detach makes of it, and is better than logging onto the screen it
// is drawing.
func logPath() string {
	path, err := appdir.Path(logFileName)
	if err != nil {
		return ""
	}
	return path
}

// viewCloser is the part of the model teardown needs. It is an interface so that
// it is satisfied whether the program was handed the model or a pointer to it.
type viewCloser interface {
	closeView()
}

type browserModel struct {
	hosts   []config.SSHHost
	session SessionFunc
	filter  textinput.Model

	// remote opens the file view's session, and is nil when the browser was built
	// without one — which is what the U and D keys check before showing a view
	// that could never fill its right column.
	remote RemoteSessionFunc
	// add writes a host the form collected, and is nil when the browser was built
	// without one; hostsFile is where it writes, for the form to show.
	add AddHostFunc
	// remove deletes a host, and is nil when the browser was built without one. A
	// delete is only ever offered for a host in hostsFile, since that is the only
	// file the service writes: the ssh config is the user's.
	remove    RemoveHostFunc
	hostsFile string
	// mode is which of the five things the browser is showing.
	mode browserMode
	// helpFor is the view the help describes and goes back to. It is only read
	// while mode is modeHelp, which is also the only way it is set.
	helpFor browserMode
	// pending is the host the confirm question is about. It is set when the
	// question is put up and cleared when it is answered, so a question can never
	// outlive the row it was asked about.
	pending config.SSHHost
	// files is the file view. It is kept after the view closes, and holds the
	// session and any transfer until close, so that leaving it really does let
	// both go.
	files filesModel
	// form is the add form. It is built when the form is opened and dropped when
	// it is left, so nothing typed into a form that was cancelled can come back.
	form hostForm

	visible []config.SSHHost
	cursor  int
	offset  int

	width  int
	height int

	status string
	failed bool
	quit   bool
}

func newBrowserModel(opts BrowserOptions) browserModel {
	filter := textinput.New()
	filter.Placeholder = "filter by name, host, user or tag"
	filter.Prompt = filterPrompt
	filter.PromptStyle = cursorStyle
	filter.TextStyle = nameStyle
	filter.Cursor.Style = cursorStyle
	filter.Width = filterWidth(defaultBrowserWidth)
	filter.SetValue(opts.Filter)
	filter.Focus()

	m := browserModel{
		hosts:     opts.Hosts,
		session:   opts.Session,
		remote:    opts.Remote,
		add:       opts.Add,
		remove:    opts.Remove,
		hostsFile: opts.HostsFile,
		filter:    filter,
	}
	m.applyFilter()
	return m
}

func (m browserModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m browserModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// The text input sizes both its scrolling window and its placeholder from
		// its own Width; left at zero, bubbles renders only the cursor rune of
		// the placeholder.
		m.filter.Width = filterWidth(msg.Width)
		m.files.resize(msg.Width, msg.Height)
		if m.mode == modeForm {
			m.form.resize(m.contentWidth())
		}
		return m, nil

	case sessionFinishedMsg:
		m.failed = msg.err != nil
		if msg.err != nil {
			m.status = fmt.Sprintf("%s: %v", msg.host.Name, msg.err)
		} else {
			m.status = fmt.Sprintf("disconnected from %s", msg.host.Name)
		}
		return m, nil

	case hostAddedMsg:
		return m.hostAdded(msg)

	case hostRemovedMsg:
		return m.hostRemoved(msg)

	case remoteOpenedMsg, remoteEntriesMsg, transferProgressMsg, transferDoneMsg:
		// The file view's own goroutines report to the program, and the program
		// reports to the model it was handed. These belong to the view, which is
		// the only thing that starts a transfer or opens a session — and which
		// still has to hear them after it has closed, so that a session arriving
		// late can be closed rather than leaked.
		var cmd tea.Cmd
		m.files, cmd = m.files.Update(msg)
		return m, cmd

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	if m.mode == modeForm {
		// The cursor's blink timer is not a key and belongs to the input that
		// asked for it; without this the form's cursor is drawn once and then
		// stands still.
		return m, m.form.update(msg)
	}
	return m, nil
}

// hostAdded records what came back from writing a host. A failure leaves the
// form up with everything in it: what is wrong with a host is usually one field,
// and retyping the other four to fix it is the worst thing a form can ask for.
func (m browserModel) hostAdded(msg hostAddedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status, m.failed = msg.err.Error(), true
		return m, nil
	}

	m.hosts = msg.hosts
	m.applyFilter()
	// The host that was just added is the one under the cursor. It lands at the
	// end of the list, which is past the bottom of a window that is full.
	m.reveal(msg.host.Alias)

	m.status = fmt.Sprintf("added %s", msg.host.Alias)
	if m.hostsFile != "" {
		m.status += " to " + abbreviated(m.hostsFile)
	}
	m.failed = false
	m.mode = modeList
	m.form = hostForm{}
	m.filter.Focus()
	return m, nil
}

// hostRemoved records what came back from deleting a host. A failure leaves the
// list exactly as it was, with the reason in the status line: nothing was
// deleted, so nothing on screen is wrong.
func (m browserModel) hostRemoved(msg hostRemovedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status, m.failed = msg.err.Error(), true
		return m, nil
	}

	// The list is rebuilt from what the file holds now, and that rebuild filters
	// it again — which drops the cursor and the scroll back to the top. Holding on
	// to both is what makes deleting from the middle of a long list feel like a row
	// coming out rather than the list resetting: the cursor stays where it was, on
	// the host that followed the one that went.
	cursor, offset := m.cursor, m.offset
	m.hosts = msg.hosts
	m.applyFilter()
	m.cursor = min(max(cursor, 0), max(len(m.visible)-1, 0))
	m.offset = offset
	m.clampScroll()

	m.status = fmt.Sprintf("deleted %s", msg.host.Name)
	if m.hostsFile != "" {
		m.status += " from " + abbreviated(m.hostsFile)
	}
	// The password is saved under a login identity rather than under the alias, so
	// it belongs to every alias that resolves there and is not the browser's to
	// throw away. Saying so is the difference between the user knowing they can
	// clear it and thinking the delete did it.
	m.status += " (saved password kept)"
	m.failed = false
	return m, nil
}

// reveal puts the cursor on the named host, scrolling the window to it. A host
// that has just been written lands at the end of the list, and an add that
// leaves the cursor where it was looks like an add that did nothing.
func (m *browserModel) reveal(alias string) {
	for i, host := range m.visible {
		if host.Name == alias {
			m.cursor = i
			m.clampScroll()
			return
		}
	}
}

func (m browserModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// The help, the file view and the form own the frame and nearly every key in
	// it while any of them is up.
	if m.mode == modeHelp {
		return m.handleHelpKey(msg)
	}
	if m.mode == modeFiles {
		return m.handleFilesKey(msg)
	}
	if m.mode == modeForm {
		return m.handleFormKey(msg)
	}
	if m.mode == modeConfirm {
		return m.handleConfirmKey(msg)
	}

	switch msg.String() {
	case "ctrl+c":
		m.quit = true
		return m, tea.Quit

	case "q":
		// Only quit on a bare "q"; inside a filter it is a literal character.
		if m.filter.Value() == "" {
			m.quit = true
			return m, tea.Quit
		}

	case "A":
		// Shifted and bare, for the same reasons as U and D below.
		if m.filter.Value() == "" {
			return m.openForm()
		}

	case "X":
		// Shifted and bare, like A above and for the same reason.
		if m.filter.Value() == "" {
			return m.askRemove()
		}

	case "?":
		// Unlike the capitals above, this one needs no empty filter to mean its
		// binding: a query with a question mark in it matches no host, so the
		// character is not one the filter can use, and "?" is what everything
		// else that scrolls offers for help.
		return m.openHelp()

	case "U", "D":
		// Shifted, and only on a bare one. A plain "u" or "d" would swallow the
		// first letter of any search for "db1" or "ubuntu", which is the whole
		// point of the filter; a shifted one is deliberate, because a search that
		// starts with a capital is the rare case. The modifier keys are no use
		// here either: ctrl+u and ctrl+d are the input's own delete bindings.
		if m.filter.Value() == "" {
			// Both open the same view; which pane has the keyboard is the one the
			// key is about, since the direction a transfer goes is the pane the
			// cursor is in.
			start := paneLocal
			if msg.String() == "D" {
				start = paneRemote
			}
			return m.openFiles(start)
		}

	case "enter":
		if host, ok := m.selected(); ok {
			return m, m.connect(host)
		}
		return m, nil

	case "esc":
		if m.filter.Value() != "" {
			m.filter.SetValue("")
			m.applyFilter()
			return m, nil
		}
		m.quit = true
		return m, tea.Quit

	case "up", "ctrl+p":
		m.move(-1)
		return m, nil

	case "down", "ctrl+n":
		m.move(1)
		return m, nil

	case "pgup":
		m.move(-m.pageSize())
		return m, nil

	case "pgdown":
		m.move(m.pageSize())
		return m, nil

	case "home":
		m.cursor = 0
		m.clampScroll()
		return m, nil

	case "end":
		m.cursor = len(m.visible) - 1
		m.clampScroll()
		return m, nil
	}

	// Everything else edits the filter, then re-filters and re-anchors the cursor.
	before := m.filter.Value()
	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(msg)
	if m.filter.Value() != before {
		m.applyFilter()
	}
	return m, cmd
}

// handleFilesKey answers a key while the file view is up. The view owns the
// frame and nearly every key in it; the one thing the browser keeps for itself
// is quitting, which means the same in every mode.
func (m browserModel) handleFilesKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.quit = true
		return m, tea.Quit

	case "?":
		// Only while the filter line is down. While it is up the keyboard is the
		// filter's, and a "?" there is a character like any other — the same
		// reason the list's capitals wait for an empty filter.
		if !m.files.filtering {
			return m.openHelp()
		}
	}

	var cmd tea.Cmd
	m.files, cmd = m.files.Update(msg)
	if m.files.leaving {
		return m.closeFiles(), cmd
	}
	return m, cmd
}

// openHelp shows the keys of the view it was opened from. That view is left
// exactly as it was — the help is drawn over it rather than instead of it — so
// closing the help puts the user back where they were, cursor and all.
func (m browserModel) openHelp() (tea.Model, tea.Cmd) {
	m.helpFor = m.mode
	m.mode = modeHelp
	return m, nil
}

// handleHelpKey answers a key while the help is up. Nothing in it reaches the
// view underneath, since a key pressed over a screen of key bindings is a key
// pressed by accident, and acting on the frame the user cannot see is the one
// thing this screen could do wrong. ctrl+c still quits, as it does everywhere.
func (m browserModel) handleHelpKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.quit = true
		return m, tea.Quit

	case "esc", "?":
		m.mode = m.helpFor
		// The list's filter is the one thing that had the keyboard before the
		// help went up and gets it back after: its blink was dropped along with
		// the other messages the help swallowed, and this is what puts the
		// cursor back on the line.
		if m.mode == modeList {
			m.filter.Focus()
		}
		return m, nil
	}
	return m, nil
}

// openFiles shows the file view for the host under the cursor, with start as the
// pane the keyboard begins in. A browser built without a way to open a session
// says so rather than showing a view that could never fill its right column.
func (m browserModel) openFiles(start paneSide) (tea.Model, tea.Cmd) {
	host, ok := m.selected()
	if !ok {
		return m, nil
	}
	if m.remote == nil {
		m.status = "transfers are unavailable: no file service is attached"
		m.failed = true
		return m, nil
	}

	m.mode = modeFiles
	m.files = newFilesModel(host, m.width, m.height, start)
	m.status = ""
	m.failed = false
	return m, m.files.openCmd(m.remote)
}

// closeFiles takes the frame back to the host list, which is the only way out of
// the file view. The session and any transfer running on it are let go here
// rather than left for exit: the user is done with both.
func (m browserModel) closeFiles() browserModel {
	// The list's status line takes over from the view's, since the last thing
	// that happened happened in there and the list is what is on screen now.
	m.status, m.failed = m.files.status, m.files.failed

	m.files.close()
	m.files = filesModel{}
	m.mode = modeList
	m.filter.Focus()
	return m
}

// openForm shows the add form. It starts empty every time: a host that was
// cancelled or written is not a draft, and a form that comes back holding the
// last one is a form that can write it twice.
func (m browserModel) openForm() (tea.Model, tea.Cmd) {
	if m.add == nil {
		m.status = "adding hosts is unavailable: no host service is attached"
		m.failed = true
		return m, nil
	}

	m.form = newHostForm(m.contentWidth())
	m.mode = modeForm
	m.status, m.failed = "", false
	return m, textinput.Blink
}

// askRemove puts the delete question up for the host under the cursor. Nothing
// has happened yet: the row stays where it is, and the question is drawn in the
// status line above the key hints so the user can see which row it is about.
func (m browserModel) askRemove() (tea.Model, tea.Cmd) {
	if m.remove == nil || m.hostsFile == "" {
		m.status = "deleting hosts is unavailable: no host service is attached"
		m.failed = true
		return m, nil
	}

	host, ok := m.selected()
	if !ok {
		return m, nil
	}
	// A host that is not in the file ohmyssh writes cannot be deleted from here,
	// and asking about one would be asking a question no answer can carry out. The
	// service refuses the same thing — it has to, since a command line reaches it
	// without this screen — but it is the list that knows which file each host came
	// from, so the user is told before they answer rather than after.
	if filepath.Clean(host.SourceFile) != filepath.Clean(m.hostsFile) {
		m.status = fmt.Sprintf("%s is in %s, which ohmyssh does not write; edit it there",
			host.Name, sourceName(host.SourceFile))
		m.failed = true
		return m, nil
	}

	m.pending = host
	m.mode = modeConfirm
	m.status, m.failed = "", false
	return m, nil
}

// handleConfirmKey answers a key while the delete question is up.
//
// Only a "y" goes ahead. A delete takes an explicit yes, so everything else — n,
// esc, enter, a key pressed by accident — cancels, which is also the answer that
// costs nothing to be wrong about. ctrl+c still quits, since it means the same
// thing in every mode.
func (m browserModel) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.quit = true
		return m, tea.Quit

	case "y", "Y":
		return m.removeHost()
	}
	return m.cancelRemove(), nil
}

// cancelRemove drops the question and takes the frame back to the list. Nothing
// was written, so there is nothing to report: the row is where it was.
func (m browserModel) cancelRemove() browserModel {
	m.pending = config.SSHHost{}
	m.mode = modeList
	m.status, m.failed = "", false
	m.filter.Focus()
	return m
}

// removeHost hands the host the user confirmed to the service. The question is
// taken down first, so that the row on screen and the host being deleted cannot
// come apart while the delete is in flight.
func (m browserModel) removeHost() (tea.Model, tea.Cmd) {
	host, remove := m.pending, m.remove
	m = m.cancelRemove()
	return m, func() tea.Msg {
		hosts, err := remove(context.Background(), host)
		return hostRemovedMsg{host: host, hosts: hosts, err: err}
	}
}

// sourceName names where a host came from, for a message about a file the user
// has to go and edit themselves.
func sourceName(file string) string {
	if file == "" {
		return "the ssh config"
	}
	return abbreviated(file)
}

// handleFormKey answers a key while the add form is up. The focused field does
// its own editing; everything else is the form's, so a key that means something
// here never reaches the field as a character.
func (m browserModel) handleFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.quit = true
		return m, tea.Quit

	case "esc":
		// Cancelling throws away what was typed, which is what esc means in the
		// list as well: a host that is wrong is cheaper to retype than to
		// untangle, and nothing has been written yet either way.
		m.mode = modeList
		m.status, m.failed = "", false
		m.filter.Focus()
		return m, nil

	case "enter":
		return m.submitForm()

	case "tab", "down", "ctrl+n":
		return m, m.form.focusOn(m.form.focus + 1)

	case "shift+tab", "up", "ctrl+p":
		return m, m.form.focusOn(m.form.focus - 1)
	}

	return m, m.form.update(msg)
}

// submitForm hands the fields to the service. They are validated here as well as
// there so that a field left empty is answered without a round trip; the service
// still has to validate, since a command line can reach it without this form
// ever being drawn.
func (m browserModel) submitForm() (tea.Model, tea.Cmd) {
	host := m.form.spec()
	if err := host.Validate(); err != nil {
		m.status, m.failed = err.Error(), true
		return m, nil
	}

	add := m.add
	m.status, m.failed = "", false
	return m, func() tea.Msg {
		hosts, err := add(context.Background(), host)
		return hostAddedMsg{host: host, hosts: hosts, err: err}
	}
}

// closeView releases what the browser is still holding: the session the file
// view was browsing over and any transfer still running on it.
func (m *browserModel) closeView() {
	m.files.close()
}

// connect releases the terminal and runs a session for host.
func (m browserModel) connect(host config.SSHHost) tea.Cmd {
	session := &sessionCommand{host: host, run: m.session}
	return tea.Exec(session, func(err error) tea.Msg {
		return sessionFinishedMsg{host: host, err: err}
	})
}

func (m *browserModel) selected() (config.SSHHost, bool) {
	if m.cursor < 0 || m.cursor >= len(m.visible) {
		return config.SSHHost{}, false
	}
	return m.visible[m.cursor], true
}

func (m *browserModel) move(delta int) {
	if len(m.visible) == 0 {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor > len(m.visible)-1 {
		m.cursor = len(m.visible) - 1
	}
	m.clampScroll()
}

// applyFilter recomputes the visible set, keeping the cursor in range.
func (m *browserModel) applyFilter() {
	query := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	if query == "" {
		m.visible = m.hosts
	} else {
		m.visible = m.visible[:0:0]
		for _, h := range m.hosts {
			if hostMatches(h, query) {
				m.visible = append(m.visible, h)
			}
		}
	}
	if m.cursor > len(m.visible)-1 {
		m.cursor = len(m.visible) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.offset = 0
	m.clampScroll()
}

// hostMatches reports whether the query appears in any user-visible field.
func hostMatches(h config.SSHHost, query string) bool {
	for _, field := range []string{h.Name, h.Hostname, h.User} {
		if strings.Contains(strings.ToLower(field), query) {
			return true
		}
	}
	for _, tag := range h.Tags {
		if strings.Contains(strings.ToLower(tag), query) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Layout
// ---------------------------------------------------------------------------

// filterWidth is the width of the filter input area for a terminal of the given
// width, leaving room for the prompt, the margin and the right edge.
func filterWidth(termWidth int) int {
	return max(termWidth-4, 8)
}

// contentWidth is the width a frame's content is drawn to once the left margin
// is accounted for. It derives from the filter width so that the filter box and
// the content under it share a single grid, in either view.
func contentWidth(termWidth int) int {
	return max(filterWidth(termWidth)+filterPromptWidth, minContentWidth)
}

// rule draws the hairline that separates one part of a frame from the next. Both
// views are drawn on the same grid, so they share the one.
func rule(width int) string {
	return margin + ruleStyle.Render(strings.Repeat("─", max(width, 1)))
}

// terminalWidth is the terminal's width, or the default before it has reported
// one.
func (m browserModel) terminalWidth() int {
	if m.width <= 0 {
		return defaultBrowserWidth
	}
	return m.width
}

// contentWidth is the width every line is drawn to once the left margin is
// accounted for.
func (m browserModel) contentWidth() int {
	return contentWidth(m.terminalWidth())
}

// listHeight is the number of rows available for hosts.
func (m browserModel) listHeight() int {
	if m.height <= 0 {
		return defaultListHeight
	}
	return max(m.height-listChrome, 1)
}

// listWindow is how many host rows the frame draws, and whether the overflow
// counter takes one of them. It depends only on the window size and the result
// count, never on the scroll offset, so the count cannot shift as the user
// moves through the list.
func (m browserModel) listWindow() (rows int, overflow bool) {
	height := max(m.listHeight()-tableHeaderLines, 1)
	if len(m.visible) > height && height > 1 {
		return height - 1, true
	}
	return height, false
}

func (m *browserModel) pageSize() int {
	rows, _ := m.listWindow()
	if rows < 1 {
		return 1
	}
	return rows
}

func (m *browserModel) clampScroll() {
	rows, _ := m.listWindow()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+rows {
		m.offset = m.cursor - rows + 1
	}
	if maxOffset := max(len(m.visible)-rows, 0); m.offset > maxOffset {
		m.offset = maxOffset
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

// column is one column of the host table.
type column struct {
	header string
	cell   func(config.SSHHost) string
	// style paints the cell. The header is painted by headerStyle instead, so a
	// column can be loud in the data and quiet in its label or the other way
	// round.
	style lipgloss.Style
	// right aligns the column, for values that read as numbers.
	right bool
	// dropOrder is when the column is given up if the table does not fit, with 1
	// going first. Zero means the column is never dropped.
	dropOrder int
	// width is the measured display width; tableLayout fills it in.
	width int
}

// hostColumns is the table, in display order. The alias and the address carry no
// drop order: they are what the user picks a host by, so everything else goes
// before either of them gives up space.
var hostColumns = []column{
	{header: "NAME", cell: func(h config.SSHHost) string { return h.Name }, style: nameStyle},
	{header: "USER", cell: config.SSHHost.DisplayUser, style: metaStyle, dropOrder: 3},
	{header: "HOST", cell: hostName, style: metaStyle},
	{header: "PORT", cell: portOf, style: metaStyle, right: true, dropOrder: 2},
	{header: "TAGS", cell: tagText, style: tagStyle, dropOrder: 1},
}

// hostName is the name a host resolves to, falling back to its alias.
func hostName(h config.SSHHost) string {
	if h.Hostname != "" {
		return h.Hostname
	}
	return h.Name
}

// portOf is the port a host dials. An empty Port means ssh's default, which the
// table spells out rather than leaving the cell looking unset.
func portOf(h config.SSHHost) string {
	if h.Port == "" {
		return "22"
	}
	return h.Port
}

// tableLayout measures the columns against the hosts on screen, then fits them
// into width. Widths come from the hosts actually showing, so a filter that
// leaves only short names behind does not keep a wide column reserved.
func (m browserModel) tableLayout(width int) []column {
	cols := make([]column, len(hostColumns))
	copy(cols, hostColumns)

	for i := range cols {
		cols[i].width = lipgloss.Width(cols[i].header)
		for _, h := range m.visible {
			cols[i].width = max(cols[i].width, lipgloss.Width(cols[i].cell(h)))
		}
		cols[i].width = min(cols[i].width, maxColumnWidth)
	}

	budget := width - rowIndent
	for order := 1; totalWidth(cols) > budget; order++ {
		idx := dropIndex(cols, order)
		if idx < 0 {
			break // only columns that must stay are left
		}
		cols = append(cols[:idx], cols[idx+1:]...)
	}
	trimToFit(cols, budget)
	return cols
}

// totalWidth is the width of a table row, gaps between columns included.
func totalWidth(cols []column) int {
	total := max(len(cols)-1, 0) * columnGap
	for _, c := range cols {
		total += c.width
	}
	return total
}

// dropIndex finds the column that is next in line to be given up, or -1 when
// every column left is one that has to stay.
func dropIndex(cols []column, order int) int {
	for i, c := range cols {
		if c.dropOrder == order {
			return i
		}
	}
	return -1
}

// trimToFit takes width off the widest column until the row fits, so a narrow
// terminal costs every wide column a little rather than clipping one outright.
func trimToFit(cols []column, budget int) {
	for over := totalWidth(cols) - budget; over > 0; over-- {
		widest := 0
		for i := range cols {
			if cols[i].width > cols[widest].width {
				widest = i
			}
		}
		if cols[widest].width <= minColumnWidth {
			return
		}
		cols[widest].width--
	}
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

func (m browserModel) View() string {
	if m.quit {
		return ""
	}
	// The help is drawn over the view it describes rather than into it, since it
	// is about the keys of that view and not about its content.
	if m.mode == modeHelp {
		return m.helpView()
	}
	// The file view is a whole frame of its own — its own header, its own
	// two-pane grid, its own footer — so it is handed the frame rather than
	// fitted into a region of this one.
	if m.mode == modeFiles {
		return m.files.View()
	}
	if m.mode == modeForm {
		return m.formView()
	}
	width := m.contentWidth()

	// Every frame of the list is the same seven lines whatever the browser is
	// doing, so nothing can push the status or the key hints off the bottom.
	lines := []string{
		margin + m.header(width),
		rule(width),
		margin + m.filter.View(),
		"",
		m.table(width),
		m.statusLine(width),
		margin + m.footer(width),
	}
	return strings.Join(cutFrame(lines, m.height), "\n")
}

// formView draws the add form on the list's own frame: the same header, the same
// status line, the same key hints, with the fields where the table was. The two
// frames are the same height, so opening the form moves nothing but the content.
func (m browserModel) formView() string {
	width := m.contentWidth()
	lines := []string{
		margin + m.formHeader(width),
		rule(width),
	}
	lines = append(lines, m.form.lines(width, 2+m.listHeight())...)
	lines = append(lines, m.statusLine(width), margin+m.footer(width))
	return strings.Join(cutFrame(lines, m.height), "\n")
}

// cutFrame drops the lines a frame has no room for. Every frame is drawn to the
// terminal's height already; this is for a terminal smaller than the chrome a
// frame cannot do without, where the alternative is bubbletea's alt screen
// keeping the last lines of a frame that is too tall — and eating the top of it,
// the header, instead of the bottom.
//
// It is counted in drawn lines rather than in the parts handed to it, because a
// part may be several lines of its own.
func cutFrame(lines []string, height int) []string {
	if height <= 0 {
		return lines
	}
	drawn := make([]string, 0, len(lines))
	for _, line := range lines {
		drawn = append(drawn, strings.Split(line, "\n")...)
	}
	if len(drawn) > height {
		return drawn[:height]
	}
	return drawn
}

// table renders the column labels and the host rows, padded to the height
// reserved for it so the status and the hints keep to the bottom of the screen
// however few hosts there are.
func (m browserModel) table(width int) string {
	lines := m.tableLines(width)
	// It is a budget, not a suggestion: a table that draws over it takes the line
	// from the key hints, and a frame taller than the terminal loses its top to
	// the alt screen. Only a terminal with no room left for a host row at all
	// gets here.
	if len(lines) > m.listHeight() {
		lines = lines[:m.listHeight()]
	}
	for pad := m.listHeight() - len(lines); pad > 0; pad-- {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (m browserModel) tableLines(width int) []string {
	// No columns to label when nothing matched; the empty state says why.
	if len(m.visible) == 0 {
		return []string{m.emptyState()}
	}

	cols := m.tableLayout(width)
	rows, overflow := m.listWindow()
	end := min(m.offset+rows, len(m.visible))

	lines := []string{
		margin + columnsHeader(cols),
		rule(width),
	}
	for i := m.offset; i < end; i++ {
		lines = append(lines, margin+m.row(m.visible[i], i == m.cursor, cols))
	}
	// The overflow counter shares the window with the hosts rather than adding a
	// line of its own; anything else would push the key hints off the bottom.
	if overflow {
		lines = append(lines, margin+hintStyle.Render(fmt.Sprintf("↓ %d more", len(m.visible)-end)))
	}
	return lines
}

// columnsHeader labels the columns, laid out through the same widths as the
// rows so the two stay in step, including after a column is dropped.
func columnsHeader(cols []column) string {
	cells := make([]string, len(cols))
	for i, c := range cols {
		// Nothing follows the last column, so padding it would only leave
		// trailing spaces.
		label := clip(c.header, c.width)
		if i < len(cols)-1 {
			label = padTo(label, c.width, c.right)
		}
		cells[i] = headerStyle.Render(label)
	}
	return strings.Repeat(" ", rowIndent) + strings.Join(cells, columnSpace)
}

// columnSpace is the gap written between two columns. It is a variable rather
// than a call at each site because every row is built from it.
var columnSpace = strings.Repeat(" ", columnGap)

func (m browserModel) header(width int) string {
	left := titleStyle.Render("ohmyssh") + "  " +
		countStyle.Render(fmt.Sprintf("%d of %d hosts", len(m.visible), len(m.hosts)))
	return headerLine(left, sourceHint(m.hosts), width)
}

// formHeader is the list's header with the form named in place of the count, and
// the file the host will be written to where the sources of the hosts are.
func (m browserModel) formHeader(width int) string {
	left := titleStyle.Render("ohmyssh") + "  " + countStyle.Render("new host")
	return headerLine(left, abbreviated(m.hostsFile), width)
}

// headerLine draws the two ends of a header: the title on the left, and the
// quiet note under it flush against the right. A line wider than the frame
// wraps, and a wrapped line pushes everything below it past the bottom of a
// frame whose height was counted without it, so when both do not fit the note is
// dropped and the title is cut instead.
func headerLine(left, right string, width int) string {
	if right == "" || lipgloss.Width(left)+lipgloss.Width(right)+4 > width {
		return clip(left, width)
	}
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	return left + strings.Repeat(" ", max(gap, 1)) + countStyle.Render(right)
}

// homeDir is where the user's home directory was when the browser started. It is
// read once because it cannot change while one is running, and it is used only
// to write paths the way the user thinks of them.
var homeDir, _ = os.UserHomeDir()

// abbreviated shortens the user's home directory to ~, which is how the rest of
// the world writes it and a dozen columns shorter than the path — the difference
// between the header saying which file a host came from and not.
func abbreviated(path string) string {
	if path == "" || homeDir == "" {
		return path
	}
	rest, found := strings.CutPrefix(path, homeDir)
	if !found {
		return path
	}
	return "~" + rest
}

// sourceHint names the files the hosts came from: usually the user's ssh config
// alone, and with it the file ohmyssh keeps its own hosts in once one has been
// added. Which of the two a host is in is the first thing to know when one of
// them will not connect, so both are named, and files repeat, so each is named
// once.
func sourceHint(hosts []config.SSHHost) string {
	var sources []string
	for _, host := range hosts {
		if host.SourceFile == "" || slices.Contains(sources, host.SourceFile) {
			continue
		}
		sources = append(sources, host.SourceFile)
	}
	for i, source := range sources {
		sources[i] = abbreviated(source)
	}
	return strings.Join(sources, " + ")
}

// row renders one host. The selected row is painted as a single bar: a per-cell
// style nested inside it would reset the bar's background at every column
// boundary, so the bar is applied to plain text instead.
func (m browserModel) row(host config.SSHHost, active bool, cols []column) string {
	cells := make([]string, len(cols))
	for i, c := range cols {
		cell := clip(c.cell(host), c.width)
		if i < len(cols)-1 {
			// The last column has nothing to its right to line up with.
			cell = padTo(cell, c.width, c.right)
		}
		if active {
			cells[i] = cell
			continue
		}
		cells[i] = c.style.Render(cell)
	}

	marker := strings.Repeat(" ", rowIndent)
	if active {
		marker = rowMarker
	}
	line := marker + strings.Join(cells, columnSpace)
	if active {
		// The bar covers the row's own cells rather than the rest of the line: a
		// highlight running to the right edge of a wide terminal is a slab of colour
		// with a host name in its corner, and it draws the eye to the empty space
		// instead of the row. The width is the columns' rather than the line's, so
		// it stays put as the cursor moves between rows of different lengths.
		return rowSelectedStyle.Width(rowIndent + totalWidth(cols)).Render(line)
	}
	return line
}

// tagText renders a host's tags the way the list shows them, or "" when it has
// none.
func tagText(host config.SSHHost) string {
	if len(host.Tags) == 0 {
		return ""
	}
	return "#" + strings.Join(host.Tags, " #")
}

func (m browserModel) emptyState() string {
	if len(m.hosts) == 0 {
		// Nothing on screen offers a key that does nothing: the empty state and
		// the hint bar agree on whether a host can be added from here.
		if m.add == nil {
			return margin + emptyStyle.Render("No hosts found. Add a Host block to ~/.ssh/config.")
		}
		return margin + emptyStyle.Render("No hosts found. Press A to add one.")
	}
	return margin + emptyStyle.Render("No hosts match the filter. Press esc to clear it.")
}

// statusLine reports the outcome of the session that just ended, or asks a
// question the browser is waiting on an answer to. It holds its line whether or
// not there is anything to say, so the key hints underneath never move, and it is
// clipped rather than wrapped for the same reason — which is also why the
// question is short enough to keep the answer on the same line.
func (m browserModel) statusLine(width int) string {
	if m.mode == modeConfirm {
		return margin + questionStyle.Render(clip(statusMarkerAsk+m.confirmQuestion(), width))
	}
	if m.status == "" {
		return ""
	}
	style, marker := statusStyle, statusMarkerOK
	if m.failed {
		style, marker = errorStyle, statusMarkerFailed
	}
	return margin + style.Render(clip(marker+m.status, width))
}

// confirmQuestion is what the browser is waiting to be told. It names the host
// and the file, since the question is about both, and it ends with the two
// answers so the keys are readable from the question itself.
func (m browserModel) confirmQuestion() string {
	where := ""
	if m.hostsFile != "" {
		where = " from " + abbreviated(m.hostsFile)
	}
	return fmt.Sprintf("delete %s%s?  y/n", m.pending.Name, where)
}

// footer lists the keys whichever mode is up answers to. It is drawn on every
// frame, including while a status is showing, so the bindings are always on
// screen. Segments are added while they fit, which trims the bar from the right
// on a narrow terminal instead of wrapping it onto a second line.
func (m browserModel) footer(width int) string {
	segments := m.footerSegments()
	switch m.mode {
	case modeForm:
		segments = formFooterSegments()
	case modeConfirm:
		segments = confirmFooterSegments()
	case modeHelp:
		segments = helpFooterSegments()
	}
	return fitSegments(segments, width)
}

// confirmFooterSegments are the two answers to the delete question. Deleting
// comes first because the question ends "y/n" and the bar reads in the same
// order; cancelling is the answer that needs no hint, since every key that is
// not a "y" is one.
func confirmFooterSegments() []binding {
	return []binding{
		{"y", "delete"},
		{"n", "cancel"},
	}
}

// fitSegments is the bar itself: the hints, in the order they are given up when
// the line runs out of room.
func fitSegments(segments []binding, width int) string {
	// Two columns of gap rather than three: at the usual 80-column terminal it
	// is the difference between the whole list of bindings fitting and the last
	// one being given up.
	const separator = "  "

	line := keyHint(segments[0])
	for _, segment := range segments[1:] {
		hint := keyHint(segment)
		if lipgloss.Width(line)+len(separator)+lipgloss.Width(hint) > width {
			break
		}
		line += separator + hint
	}
	return line
}

// footerSegments are the list's bindings, in the order they are given up when
// the line runs out of room: the last segment is the first to go.
//
// Every binding here is discoverable only by reading this bar, except the filter
// hint, which the filter box spells out in its own placeholder on the line above
// — so it is last, and it is what pays for the help binding at 80 columns. The
// help goes ahead of it because there is nothing else on screen that says how
// to reach the list of keys the bar itself is too short to hold; the filter's
// box, two lines up, is that list for the filter.
//
// The move hint is written without its slash and the transfer hint as "files"
// rather than "transfer" for one reason: at 80 columns with a host service
// attached, that is what makes room for the help without giving up a binding.
// The transfer keys are one segment rather than two so that they stay together
// when the line is short; which of the two opens which pane is said where it
// matters, in the file view's own bar. The add and delete bindings are capital
// letters the user cannot guess, so they are kept ahead of everything below
// them.
func (m browserModel) footerSegments() []binding {
	segments := []binding{
		{"enter", "connect"},
		{"↑↓", "move"},
		{"esc", "clear"},
		{"q", "quit"},
	}
	if m.add != nil {
		segments = append(segments, binding{"A", "add"})
	}
	if m.remove != nil {
		segments = append(segments, binding{"X", "delete"})
	}
	segments = append(segments,
		binding{"U/D", "files"},
		binding{"?", "help"})
	return append(segments, binding{"type", "filter"})
}

// ---------------------------------------------------------------------------
// Text fitting
// ---------------------------------------------------------------------------

// padTo fills a cell out to its column width. Numeric columns pad on the left so
// their digits line up under the header.
func padTo(s string, width int, right bool) string {
	gap := width - lipgloss.Width(s)
	if gap <= 0 {
		return s
	}
	if right {
		return strings.Repeat(" ", gap) + s
	}
	return s + strings.Repeat(" ", gap)
}

// clip shortens s to at most width display columns, marking a cut with an
// ellipsis. Columns are a fixed grid, so a cell that outgrows its column has to
// be cut rather than allowed to push the rest of the row along.
func clip(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}

	var b strings.Builder
	used := 0
	for _, r := range s {
		// One column is held back for the ellipsis.
		rw := lipgloss.Width(string(r))
		if used+rw > width-1 {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String() + "…"
}

// clipTail is clip the other way round: it keeps the end of s and cuts the
// front. A directory is named by its last component — a path cut at the end says
// only which tree it is in, which is the half the reader already knows.
func clipTail(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}

	runes := []rune(s)
	used := 0
	start := len(runes)
	for i := len(runes) - 1; i >= 0; i-- {
		// One column is held back for the leading ellipsis.
		rw := lipgloss.Width(string(runes[i]))
		if used+rw > width-1 {
			break
		}
		used += rw
		start = i
	}
	return "…" + string(runes[start:])
}
