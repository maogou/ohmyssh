package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/maogou/ohmyssh/internal/config"
	"github.com/maogou/ohmyssh/internal/sshclient"
)

// The file view is two panes over one session: the local filesystem on the left
// and the host's on the right, each walked with the arrow keys and the whole
// view driven from the pane that has the keyboard. Enter on a file sends it to
// whichever directory the other pane is showing, c sends a directory whole, and
// every transfer rides the connection the panes were browsed over rather than
// dialling for itself.

const (
	// fileNarrowWidth is where two panes stop fitting side by side. Below it one
	// pane is drawn at a time — two columns narrower than the names in them are
	// worse than one column and a tab key.
	fileNarrowWidth = 80

	// fileGap is the space between the two panes, and fileSizeWidth is the column
	// a file's size is right-aligned in. Both are part of the row grid, so a pane
	// line is exactly as wide as the pane however long a name is.
	fileGap       = 2
	fileSizeWidth = 9

	// fileProgressLines is the height of the live progress block. It is always
	// held, blank while nothing is running, so a transfer does not move the panes
	// under the cursor that started it.
	//
	// Two is not a choice made here: liveFrame draws exactly two lines and the
	// command line's live display rewrites a frame by moving the cursor back up
	// over that many.
	fileProgressLines = 2

	// progressBuffer is how many reports may be waiting to be drawn. A transfer
	// that outruns the renderer fills this rather than blocking on it, and more
	// than a frame's worth of reports is never worth drawing anyway.
	progressBuffer = 64
)

// paneSide names one of the two panes. The local one comes first because it is
// drawn first, on the left.
type paneSide int

const (
	paneLocal paneSide = iota
	paneRemote
	paneCount
)

// TransferRequest is one transfer the browser asked for: the two paths and which
// way the bytes go.
//
// It is the browser's own type rather than the service's, because service
// imports ui and not the other way round — the same reason SessionFunc exists.
type TransferRequest struct {
	Local     string
	Remote    string
	Direction sshclient.Direction
}

// transferProgressMsg carries one report from the running transfer into the
// event loop. generation names the transfer that sent it.
type transferProgressMsg struct {
	generation int
	progress   sshclient.Progress
}

// transferDoneMsg reports that the transfer stopped. The count that was running
// is what says which one, since a cancelled transfer's last messages can arrive
// after the next one has begun.
type transferDoneMsg struct {
	generation int
	err        error
}

// remoteOpenedMsg carries the session the file view asked for, or why it could
// not be opened. Dialling happens on a goroutine of its own, so the frame keeps
// being drawn while the connection is made.
type remoteOpenedMsg struct {
	session RemoteSession
	err     error
}

// remoteEntriesMsg carries a remote listing back. path says which directory it
// is of, so a reply for one the user has already walked past can be dropped.
type remoteEntriesMsg struct {
	path    string
	entries []RemoteEntry
	err     error
}

// entry is one row of a pane. Both ends are turned into it — the left one out of
// the local filesystem, the right one out of a RemoteEntry — so that a row is
// drawn one way and the two columns cannot come to read differently.
type entry struct {
	name string
	dir  bool
	size int64
	// path is the entry's full path on its own end: a filesystem path in the left
	// pane and a remote one in the right. A transfer is started from it, so it is
	// the path the pane was showing rather than one rebuilt out of the name.
	path string
}

// filePane is one column: where it is, what is there, and how far down the list
// the user has walked.
type filePane struct {
	// cwd is the directory the pane shows. The remote one is empty until the
	// session arrives.
	cwd string
	// entries is the whole listing, in the order the pane draws it.
	entries []entry
	// cursor and offset are into the filtered list rather than into entries: the
	// filter is what the user is looking at, so it is what the cursor counts
	// through.
	cursor int
	offset int
	// pending is the directory a listing is on its way back for. A reply for
	// anything else is one the user has already walked past.
	pending string
	// reveal is the entry the cursor should land on once the next listing
	// arrives. Stepping back up out of a directory should leave the cursor on it,
	// since that is the row the user is looking for after stepping back.
	reveal string
}

