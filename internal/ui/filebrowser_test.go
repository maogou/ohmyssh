package ui

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/maogou/ohmyssh/internal/config"
	"github.com/maogou/ohmyssh/internal/sshclient"
)

// fakeSession is a RemoteSession that answers out of a fixed tree, standing in
// for the host the right-hand pane is browsing.
//
// What it was asked for goes down channels rather than into fields: the view
// talks to it from goroutines of its own, and a shared field would be a race
// under -race.
type fakeSession struct {
	home string
	// dirs is the tree ReadDir answers from, keyed by the directory asked about.
	// A directory that is not in it is an error, as it would be on a host.
	dirs map[string][]RemoteEntry

	reads     chan string
	transfers chan TransferRequest

	// err is what a transfer ends with, and block is what it waits on instead
	// when set — which is how a transfer that is still running is held open.
	err   error
	block func(ctx context.Context) error

	closed    chan struct{}
	closeOnce sync.Once
}

func newFakeSession() *fakeSession {
	return &fakeSession{
		home:      "/home/deploy",
		dirs:      testTree(),
		reads:     make(chan string, 16),
		transfers: make(chan TransferRequest, 16),
		closed:    make(chan struct{}),
	}
}

// testTree is the host the tests browse: a home directory with two
// directories, a file, and a dotfile that must not be listed.
func testTree() map[string][]RemoteEntry {
	return map[string][]RemoteEntry{
		"/home/deploy": {
			{Name: "app", Dir: true},
			{Name: "dist", Dir: true},
			{Name: "deploy.sh", Size: 1200},
			{Name: ".env", Size: 40},
		},
		"/home/deploy/dist": {
			{Name: "main.js", Size: 2048},
		},
	}
}

func (f *fakeSession) Home() string { return f.home }

func (f *fakeSession) ReadDir(dir string) ([]RemoteEntry, error) {
	f.reads <- dir
	entries, ok := f.dirs[dir]
	if !ok {
		return nil, errors.New("no such directory")
	}
	return entries, nil
}

func (f *fakeSession) Transfer(ctx context.Context, req TransferRequest, _ sshclient.ProgressFunc) error {
	f.transfers <- req
	if f.block != nil {
		return f.block(ctx)
	}
	return f.err
}

func (f *fakeSession) Close() error {
	f.closeOnce.Do(func() { close(f.closed) })
	return nil
}

// lastTransfer is the request the view sent, or a failed test when it sent none:
// a transfer that never started is the failure worth hearing about.
func (f *fakeSession) lastTransfer(t *testing.T) TransferRequest {
	t.Helper()

	select {
	case req := <-f.transfers:
		return req
	default:
		t.Fatal("no transfer was started")
		return TransferRequest{}
	}
}