// filesModel is the browser's file view: the local filesystem on the left, the
// host on the right, the right one opened in the directory the login landed in.
//
// It is a model of its own rather than more fields on browserModel because it
// takes the keyboard and the whole frame over while it is up, and because two
// panes, a session and a filter of its own are more state than the host list has
// any use for.
type filesModel struct {
	host config.SSHHost
	// session is the connection both panes and every transfer go over. It is nil
	// until the dial comes back, and nil again once the view has closed.
	session RemoteSession

	panes [paneCount]filePane
	focus paneSide

	// filter narrows the focused pane, and filtering is whether its line is up
	// and taking keystrokes. Closing the line with enter leaves the query
	// applied, so the matches can then be walked with the arrows — which is what
	// typing it was for.
	filter    textinput.Model
	filtering bool

	// opening is true between asking for a session and it arriving. It is only
	// ever about the view's own connection; a listing that failed says so in
	// status.
	opening bool
	// loading is true while a remote listing is in flight.
	loading bool

	// request, progress, events, done, cancel and generation are the running
	// transfer. generation counts the transfers started, so that a report from
	// one the user has moved past cannot be drawn over its successor.
	request    TransferRequest
	progress   sshclient.Progress
	events     <-chan sshclient.Progress
	done       <-chan error
	cancel     context.CancelFunc
	generation int

	// ctx is the lifetime of the view, and stopOpen cancels it. A dial that is
	// still out when the user backs all the way out is stopped by it rather than
	// left to land on a view that has closed.
	ctx      context.Context
	stopOpen context.CancelFunc
	// opened is whether the view is still up. A session that arrives after it has
	// come down is closed on arrival: no one is left to browse with it.
	opened bool
	// leaving is set when the user has asked to leave, which is what browserModel
	// reads to take the frame back to the host list.
	leaving bool

	status string
	failed bool

	width  int
	height int
}

// newFilesModel opens the file view on host, with the keyboard in the pane the
// key that opened it was about: U starts on the local files, D on the remote
// ones. The left pane is filled in at once — it is the filesystem this process
// is already on — while the right one waits for the session openCmd asks for.
//
// width and height are the terminal's, not the filter box's: the two differ by
// the box's own chrome, and a view given the latter lays its panes out a few
// columns narrow.
//
// The size is taken here rather than left to the next resize, which is what the
// host list has already been told it. Opened without one, the panes are drawn to
// a default width that matches no terminal until the user happens to resize the
// window, and every line of the frame moves.
func newFilesModel(host config.SSHHost, width, height int, focus paneSide) filesModel {
	filter := textinput.New()
	filter.Placeholder = "filter this pane"
	filter.Prompt = filterPrompt
	filter.PromptStyle = cursorStyle
	filter.TextStyle = nameStyle
	filter.Cursor.Style = cursorStyle
	filter.Width = filterWidth(width)

	ctx, cancel := context.WithCancel(context.Background())
	m := filesModel{
		host:     host,
		focus:    focus,
		filter:   filter,
		opening:  true,
		ctx:      ctx,
		stopOpen: cancel,
		opened:   true,
	}
	// One place turns a size into a layout, so the first frame is laid out exactly
	// as every frame after a resize is.
	m.resize(width, height)
	m.panes[paneLocal].cwd = localStartDir()
	m.reloadLocal()
	return m
}

// openCmd dials the host and opens the session both panes read over. It runs on
// a goroutine of its own: a host that is not answering takes the dial timeout to
// say so, and the frame has to keep being drawn while it does.
func (m filesModel) openCmd(open RemoteSessionFunc) tea.Cmd {
	ctx, host := m.ctx, m.host
	return func() tea.Msg {
		session, err := open(ctx, host)
		return remoteOpenedMsg{session: session, err: err}
	}
}

// resize records the terminal's size. The panes have no widths of their own —
// those are computed from this at drawing time — so only the filter input, which
// sizes its own scrolling window, has to be told.
func (m *filesModel) resize(width, height int) {
	m.width, m.height = width, height
	m.filter.Width = filterWidth(width)
}

// close shuts the view down: the transfer that is running, the session both
// panes were reading, and the dial that may still be out.
//
// It is safe to call on a view that never opened any of them and on one that has
// already been closed, which is what lets the browser call it both when the user
// leaves the view and again at exit.
func (m *filesModel) close() {
	m.opened = false

	m.stopTransfer()
	// The pipe is let go as well as the context, so a report that arrives after
	// the view has closed finds nothing waiting for it to re-arm.
	m.events, m.done, m.cancel = nil, nil, nil

	if m.session != nil {
		_ = m.session.Close()
		m.session = nil
	}
	if m.stopOpen != nil {
		m.stopOpen()
		m.stopOpen = nil
	}
}

// Update answers a message from the file view's own goroutines.
func (m filesModel) Update(msg tea.Msg) (filesModel, tea.Cmd) {
	switch msg := msg.(type) {
	case remoteOpenedMsg:
		return m.applySession(msg)

	case remoteEntriesMsg:
		return m.applyEntries(msg)

	case transferProgressMsg:
		if !m.transferring() || msg.generation != m.generation {
			return m, nil
		}
		m.progress = msg.progress
		return m, m.waitForTransfer()

	case transferDoneMsg:
		if !m.transferring() || msg.generation != m.generation {
			return m, nil
		}
		m.applyTransferOutcome(msg.err)
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// applySession takes the dial's answer: the remote pane opens in the directory
// the login landed in, which is the user's home directory there.
func (m filesModel) applySession(msg remoteOpenedMsg) (filesModel, tea.Cmd) {
	if !m.opened {
		// The view came down while the dial was out. Nothing is left to browse
		// with, and the session would otherwise sit on a connection that no one
		// is ever going to close.
		if msg.session != nil {
			_ = msg.session.Close()
		}
		return m, nil
	}

	m.opening = false
	if msg.err != nil {
		m.status = msg.err.Error()
		m.failed = true
		return m, nil
	}

	m.session = msg.session
	m.failed = false

	remote := m.at(paneRemote)
	remote.cursor, remote.offset = 0, 0
	// The pane's directory is not set here: it becomes whatever the listing that
	// comes back was of, so a directory that will not open cannot leave the pane
	// claiming to be somewhere it never reached.
	return m, m.loadInto(msg.session.Home())
}

// applyEntries puts a listing into the pane that asked for it. A reply for a
// directory the pane is no longer waiting on is dropped: the user has walked on,
// and drawing it would put one directory's files under another's path.
func (m filesModel) applyEntries(msg remoteEntriesMsg) (filesModel, tea.Cmd) {
	remote := m.at(paneRemote)
	if msg.path != remote.pending {
		return m, nil
	}
	remote.pending = ""
	m.loading = false

	if msg.err != nil {
		// The pane stays where it was. A directory that will not open is nearly
		// always a permission or a name that is no longer there, and dropping the
		// user back to an empty column would lose the listing they still want.
		m.status = fmt.Sprintf("%s: %v", msg.path, msg.err)
		m.failed = true
		return m, nil
	}

	remote.cwd = msg.path
	remote.entries = remoteEntries(msg.entries, msg.path)
	if i := indexOf(remote.entries, remote.reveal); i >= 0 {
		remote.cursor = i
	}
	remote.reveal = ""
	m.clampScroll(paneRemote)
	return m, nil
}

// at returns a pane so it can be written through. The receiver is a pointer for
// that reason; its callers are value-receiver methods, so it writes into the
// copy bubbletea is about to keep.
func (m *filesModel) at(which paneSide) *filePane { return &m.panes[which] }

// ready reports whether the remote pane has a session to talk to. Anything that
// would ask the host something checks it first: a key pressed while the dial is
// still out must do nothing rather than reach a nil session.
func (m filesModel) ready() bool { return m.session != nil }

// transferring reports whether a transfer is running, which is what esc, the
// progress block and the status line are all about.
func (m filesModel) transferring() bool { return m.events != nil }

// ---------------------------------------------------------------------------
// Keys
// ---------------------------------------------------------------------------

// handleKey answers a key while the file view is up.
func (m filesModel) handleKey(msg tea.KeyMsg) (filesModel, tea.Cmd) {
	// The filter owns the keyboard while its line is up, which is what leaves the
	// letters free to be bindings the rest of the time.
	if m.filtering {
		return m.handleFilterKey(msg)
	}

	switch msg.String() {
	case "esc":
		switch {
		case m.cancel != nil:
			// The bars stay up until the transfer has actually stopped, so they
			// cannot vanish while bytes are still moving: the outcome message is
			// what clears them. cancel is cleared here rather than in
			// stopTransfer, whose receiver is a copy — a second esc would
			// otherwise reach a context that has already been cancelled.
			m.stopTransfer()
			m.cancel = nil
			m.status = "cancelling…"
			m.failed = false
		case m.filter.Value() != "":
			m.filter.SetValue("")
			m.clampScroll(m.focus)
		default:
			// All the way out, which is the only way back to the host list.
			m.leaving = true
		}
		return m, nil

	case "tab", "shift+tab":
		m.switchPane()
		return m, nil

	case "up", "k", "ctrl+p":
		m.move(-1)
		return m, nil
	case "down", "j", "ctrl+n":
		m.move(1)
		return m, nil
	case "pgup":
		m.move(-m.paneRows())
		return m, nil
	case "pgdown":
		m.move(m.paneRows())
		return m, nil
	case "home", "g":
		m.cursorTo(0)
		return m, nil
	case "end", "G":
		m.cursorTo(len(m.visible(m.focus)) - 1)
		return m, nil

	case "enter", "right", "l":
		return m, m.openEntry()

	case "left", "h", "backspace":
		return m, m.goUp()

	case "c":
		// The whole of whatever the cursor is on, directory or not, which is the
		// half of the pair that enter cannot do.
		return m, m.transferEntry()

	case "/":
		m.filtering = true
		m.filter.SetValue("")
		m.clampScroll(m.focus)
		return m, m.filter.Focus()

	case "r":
		return m, m.reload()
	}
	return m, nil
}

// handleFilterKey answers a key while the filter line is up. Everything that is
// not a way out of it is a character, which is why the ways out are handled
// before the input sees them.
func (m filesModel) handleFilterKey(msg tea.KeyMsg) (filesModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// esc throws the query away and enter keeps it: after enter the arrows
		// walk the matches with the rest of the keyboard still theirs.
		m.filtering = false
		m.filter.SetValue("")
		m.filter.Blur()
		m.clampScroll(m.focus)
		return m, nil

	case "enter":
		m.filtering = false
		m.filter.Blur()
		m.clampScroll(m.focus)
		return m, nil

	case "tab", "shift+tab":
		// The filter belongs to whichever pane has the keyboard, so switching
		// takes it along.
		m.filtering = false
		m.filter.Blur()
		m.switchPane()
		return m, nil
	}

	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(msg)
	m.clampScroll(m.focus)
	return m, cmd
}

// switchPane gives the keyboard to the other pane. The filter follows it, so
// both lists are re-anchored: the one gaining the keyboard has just been
// narrowed by a query the other one was not subject to.
func (m *filesModel) switchPane() {
	m.focus = (m.focus + 1) % paneCount
	for _, which := range []paneSide{paneLocal, paneRemote} {
		m.clampScroll(which)
	}
}

// move walks the cursor down the focused pane's filtered list.
func (m *filesModel) move(delta int) {
	m.cursorTo(m.focused().cursor + delta)
}

// cursorTo puts the cursor at index, clamped to the list.
func (m *filesModel) cursorTo(index int) {
	m.focused().cursor = index
	m.clampScroll(m.focus)
}

// focused is the pane with the keyboard. It is a pointer so it can be written
// through, for the same reason at is.
func (m *filesModel) focused() *filePane { return &m.panes[m.focus] }

// clampScroll keeps a pane's cursor inside its filtered list and its window
// around its cursor.
func (m *filesModel) clampScroll(which paneSide) {
	p := m.at(which)
	count := len(m.visible(which))
	if count == 0 {
		p.cursor, p.offset = 0, 0
		return
	}
	p.cursor = min(max(p.cursor, 0), count-1)

	rows := m.paneRows()
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+rows {
		p.offset = p.cursor - rows + 1
	}
	p.offset = min(max(p.offset, 0), max(count-rows, 0))
}

// current is the entry under the cursor, if the pane has one.
func (m filesModel) current() (entry, bool) {
	visible := m.visible(m.focus)
	cursor := m.panes[m.focus].cursor
	if cursor < 0 || cursor >= len(visible) {
		return entry{}, false
	}
	return visible[cursor], true
}

// openEntry answers enter: a directory is walked into, and anything else is sent
// to the other pane's directory.
func (m *filesModel) openEntry() tea.Cmd {
	e, ok := m.current()
	if !ok {
		return nil
	}
	if e.dir {
		return m.descend(e)
	}
	return m.transferEntry()
}

// descend walks the focused pane into a directory.
func (m *filesModel) descend(e entry) tea.Cmd {
	p := m.focused()
	p.cursor, p.offset, p.reveal = 0, 0, ""

	if m.focus == paneRemote {
		if !m.ready() {
			return nil
		}
		return m.loadInto(e.path)
	}
	p.cwd = e.path
	m.reloadLocal()
	return nil
}

// goUp walks the focused pane to its parent directory.
func (m *filesModel) goUp() tea.Cmd {
	p := m.focused()
	var parent, leaving string
	if m.focus == paneRemote {
		parent, leaving = path.Dir(p.cwd), path.Base(p.cwd)
	} else {
		parent, leaving = filepath.Dir(p.cwd), filepath.Base(p.cwd)
	}
	// A root is its own parent, which is how a pane knows there is nowhere above
	// it to go.
	if parent == p.cwd || parent == "" {
		return nil
	}

	p.reveal = leaving
	if m.focus == paneRemote {
		if !m.ready() {
			return nil
		}
		return m.loadInto(parent)
	}
	p.cwd = parent
	m.reloadLocal()
	return nil
}

// reload re-reads the focused pane's directory, which is what r is for: files
// appear and go away underneath a pane that is holding an older listing.
func (m *filesModel) reload() tea.Cmd {
	if m.focus != paneRemote {
		m.reloadLocal()
		return nil
	}
	if !m.ready() {
		return nil
	}
	return m.loadInto(m.panes[paneRemote].cwd)
}

// loadCmd reads a remote directory. It is a round trip per directory, so it runs
// on a goroutine of its own while the pane goes on drawing what it has.
func (m filesModel) loadCmd(dir string) tea.Cmd {
	session := m.session
	return func() tea.Msg {
		entries, err := session.ReadDir(dir)
		return remoteEntriesMsg{path: dir, entries: entries, err: err}
	}
}

// loadInto starts a listing of dir into the remote pane. Recording which
// directory is on its way is what lets a reply for one the user has already
// walked past be dropped rather than drawn under the wrong path.
func (m *filesModel) loadInto(dir string) tea.Cmd {
	m.at(paneRemote).pending = dir
	m.loading = true
	return m.loadCmd(dir)
}

// reloadLocal re-reads the local pane's directory. The local filesystem is this
// process's own, so the read happens here rather than on a goroutine: it is the
// call the shell that started ohmyssh would have made.
func (m *filesModel) reloadLocal() {
	local := m.at(paneLocal)
	entries, err := listLocal(local.cwd)
	if err != nil {
		m.status = fmt.Sprintf("%s: %v", local.cwd, err)
		m.failed = true
		return
	}
	local.entries = entries
	if i := indexOf(entries, local.reveal); i >= 0 {
		local.cursor = i
	}
	local.reveal = ""
	m.clampScroll(paneLocal)
}

// ---------------------------------------------------------------------------
// Transfers
// ---------------------------------------------------------------------------

// transferEntry sends whatever the cursor is on across, in the direction its
// pane says: an entry in the left pane goes to the directory the right one is
// showing, and one in the right pane comes back to the left one's.
//
// A directory goes whole. It is the same plan the command line builds for a
// directory, so a tree moves the same way whether it was named on a command line
// or picked out of a pane.
func (m *filesModel) transferEntry() tea.Cmd {
	e, ok := m.current()
	if !ok {
		return nil
	}
	if m.transferring() {
		m.status = "a transfer is already running"
		m.failed = true
		return nil
	}
	if !m.ready() {
		m.status = "no session yet: still connecting"
		m.failed = true
		return nil
	}

	var req TransferRequest
	if m.focus == paneLocal {
		req = TransferRequest{
			Local: e.path,
			// The name is joined onto the other pane's directory, so a directory
			// lands as a directory of its own there rather than spilling its
			// contents into the middle of it.
			Remote:    path.Join(m.panes[paneRemote].cwd, e.name),
			Direction: sshclient.Upload,
		}
	} else {
		req = TransferRequest{
			Local:     filepath.Join(m.panes[paneLocal].cwd, e.name),
			Remote:    e.path,
			Direction: sshclient.Download,
		}
	}
	return m.beginTransfer(req)
}

// beginTransfer runs the transfer on a goroutine of its own and returns the
// command that reads its first report.
//
// Each report read re-arms the next read, so exactly one reader is ever in
// flight and the event loop never blocks on the channel.
func (m *filesModel) beginTransfer(req TransferRequest) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.generation++
	m.request = req
	m.progress = sshclient.Progress{}
	m.status = ""
	m.failed = false

	events := make(chan sshclient.Progress, progressBuffer)
	// Buffered, so the producer is never left waiting for a reader that has
	// stopped reading: a cancelled transfer's last report lands here and is
	// dropped rather than blocking the goroutine that is trying to end.
	done := make(chan error, 1)

	session := m.session
	go func() {
		err := session.Transfer(ctx, req, func(p sshclient.Progress) {
			select {
			case events <- p:
			case <-ctx.Done():
			}
		})
		// The error is handed over before the close, so whoever sees the close
		// already has the outcome waiting for them.
		done <- err
		close(events)
	}()

	m.events, m.done = events, done
	return m.waitForTransfer()
}