// localTree is a directory shaped like the host's, so that a test about a
// transfer does not have to remember which end it is looking at.
func localTree(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, size := range map[string]int{"deploy.sh": 1200, "notes.txt": 12} {
		if err := os.WriteFile(filepath.Join(dir, name), bytes.Repeat([]byte("x"), size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ".hidden"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// press sends a key and hands back the command it returned, which for the keys
// that start something is the thing under test.
func press(t *testing.T, m browserModel, msg tea.KeyMsg) (browserModel, tea.Cmd) {
	t.Helper()

	updated, cmd := m.Update(msg)
	return updated.(browserModel), cmd
}

// pressKey sends a key and ignores whatever command came back with it. It is for
// the keys whose command is the cursor blink rather than the thing under test.
func pressKey(t *testing.T, m browserModel, msg tea.KeyMsg) browserModel {
	t.Helper()

	m, _ = press(t, m, msg)
	return m
}

func rune2(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func key(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

// openFileView presses U and feeds the dial's answer back in, which is the state
// every test of the panes starts from: the view up, with its session attached
// and both directories listed.
func openFileView(t *testing.T, session RemoteSession) browserModel {
	t.Helper()

	return openFileViewWith(t, session, 'U')
}

// openFileViewWith opens the view with the given key, which is how a test says
// which pane it expects to find the keyboard in.
func openFileViewWith(t *testing.T, session RemoteSession, pressed rune) browserModel {
	t.Helper()

	m := sized(BrowserOptions{
		Hosts: testHosts(),
		Remote: func(context.Context, config.SSHHost) (RemoteSession, error) {
			return session, nil
		},
	})
	m, cmd := press(t, m, rune2(pressed))
	if m.mode != modeFiles {
		t.Fatalf("%c put the browser in mode %v, want the file view", pressed, m.mode)
	}
	return settleBrowser(t, m, cmd)
}

// settleBrowser runs the commands the model returns and feeds their messages
// back in until it stops asking for more, which is what the bubbletea runtime
// does with them. The round count is bounded so that a model which keeps
// re-arming fails the test instead of spending the run waiting for a report that
// never comes.
func settleBrowser(t *testing.T, m browserModel, cmd tea.Cmd) browserModel {
	t.Helper()

	for round := 0; cmd != nil && round < 100; round++ {
		updated, next := m.Update(cmd())
		m, cmd = updated.(browserModel), next
	}
	if cmd != nil {
		t.Fatal("the model never settled: it kept returning commands")
	}
	return m
}

// selectEntry puts the cursor on the named entry of the focused pane, which is
// how a test says which file it means without depending on the order a listing
// came back in.
func selectEntry(t *testing.T, m filesModel, name string) filesModel {
	t.Helper()

	visible := m.visible(m.focus)
	for i, e := range visible {
		if e.name == name {
			m.panes[m.focus].cursor = i
			return m
		}
	}
	t.Fatalf("%s is not in the focused pane: %+v", name, visible)
	return m
}

// withLocalDir points the left pane at dir, which is what the view does on
// opening it on a directory the test controls rather than on whichever one the
// test binary happens to be running in.
func (m filesModel) withLocalDir(dir string) filesModel {
	m.panes[paneLocal].cwd = dir
	m.reloadLocal()
	return m
}

// The two panes open where the user expects them: the left in the directory
// ohmyssh was started in, the right in the directory the login landed in.
func TestFileViewOpensAtTheLocalDirectoryAndTheRemoteHome(t *testing.T) {
	session := newFakeSession()
	m := openFileView(t, session).files

	if got := m.panes[paneRemote].cwd; got != session.Home() {
		t.Errorf("the remote pane opened at %q, want the home %q", got, session.Home())
	}
	if got := m.panes[paneLocal].cwd; got == "" {
		t.Error("the local pane opened with no directory")
	}
	if m.focus != paneLocal {
		t.Errorf("focus = %v, want the local pane", m.focus)
	}
	if got := <-session.reads; got != session.Home() {
		t.Errorf("the session was asked for %q, want the home directory", got)
	}
}

// Each of the two keys opens the view on the end it names: U is about the files
// being sent, D about the ones being fetched, and the pane the keyboard is in is
// the pane a transfer is read from.
func TestFileViewOpensInThePaneItsKeyMeans(t *testing.T) {
	for _, tc := range []struct {
		pressed rune
		want    paneSide
	}{
		{'U', paneLocal},
		{'D', paneRemote},
	} {
		m := openFileViewWith(t, newFakeSession(), tc.pressed).files
		if m.focus != tc.want {
			t.Errorf("%c focused pane %v, want %v", tc.pressed, m.focus, tc.want)
		}
	}
}

// Dotfiles are left out of both panes. Two columns that disagreed about what a
// file is would read as a bug, and they are always read side by side.
func TestFileViewHidesDotfilesInBothPanes(t *testing.T) {
	dir := localTree(t)
	m := openFileView(t, newFakeSession()).files.withLocalDir(dir)

	for _, which := range []paneSide{paneLocal, paneRemote} {
		for _, e := range m.panes[which].entries {
			if strings.HasPrefix(e.name, ".") {
				t.Errorf("pane %v lists the dotfile %q", which, e.name)
			}
		}
	}
	if view := m.View(); strings.Contains(view, ".env") || strings.Contains(view, ".hidden") {
		t.Errorf("a dotfile reached the frame:\n%s", view)
	}
}

// Directories come first and then names, which is the order a listing is read
// in: the ways further in before the files at this level.
func TestFileViewSortsDirectoriesFirst(t *testing.T) {
	m := openFileView(t, newFakeSession()).files

	var names []string
	for _, e := range m.panes[paneRemote].entries {
		names = append(names, e.name)
	}
	want := []string{"app", "dist", "deploy.sh"}
	if len(names) != len(want) {
		t.Fatalf("remote listing = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("remote listing = %v, want %v", names, want)
		}
	}
}

// enter on a file in the left pane sends it to the directory the right pane is
// showing, under its own name.
func TestFileViewEnterOnALocalFileUploads(t *testing.T) {
	dir := localTree(t)
	session := newFakeSession()
	m := openFileView(t, session).files.withLocalDir(dir)
	m = selectEntry(t, m, "deploy.sh")
	// Settled before the request is read: the transfer runs on a goroutine of its
	// own, and reading the channel before it has been given its request would be
	// a race the test would lose about half the time.
	m = settleFiles(t, m, m.openEntry())

	want := TransferRequest{
		Local:     filepath.Join(dir, "deploy.sh"),
		Remote:    "/home/deploy/deploy.sh",
		Direction: sshclient.Upload,
	}
	if got := session.lastTransfer(t); got != want {
		t.Errorf("request = %+v, want %+v", got, want)
	}
}

// And the other way round: an entry in the right pane comes back to the
// directory the left pane is showing.
func TestFileViewEnterOnARemoteFileDownloads(t *testing.T) {
	dir := localTree(t)
	session := newFakeSession()
	m := openFileView(t, session).files.withLocalDir(dir)
	m.focus = paneRemote
	m = selectEntry(t, m, "deploy.sh")
	m = settleFiles(t, m, m.openEntry())

	want := TransferRequest{
		Local:     filepath.Join(dir, "deploy.sh"),
		Remote:    "/home/deploy/deploy.sh",
		Direction: sshclient.Download,
	}
	if got := session.lastTransfer(t); got != want {
		t.Errorf("request = %+v, want %+v", got, want)
	}
}

// enter on a directory walks into it, on either end, rather than sending it.
func TestFileViewEnterOnADirectoryDescends(t *testing.T) {
	dir := localTree(t)
	session := newFakeSession()
	m := openFileView(t, session).files.withLocalDir(dir)

	m = selectEntry(t, m, "dist")
	m = settleFiles(t, m, m.openEntry())
	if got := m.panes[paneLocal].cwd; got != filepath.Join(dir, "dist") {
		t.Errorf("the local pane is at %q, want the dist directory", got)
	}

	m.focus = paneRemote
	m = selectEntry(t, m, "dist")
	m = settleFiles(t, m, m.openEntry())
	if got := m.panes[paneRemote].cwd; got != "/home/deploy/dist" {
		t.Errorf("the remote pane is at %q, want /home/deploy/dist", got)
	}

	if len(session.transfers) != 0 {
		t.Errorf("walking into a directory started a transfer: %+v", <-session.transfers)
	}
}

// c is the half of the pair enter cannot do: it sends a directory whole.
func TestFileViewCopySendsADirectoryWhole(t *testing.T) {
	dir := localTree(t)
	session := newFakeSession()
	m := openFileView(t, session).files.withLocalDir(dir)
	m = selectEntry(t, m, "dist")
	m = settleFiles(t, m, m.transferEntry())

	// The name is joined onto the other pane's directory, so the tree lands as a
	// directory of its own rather than spilling into the middle of the home
	// directory.
	want := TransferRequest{
		Local:     filepath.Join(dir, "dist"),
		Remote:    "/home/deploy/dist",
		Direction: sshclient.Upload,
	}
	if got := session.lastTransfer(t); got != want {
		t.Errorf("request = %+v, want %+v", got, want)
	}
}

// tab gives the keyboard to the other pane, and back again.
func TestFileViewTabSwitchesPanes(t *testing.T) {
	m := openFileView(t, newFakeSession()).files

	for i, want := range []paneSide{paneRemote, paneLocal, paneRemote} {
		var cmd tea.Cmd
		m, cmd = mustCmd(t, m, key(tea.KeyTab))
		m = settleFiles(t, m, cmd)
		if m.focus != want {
			t.Fatalf("after tab %d: focus = %v, want %v", i+1, m.focus, want)
		}
	}
}

// Walking up leaves the cursor on the directory it came out of, since that is
// the row the user is looking for after stepping back.
func TestFileViewLeftGoesUpOntoTheDirectoryItLeft(t *testing.T) {
	dir := localTree(t)
	session := newFakeSession()
	m := openFileView(t, session).files.withLocalDir(dir)

	// Down into dist, then back up.
	m = selectEntry(t, m, "dist")
	m = settleFiles(t, m, m.openEntry())
	m = settleFiles(t, m, m.goUp())

	if got := m.panes[paneLocal].cwd; got != dir {
		t.Fatalf("the local pane is at %q, want %q", got, dir)
	}
	if got := m.panes[paneLocal].cursor; m.visible(paneLocal)[got].name != "dist" {
		t.Errorf("the cursor landed on %q, want the dist it came out of", m.visible(paneLocal)[got].name)
	}
}

// The filter narrows the pane that has the keyboard and nobody else: the query
// is about one column, and a second one quietly missing rows would be hiding
// files from a question that was never asked about it.
func TestFileViewFilterNarrowsOnlyTheFocusedPane(t *testing.T) {
	session := newFakeSession()
	m := openFileView(t, session).files.withLocalDir(localTree(t))

	m, _ = m.handleKey(rune2('/'))
	m, _ = m.handleKey(rune2('d'))
	m, _ = m.handleKey(rune2('i'))
	m, _ = m.handleKey(rune2('s'))

	if got := len(m.visible(paneLocal)); got != 1 {
		t.Errorf("the filtered pane shows %d entries, want just dist", got)
	}
	if got := len(m.visible(paneRemote)); got != 3 {
		t.Errorf("the unfiltered pane shows %d entries, want all 3", got)
	}

	// The filter belongs to whichever pane has the keyboard, so switching takes
	// it along: the query now narrows the remote listing and the local one is
	// whole again.
	m, _ = m.handleKey(key(tea.KeyTab))
	if got := len(m.visible(paneRemote)); got != 1 {
		t.Errorf("after tab the remote pane shows %d entries, want just dist", got)
	}
	if got := len(m.visible(paneLocal)); got != 3 {
		t.Errorf("after tab the local pane shows %d entries, want all 3", got)
	}
}

// A key that means something in the host list must not mean it here, or the
// program would end with a transfer half moved.
func TestFileViewKeepsTheListBindingsOut(t *testing.T) {
	m := openFileView(t, newFakeSession())

	for _, r := range "qQD" {
		m = pressKey(t, m, rune2(r))
		if m.quit {
			t.Fatalf("%q quit the browser from the file view", r)
		}
		if m.mode != modeFiles {
			t.Fatalf("%q left the file view", r)
		}
	}
	if got := m.filter.Value(); got != "" {
		t.Errorf("filter = %q, want the keystrokes kept out of the host filter", got)
	}

	m = pressKey(t, m, key(tea.KeyCtrlC))
	if !m.quit {
		t.Error("ctrl+c did not quit from the file view")
	}
}

// esc is the way out of the view, and it takes the session with it: nothing is
// left holding a connection the user is done with.
func TestFileViewEscReturnsToTheListAndClosesTheSession(t *testing.T) {
	session := newFakeSession()
	m := openFileView(t, session)

	m = pressKey(t, m, key(tea.KeyEsc))
	if m.mode != modeList {
		t.Fatalf("mode = %v after esc, want the host list", m.mode)
	}
	if !m.filter.Focused() {
		t.Error("the host filter did not take the keyboard back")
	}
	select {
	case <-session.closed:
	default:
		t.Error("esc left the session open")
	}
}

// A dial that comes back after the user has already walked out has nobody left
// to browse with it, and would otherwise sit on a connection no one will close.
func TestFileViewClosesASessionThatArrivesTooLate(t *testing.T) {
	m := openFileView(t, newFakeSession())
	m = pressKey(t, m, key(tea.KeyEsc))

	late := newFakeSession()
	updated, _ := m.Update(remoteOpenedMsg{session: late})
	_ = updated

	select {
	case <-late.closed:
	default:
		t.Error("a session that arrived after the view closed was left open")
	}
}

// esc cancels a running transfer, and the bars stay up until it has actually
// stopped: they must not vanish while bytes are still moving.
func TestFileViewEscCancelsARunningTransfer(t *testing.T) {
	session := newFakeSession()
	// A transfer that only ends when it is cancelled, so the test controls when
	// it stops rather than racing it.
	session.block = func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}

	m := openFileView(t, session).files.withLocalDir(localTree(t))
	m = selectEntry(t, m, "deploy.sh")
	started := m.transferEntry()
	if !m.transferring() {
		t.Fatal("the transfer did not start")
	}

	m, _ = m.handleKey(key(tea.KeyEsc))
	if !m.transferring() {
		t.Error("the progress block went away while the transfer was still stopping")
	}
	if m.status != "cancelling…" {
		t.Errorf("status = %q, want it to say the transfer is stopping", m.status)
	}
	if m.failed {
		t.Error("cancelling was marked as a failure")
	}

	m = settleFiles(t, m, started)
	if m.transferring() {
		t.Error("the transfer never stopped")
	}
	if want := "cancelled put " + filepath.Join(m.panes[paneLocal].cwd, "deploy.sh") + " → /home/deploy/deploy.sh"; m.status != want {
		t.Errorf("status = %q, want %q", m.status, want)
	}
	if m.failed {
		t.Error("a cancelled transfer is not a failure, and should not be marked as one")
	}
}

// A failure is reported where every other outcome is, with the tick replaced by
// the cross.
func TestFileViewTransferFailureLandsInTheStatus(t *testing.T) {
	session := newFakeSession()
	session.err = errors.New("permission denied")

	m := openFileView(t, session).files.withLocalDir(localTree(t))
	m = selectEntry(t, m, "deploy.sh")
	m = settleFiles(t, m, m.transferEntry())

	if !m.failed {
		t.Fatal("a failed transfer was not marked as a failure")
	}
	// The status itself, not the frame: the line is clipped to the terminal, and a
	// long path is enough to push the reason off the end of it.
	for _, want := range []string{"put", "permission denied"} {
		if !strings.Contains(m.status, want) {
			t.Errorf("status = %q, want it to report %q", m.status, want)
		}
	}
	if view := m.View(); !strings.Contains(view, statusMarkerFailed) {
		t.Errorf("the frame does not mark the failure:\n%s", view)
	}
}

// A listing that came back for a directory the user has already walked past is
// dropped: drawing it would put one directory's files under another's path.
func TestFileViewDropsAListingItNoLongerWaitsOn(t *testing.T) {
	session := newFakeSession()
	m := openFileView(t, session).files

	before := m.panes[paneRemote].entries
	m, _ = m.Update(remoteEntriesMsg{
		path:    "/somewhere/else",
		entries: []RemoteEntry{{Name: "stale"}},
	})

	if got := m.panes[paneRemote].cwd; got != session.Home() {
		t.Errorf("the pane moved to %q on a stale listing", got)
	}
	if len(m.panes[paneRemote].entries) != len(before) {
		t.Error("a stale listing replaced the pane's own")
	}
}

// A directory that will not open is reported without losing the listing the
// user still wants.
func TestFileViewReportsAListingThatFailed(t *testing.T) {
	session := newFakeSession()
	m := openFileView(t, session).files

	before := m.panes[paneRemote].entries
	m = settleFiles(t, m, m.descend(entry{name: "nope", dir: true, path: "/home/deploy/nope"}))

	if !m.failed || !strings.Contains(m.status, "/home/deploy/nope") {
		t.Errorf("status = %q (failed %v), want the directory that would not open", m.status, m.failed)
	}
	if len(m.panes[paneRemote].entries) != len(before) {
		t.Error("the pane's listing was lost with the failed read")
	}
}

// A dial that fails is reported in the status, and the left pane is still worth
// showing: it is the filesystem this process is already on.
func TestFileViewReportsASessionThatCouldNotBeOpened(t *testing.T) {
	m := sized(BrowserOptions{
		Hosts: testHosts(),
		Remote: func(context.Context, config.SSHHost) (RemoteSession, error) {
			return nil, errors.New("no saved password for web1")
		},
	})
	m, cmd := press(t, m, rune2('U'))
	m = settleBrowser(t, m, cmd)

	if m.mode != modeFiles {
		t.Fatalf("mode = %v, want the view with the failure in its status", m.mode)
	}
	if !m.files.failed || !strings.Contains(m.files.status, "no saved password") {
		t.Errorf("status = %q, want the dial's own error", m.files.status)
	}
	if view := m.View(); !strings.Contains(view, "no session") {
		t.Errorf("the remote pane does not say it has no session:\n%s", view)
	}
}

// A browser built without a file service says so rather than showing a view that
// could never fill its right column.
func TestBrowserWithoutAFileServiceSaysSo(t *testing.T) {
	m := sized(BrowserOptions{Hosts: testHosts()})
	m = pressKey(t, m, rune2('U'))

	if m.mode != modeList {
		t.Errorf("mode = %v, want the list: there is nothing to browse with", m.mode)
	}
	if !m.failed || !strings.Contains(m.status, "unavailable") {
		t.Errorf("status = %q (failed %v), want it to say transfers are unavailable", m.status, m.failed)
	}
}

// The status the view earned is what the list shows once the user is back in it:
// the last thing that happened happened in there.
func TestBrowserCarriesTheFileViewStatusBack(t *testing.T) {
	session := newFakeSession()
	session.err = errors.New("permission denied")

	m := openFileView(t, session)
	m.files.status, m.files.failed = "put dist → /opt/app  3 files  4.2 MB", false

	m = pressKey(t, m, key(tea.KeyEsc))
	if !strings.Contains(m.View(), "put dist → /opt/app") {
		t.Errorf("the list does not report what the view did:\n%s", m.View())
	}
}

// A cancelled transfer's last reports can arrive after the next one has begun.
// The generation counter is what keeps one from being drawn over the other.
func TestFileViewDropsMessagesFromAnEarlierTransfer(t *testing.T) {
	m := openFileView(t, newFakeSession()).files
	m.generation = 2
	m.events = make(chan sshclient.Progress)
	m.progress = sshclient.Progress{File: "current", FilesDone: 1}
	m.status = "in flight"

	updated, _ := m.Update(transferProgressMsg{
		generation: 1,
		progress:   sshclient.Progress{File: "stale", FilesDone: 9},
	})
	if got := updated.progress.File; got != "current" {
		t.Errorf("progress = %q, want the running transfer's own", got)
	}

	updated, _ = m.Update(transferDoneMsg{generation: 1, err: errFailed})
	if updated.status != "in flight" {
		t.Errorf("status = %q, want the stale outcome dropped", updated.status)
	}
	if !updated.transferring() {
		t.Error("the stale outcome took the running transfer down")
	}
}

// The frame is exactly as tall as the terminal, at every height: bubbletea's alt
// screen keeps the last lines of a frame that is too tall, so an overrun would
// eat the header and the paths while the model went on believing it had drawn
// all of it.
// sizedFor opens the file view on a browser that has seen a window size of the
// test's choosing, for the layout tests that are about what a terminal of a
// particular size does.
func sizedFor(t *testing.T, width, height int, session RemoteSession) browserModel {
	t.Helper()

	m := newBrowserModel(BrowserOptions{
		Hosts: testHosts(),
		Remote: func(context.Context, config.SSHHost) (RemoteSession, error) {
			return session, nil
		},
	})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m = updated.(browserModel)

	m, cmd := press(t, m, rune2('U'))
	if m.mode != modeFiles {
		t.Fatal("U did not open the file view")
	}
	return settleBrowser(t, m, cmd)
}

// The view is laid out for the terminal it is opened on, not for a default it
// holds until the user happens to resize the window.
//
// Every test below resizes the view before measuring it, which is how the size it
// was opened at went unnoticed: the model was built from the filter box's width,
// which is the terminal's less the box's own chrome, and with no height at all.
// The panes were therefore a few columns narrow and a few rows short, and the grid
// under the filter box did not line up with the one the host list is drawn on,
// until a resize happened to correct it.
func TestFileViewOpensAtTheTerminalSize(t *testing.T) {
	for _, size := range []struct{ width, height int }{
		{defaultBrowserWidth, 30},
		{fileNarrowWidth - 1, 24},
		{200, 12},
	} {
		m := sizedFor(t, size.width, size.height, newFakeSession())

		if got := m.files.width; got != size.width {
			t.Errorf("%dx%d: view width = %d, want the terminal's %d",
				size.width, size.height, got, size.width)
		}
		if got := m.files.height; got != size.height {
			t.Errorf("%dx%d: view height = %d, want the terminal's %d",
				size.width, size.height, got, size.height)
		}
		// The two views share one grid: a line in either starts and ends where the
		// same line in the other does.
		if got, want := m.files.contentWidth(), m.contentWidth(); got != want {
			t.Errorf("%dx%d: the file view is drawn to %d columns, the host list to %d",
				size.width, size.height, got, want)
		}
		// And it is drawn to that size from the first frame, before any resize.
		lines := strings.Split(m.files.View(), "\n")
		if len(lines) != size.height {
			t.Errorf("%dx%d: the frame is %d lines, want %d",
				size.width, size.height, len(lines), size.height)
		}
		for i, line := range lines {
			if got := lipgloss.Width(line); got > size.width {
				t.Errorf("%dx%d: line %d is %d columns, %d past the edge",
					size.width, size.height, i, got, got-size.width)
			}
		}
	}
}

func TestFileViewFrameIsExactlyTheTerminalHeight(t *testing.T) {
	for height := 6; height <= 40; height++ {
		m := openFileView(t, newFakeSession()).files
		m.resize(100, height)

		lines := strings.Split(m.View(), "\n")
		if len(lines) != height {
			t.Fatalf("height %d: the frame is %d lines:\n%s", height, len(lines), m.View())
		}
		// Once there is room for the whole chrome the key hints get the last
		// line, and they must be there rather than dropped for the panes.
		if height >= 12 {
			if last := lines[len(lines)-1]; !strings.Contains(last, "send") {
				t.Fatalf("height %d: last line %q is not the key hints:\n%s", height, last, m.View())
			}
		}
	}
}

// The frame is exactly as wide as the terminal, at every width: a line wider
// than the terminal wraps, and one wrapped line pushes everything below it down
// and off the bottom of a frame whose height was counted without it.
//
// The path line is the one that gets this wrong — it is the line whose content
// has no natural end, since a directory can be named anything — so it is driven
// here with a path too long for the pane and a listing too wide for the row.
func TestFileViewFrameIsExactlyTheTerminalWidth(t *testing.T) {
	dir := localTree(t)
	for _, name := range []string{
		strings.Repeat("a", 120) + ".txt",
		strings.Repeat("b", 60),
	} {
		target := filepath.Join(dir, name)
		if strings.HasSuffix(name, ".txt") {
			if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Mkdir(target, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	for width := 12; width <= 200; width++ {
		session := newFakeSession()
		session.dirs[session.home] = []RemoteEntry{
			{Name: strings.Repeat("c", 90), Dir: true},
			{Name: "a.txt", Size: 1},
		}

		m := openFileView(t, session).files.withLocalDir(dir)
		m.resize(width, 24)
		// The cursor on the longest thing there is, so the selected row — which is
		// painted as a bar to the full pane width — is the one being measured.
		m = selectEntry(t, m, strings.Repeat("a", 120)+".txt")
		m.status = "get " + filepath.Join(dir, strings.Repeat("a", 120)+".txt") + " → /root/" + strings.Repeat("z", 60)

		view := m.View()
		for i, line := range strings.Split(view, "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Fatalf("width %d: line %d is %d columns, %d past the edge:\n%s",
					width, i, got, got-width, view)
			}
		}
	}
}

// The panes keep their rows whatever is in them: a listing that overflows
// scrolls and a short one pads, so the progress block and the hints below do not
// move.
func TestFileViewPanesKeepTheirRows(t *testing.T) {
	hosts := make([]RemoteEntry, 200)
	for i := range hosts {
		hosts[i] = RemoteEntry{Name: "file" + string(rune('a'+i%26)) + string(rune('0'+i/26)), Size: int64(i)}
	}
	session := newFakeSession()
	session.dirs[session.home] = hosts

	m := openFileView(t, session).files
	m.resize(100, 30)

	rows := m.paneRows()
	if rows < 2 {
		t.Fatalf("pane rows = %d, want room for a listing", rows)
	}
	for _, which := range []paneSide{paneLocal, paneRemote} {
		if got := len(m.renderPane(which, m.paneWidth(m.contentWidth()), rows)); got != rows {
			t.Errorf("pane %v drew %d rows, want %d", which, got, rows)
		}
	}
	if got := strings.Count(strings.Join(m.paneLines(m.contentWidth(), rows), "\n"), "\n") + 1; got != rows {
		t.Errorf("the pane region is %d lines, want %d", got, rows)
	}
}

// Below the width two columns need, one pane is drawn at a time rather than two
// too narrow to read a file name in.
func TestFileViewIsOnePaneWhenNarrow(t *testing.T) {
	m := openFileView(t, newFakeSession()).files
	m.resize(fileNarrowWidth-1, 24)

	if !m.narrow() {
		t.Fatalf("a %d-column terminal is not narrow", fileNarrowWidth-1)
	}
	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) != 24 {
		t.Fatalf("the frame is %d lines, want 24:\n%s", len(lines), view)
	}
	// Both paths are still named — the user has to know which pane they are in
	// and which one tab would bring forward — but the rows belong to one pane.
	for _, label := range []string{"[LOCAL]", "[REMOTE]"} {
		if !strings.Contains(view, label) {
			t.Errorf("the narrow frame does not label %s:\n%s", label, view)
		}
	}
	if m.paneWidth(m.contentWidth()) != m.contentWidth() {
		t.Errorf("the narrow pane is %d wide, want the whole %d", m.paneWidth(m.contentWidth()), m.contentWidth())
	}
}

// The two panes are a grid: an entry starts at the same column in the row a file
// is on as in the row a directory is, and the right pane starts where it says it
// does however long the names on the left are.
func TestFileViewRowsShareOneGrid(t *testing.T) {
	dir := localTree(t)
	if err := os.WriteFile(filepath.Join(dir, strings.Repeat("long", 20)+".txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := openFileView(t, newFakeSession()).files.withLocalDir(dir)
	m.resize(120, 30)

	width := m.contentWidth()
	paneWidth := m.paneWidth(width)
	rows := m.paneLines(width, m.paneRows())
	if len(rows) == 0 {
		t.Fatal("no pane rows")
	}

	// Read through the styles: the model reports the row it drew, so what is
	// compared is the grid rather than the escape codes.
	for _, row := range rows {
		if got := len(row); got == 0 {
			continue
		}
		// Every row is margin + pane + gap + pane, and a long name is cut rather
		// than allowed to push the right pane along.
		if got := lipgloss.Width(row); got != len(margin)+2*paneWidth+fileGap {
			t.Errorf("row is %d columns, want %d:\n%q", got, len(margin)+2*paneWidth+fileGap, row)
		}
	}
}

// settleFiles is settleBrowser's counterpart for a model driven without the host
// list in front of it.
func settleFiles(t *testing.T, m filesModel, cmd tea.Cmd) filesModel {
	t.Helper()

	for round := 0; cmd != nil && round < 100; round++ {
		var next tea.Cmd
		m, next = m.Update(cmd())
		cmd = next
	}
	if cmd != nil {
		t.Fatal("the file view never settled: it kept returning commands")
	}
	return m
}

// mustCmd presses a key and hands back the model alongside the command, which is
// what a key that changes the model as well as starting something needs: dropping
// the model would drop the change with it.
func mustCmd(t *testing.T, m filesModel, msg tea.KeyMsg) (filesModel, tea.Cmd) {
	t.Helper()

	return m.handleKey(msg)
}