// waitForTransfer reads one report, or the end of the transfer, and re-arms.
func (m filesModel) waitForTransfer() tea.Cmd {
	generation, events, done := m.generation, m.events, m.done
	return func() tea.Msg {
		if p, ok := <-events; ok {
			return transferProgressMsg{generation: generation, progress: p}
		}
		return transferDoneMsg{generation: generation, err: <-done}
	}
}

// stopTransfer cancels a transfer that is still running. It is safe to call when
// there is none.
//
// It reads the cancel function from its own copy of the model and so cannot clear
// it: whoever wants it cleared has to do that on the model it keeps, which is why
// the esc binding sets the field itself.
func (m filesModel) stopTransfer() {
	if m.cancel != nil {
		m.cancel()
	}
}

// applyTransferOutcome says how the transfer ended.
//
// Backing out is not a failure: the user asked for it, so it is reported without
// the failed marker. The pipe is let go here, which is what takes the progress
// block down and gives the keys back.
func (m *filesModel) applyTransferOutcome(err error) {
	from, to := TransferEnds(m.request.Direction, m.request.Local, m.request.Remote)
	verb := m.request.Direction.Verb()

	switch {
	case err == nil:
		m.status = TransferSummary{
			Direction: m.request.Direction,
			From:      from,
			To:        to,
			Files:     m.progress.FilesTotal,
			Bytes:     m.progress.TotalBytes,
		}.Line()
		m.failed = false

	case errors.Is(err, context.Canceled):
		m.status = fmt.Sprintf("cancelled %s %s → %s", verb, from, to)
		m.failed = false

	default:
		m.status = TransferSummary{
			Direction: m.request.Direction,
			From:      from,
			To:        to,
			Err:       err,
		}.Line()
		m.failed = true
	}

	m.cancel = nil
	m.events, m.done = nil, nil
}

// ---------------------------------------------------------------------------
// Listings
// ---------------------------------------------------------------------------

// visible is the entries a pane draws. A filter narrows only the pane that has
// the keyboard and only while a query is up: the query is about one pane, and a
// second one quietly missing rows would be hiding files nobody asked about.
func (m filesModel) visible(which paneSide) []entry {
	entries := m.panes[which].entries
	query := m.query()
	if query == "" || which != m.focus {
		return entries
	}

	matched := make([]entry, 0, len(entries))
	for _, e := range entries {
		if strings.Contains(strings.ToLower(e.name), query) {
			matched = append(matched, e)
		}
	}
	return matched
}

// query is the filter as a match string: lower case, and without the surrounding
// spaces that would only ever have been typed by accident.
func (m filesModel) query() string {
	return strings.ToLower(strings.TrimSpace(m.filter.Value()))
}

// listLocal reads a directory into the pane's own entry shape.
//
// Entries whose name begins with a dot are left out, which is what the reference
// browser this view follows does. The remote pane leaves them out too: two
// columns of files that disagree about what a file is would read as a bug, and
// the panes are always read side by side.
func listLocal(dir string) ([]entry, error) {
	listed, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	entries := make([]entry, 0, len(listed))
	for _, item := range listed {
		if strings.HasPrefix(item.Name(), ".") {
			continue
		}
		e := entry{
			name: item.Name(),
			dir:  item.IsDir(),
			path: filepath.Join(dir, item.Name()),
		}
		if !e.dir {
			// A size that cannot be read is left at zero rather than dropping the
			// entry: the file is there, and the transfer will say more about it
			// than the listing can.
			if info, err := item.Info(); err == nil {
				e.size = info.Size()
			}
		}
		entries = append(entries, e)
	}
	sortEntries(entries)
	return entries, nil
}

// remoteEntries is listLocal's counterpart for a listing that came over the
// session.
func remoteEntries(listed []RemoteEntry, dir string) []entry {
	entries := make([]entry, 0, len(listed))
	for _, item := range listed {
		if strings.HasPrefix(item.Name, ".") {
			continue
		}
		entries = append(entries, entry{
			name: item.Name,
			dir:  item.Dir,
			size: item.Size,
			path: path.Join(dir, item.Name),
		})
	}
	sortEntries(entries)
	return entries
}

// sortEntries puts the directories first and then sorts by name, which is the
// order a listing is read in: the ways further in before the files at this
// level. Matching is case-insensitive, so that a name is not filed under a
// letter the reader would have to remember it was capitalised with.
func sortEntries(entries []entry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].dir != entries[j].dir {
			return entries[i].dir
		}
		left, right := strings.ToLower(entries[i].name), strings.ToLower(entries[j].name)
		if left != right {
			return left < right
		}
		return entries[i].name < entries[j].name
	})
}

// indexOf finds an entry by name, for a cursor to land on after a reload.
func indexOf(entries []entry, name string) int {
	if name == "" {
		return -1
	}
	for i, e := range entries {
		if e.name == name {
			return i
		}
	}
	return -1
}

// localStartDir is where the left pane opens: the directory ohmyssh was started
// in, which is the one the user was looking at when they typed the command. A
// working directory that cannot be read falls back to the home directory rather
// than to an empty pane.
func localStartDir() string {
	if wd, err := os.Getwd(); err == nil && wd != "" {
		return wd
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home
	}
	return string(os.PathSeparator)
}

// ---------------------------------------------------------------------------
// Layout
// ---------------------------------------------------------------------------

// narrow reports whether the terminal is too narrow for two panes. Below this
// the two columns would be narrower than the names in them, so one is drawn at a
// time and tab brings the other forward.
func (m filesModel) narrow() bool {
	return m.width > 0 && m.width < fileNarrowWidth
}

// contentWidth is the width every line is drawn to once the left margin is
// accounted for. It comes from the same grid the host list is drawn on, so a
// filter box at the top of either view is the same box.
func (m filesModel) contentWidth() int {
	if m.width <= 0 {
		return contentWidth(defaultBrowserWidth)
	}
	return contentWidth(m.width)
}

// paneWidth is how wide one pane is drawn: the whole content width when only one
// is up, otherwise half of it less the gap. The division is exact rather than
// floored at some minimum so that two panes and the gap between them can never
// come to more than the content width, whatever the terminal does.
func (m filesModel) paneWidth(width int) int {
	if m.narrow() {
		return width
	}
	return max((width-fileGap)/2, 1)
}

// paneRows is how many rows the panes get: what is left of the terminal once the
// chrome is paid for. It is counted from the chrome rather than held as a
// constant, so a line added to the frame cannot quietly push the key hints off
// the bottom of it.
func (m filesModel) paneRows() int {
	if m.height <= 0 {
		return defaultListHeight
	}
	width := m.contentWidth()
	return max(m.height-len(m.topLines(width))-len(m.bottomLines(width)), 1)
}

// topLines is everything above the panes: what the view is, where the two panes
// are, and the rules that separate them from the files.
func (m filesModel) topLines(width int) []string {
	lines := []string{
		margin + m.header(width),
		rule(width),
	}
	lines = append(lines, m.pathLines(width)...)
	return append(lines, rule(width))
}

// bottomLines is everything below the panes. The progress block keeps its two
// lines whether or not a transfer is running, for the same reason the status
// line keeps its one: space that comes and goes would move the panes on every
// transfer, and the row the user was reading would move with them.
func (m filesModel) bottomLines(width int) []string {
	return append(m.progressLines(width),
		m.statusLine(width),
		margin+m.footer(width),
	)
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// View draws the frame.
//
// Its height is the terminal's, always. The panes get whatever the chrome leaves
// them, and the assembled frame is cut to the terminal's height as a last
// resort — because bubbletea's alt screen keeps the last lines of a frame that
// is too tall, so what would vanish is the top of it: the header and the paths,
// while the model went on believing it had drawn the whole thing.
func (m filesModel) View() string {
	width := m.contentWidth()
	lines := append(m.topLines(width), m.paneLines(width, m.paneRows())...)
	lines = append(lines, m.bottomLines(width)...)

	if m.height > 0 && len(lines) > m.height {
		lines = lines[:m.height]
	}
	return strings.Join(lines, "\n")
}

// paneLines draws the pane rows: both panes side by side, or the focused one
// alone when the terminal is too narrow for two.
func (m filesModel) paneLines(width, rows int) []string {
	if m.narrow() {
		return m.renderPane(m.focus, width, rows)
	}

	paneWidth := m.paneWidth(width)
	left := m.renderPane(paneLocal, paneWidth, rows)
	right := m.renderPane(paneRemote, paneWidth, rows)

	gap := strings.Repeat(" ", fileGap)
	lines := make([]string, 0, rows)
	for i := range rows {
		// Both panes are padded to their full width before the gap is drawn: a row
		// a pane left short would slide the column beside it over by however much,
		// and a row of padding is short by every column it has.
		lines = append(lines, margin+
			padTo(left[i], paneWidth, false)+gap+
			padTo(right[i], paneWidth, false))
	}
	return lines
}

// renderPane draws one column, in exactly rows rows whatever it holds: the pane
// region of the frame is a fixed height, so a pane with less than that pads and
// one with more scrolls.
func (m filesModel) renderPane(which paneSide, width, rows int) []string {
	p := m.panes[which]
	visible := m.visible(which)

	lines := make([]string, 0, rows)
	if len(visible) == 0 {
		lines = append(lines, padTo(clip(m.paneEmpty(which), width), width, false))
	}
	for i := p.offset; i < len(visible) && len(lines) < rows; i++ {
		lines = append(lines, m.entryLine(visible[i], i == p.cursor, width))
	}
	// The padding is plain spaces rather than a styled blank: a background
	// colour under the last few entries would read as a selection.
	for len(lines) < rows {
		lines = append(lines, "")
	}
	return lines
}

// entryLine draws one entry, in exactly width columns so that the two panes line
// up row for row however long a name is.
//
// The selected row is painted as a single bar: a per-cell style nested inside it
// would reset the bar's background at every boundary, so the bar is applied to
// plain text instead.
func (m filesModel) entryLine(e entry, active bool, width int) string {
	// The size column is given up before the name is squeezed away: there is no
	// width that fits both, and the name is the half a listing is read for.
	showSize := width >= rowIndent+fileSizeWidth+2

	nameWidth := max(width-rowIndent, 1)
	if showSize {
		nameWidth = max(width-rowIndent-fileSizeWidth-1, 1)
	}

	name := e.name
	if e.dir {
		// A trailing slash marks a directory even where the colour does not
		// survive, which is every terminal whose output is not being looked at
		// directly.
		name += "/"
	}
	name = padTo(clip(name, nameWidth), nameWidth, false)

	size := ""
	if showSize {
		cell := ""
		if !e.dir {
			cell = HumanBytes(e.size)
		}
		size = " " + padTo(clip(cell, fileSizeWidth), fileSizeWidth, true)
	}

	marker := strings.Repeat(" ", rowIndent)
	if active {
		marker = rowMarker
		// The selected row is painted as a single bar over plain text: a per-cell
		// style nested inside it would reset the bar's background at every
		// boundary.
		return rowSelectedStyle.Width(width).Render(marker + name + size)
	}
	if e.dir {
		// A directory is bold and a file is not, which is the one distinction the
		// listing is read by.
		return marker + nameStyle.Render(name) + metaStyle.Render(size)
	}
	return marker + name + metaStyle.Render(size)
}

// paneEmpty is what a pane says when it has no row to draw. It is padded to the
// pane's width by the caller, so it is kept short.
func (m filesModel) paneEmpty(which paneSide) string {
	switch {
	case which == paneRemote && m.opening:
		return "connecting…"
	case which == paneRemote && !m.ready():
		return "no session"
	case which == paneRemote && m.loading:
		return "loading…"
	case m.query() != "" && which == m.focus:
		return "no match"
	default:
		return "empty"
	}
}

// pathLines labels each pane with the directory it is showing, and holds the
// filter line in place of them while one is up. Either way it is one line side
// by side and two stacked, which is what keeps the pane budget steady.
func (m filesModel) pathLines(width int) []string {
	if m.filtering {
		// The filter replaces the paths rather than being added below them: the
		// frame's height is a promise, and a line that comes and goes breaks it.
		line := margin + m.filter.View()
		if m.narrow() {
			return []string{line, ""}
		}
		return []string{line}
	}

	if m.narrow() {
		return []string{
			margin + m.panePath(paneLocal, width),
			margin + m.panePath(paneRemote, width),
		}
	}
	// Side by side, the two paths share the pane grid the rows below them are on:
	// a path padded to the whole content width would push the second one past the
	// right edge of the frame.
	paneWidth := m.paneWidth(width)
	return []string{
		margin + padTo(m.panePath(paneLocal, paneWidth), paneWidth, false) +
			strings.Repeat(" ", fileGap) +
			padTo(m.panePath(paneRemote, paneWidth), paneWidth, false),
	}
}

// panePath names a pane and says where it is, in the space the pane has on the
// path line.
func (m filesModel) panePath(which paneSide, width int) string {
	label, style := m.paneLabel(which)
	rendered := style.Render(label)

	dir := m.panes[which].cwd
	if dir == "" {
		dir = m.paneEmpty(which)
	}
	room := width - lipgloss.Width(label) - 1
	if room < 1 {
		return clip(label, width)
	}
	// The path is cut at the front: which tree it is in is the part the header
	// already says, and the last component is the part being asked about.
	return rendered + " " + clipTail(dir, room)
}

// paneLabel names a pane and marks which one has the keyboard. Both names are
// the same width, so the two paths start at the same column.
func (m filesModel) paneLabel(which paneSide) (string, lipgloss.Style) {
	name := "[LOCAL]"
	if which == paneRemote {
		name = "[REMOTE]"
	}
	if which == m.focus {
		return name, cursorStyle
	}
	return name, metaStyle
}

// header names the view and the host it is open on, with the address the session
// went to filling in the right-hand end of the line.
func (m filesModel) header(width int) string {
	left := titleStyle.Render("ohmyssh") + "  " +
		countStyle.Render(fmt.Sprintf("files · %s", m.host.Name))

	hint := m.host.DisplayUser() + "@" + hostName(m.host)
	if hint == "@" || lipgloss.Width(left)+lipgloss.Width(hint)+4 > width {
		// The title is cut rather than allowed to overrun a terminal too narrow
		// for it: a line wider than the frame wraps, and a wrapped line pushes
		// everything below it past the bottom of a frame whose height was counted
		// without it.
		return clip(left, width)
	}
	rendered := countStyle.Render(hint)
	gap := width - lipgloss.Width(left) - lipgloss.Width(rendered)
	return left + strings.Repeat(" ", max(gap, 1)) + rendered
}

// progressLines is where a running transfer is drawn, and the blank pair it
// leaves when there is none.
func (m filesModel) progressLines(width int) []string {
	if !m.transferring() {
		return []string{"", ""}
	}
	lines := liveFrame(m.progress, width)
	for len(lines) < fileProgressLines {
		lines = append(lines, "")
	}
	return lines[:fileProgressLines]
}

// statusLine is the last thing that happened: what is being moved where while a
// transfer runs, and how it ended once it has. It holds its line whether or not
// there is anything to say, so the panes above it never move, and it is clipped
// rather than wrapped for the same reason.
func (m filesModel) statusLine(width int) string {
	if m.transferring() {
		from, to := TransferEnds(m.request.Direction, m.request.Local, m.request.Remote)
		verb := m.request.Direction.Verb()
		return margin + metaStyle.Render(clip(fmt.Sprintf("%s %s → %s", verb, from, to), width))
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

// footer lists the keys the view answers to. Segments are added while they fit,
// which trims the bar from the right on a narrow terminal instead of wrapping it
// onto a second line.
func (m filesModel) footer(width int) string {
	// The bar is the same one the host list draws, so it is fitted the same way
	// and from the same kind of bindings.
	return fitSegments(m.footerSegments(), width)
}

// footerSegments are the bindings of whichever state the view is in, in the
// order they are given up when the line runs out of room.
//
// esc is the one that is last and so the first to go, for the reason it is
// first in the help: pressing it is what a user does when they do not know what
// else to press, and by then they have found it. The filter is not offered
// while it is up, and neither is anything but cancelling while a transfer is
// running: those bars are about the one thing the view is doing.
func (m filesModel) footerSegments() []binding {
	switch {
	case m.filtering:
		return []binding{
			{"type", "filter"},
			{"enter", "keep"},
			{"esc", "clear"},
		}
	case m.transferring():
		return []binding{
			{"esc", "cancel"},
			{"ctrl+c", "quit"},
		}
	}

	return []binding{
		{"tab", "pane"},
		{"↑↓", "move"},
		{"enter", "send"},
		{"c", "copy"},
		{"/", "find"},
		{"r", "reload"},
		{"?", "help"},
		{"esc", "back"},
	}
}
