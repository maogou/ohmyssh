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

// errFailed stands in for a dial error in the status line.
var errFailed = errors.New("dial refused")

func testHosts() []config.SSHHost {
	return []config.SSHHost{
		{Name: "web1", Hostname: "10.0.0.1", User: "deploy", Port: "22", Tags: []string{"prod"}, SourceFile: "/etc/ssh_config"},
		{Name: "db1", Hostname: "10.0.0.2", User: "postgres", Port: "5432"},
	}
}

// sized returns a model that has seen a window size, as it always has by the
// time the first frame is drawn.
func sized(opts BrowserOptions) browserModel {
	m := newBrowserModel(opts)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return updated.(browserModel)
}

// The filter box derives its placeholder layout from its own Width, and bubbles
// renders only the cursor rune when that width is zero. Without the window size
// reaching the input, the hint the user reads is a single stray letter.
func TestBrowserRendersFilterPlaceholder(t *testing.T) {
	// The first rune carries the cursor, so it is wrapped in its own escape
	// sequence; the rest of the placeholder follows contiguously.
	const tail = "ilter by name, host, user or tag"

	// The input needs a width from its first frame onward, and again once the
	// terminal reports its real size.
	frames := map[string]browserModel{
		"before a size is known": newBrowserModel(BrowserOptions{Hosts: testHosts()}),
		"after a resize":         sized(BrowserOptions{Hosts: testHosts()}),
	}
	for name, m := range frames {
		if view := m.View(); !strings.Contains(view, tail) {
			t.Errorf("filter placeholder not rendered %s:\n%s", name, view)
		}
	}
}

func TestBrowserFilterTracksTerminalWidth(t *testing.T) {
	m := newBrowserModel(BrowserOptions{Hosts: testHosts()})

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	narrow := updated.(browserModel)

	if got, want := narrow.filter.Width, filterBoxWidth(40); got != want {
		t.Errorf("filter width after resize = %d, want %d", got, want)
	}
	if got := narrow.View(); !strings.Contains(got, "ilter by name") {
		t.Errorf("placeholder missing at a narrow width:\n%s", got)
	}
}

func TestBrowserListsEveryHostBeforeFiltering(t *testing.T) {
	m := sized(BrowserOptions{Hosts: testHosts()})

	if len(m.visible) != 2 {
		t.Fatalf("visible = %d hosts, want 2", len(m.visible))
	}
	view := m.View()
	for _, name := range []string{"web1", "db1"} {
		if !strings.Contains(view, name) {
			t.Errorf("host %q missing from the view:\n%s", name, view)
		}
	}
}

func TestBrowserFilterNarrowsHosts(t *testing.T) {
	m := sized(BrowserOptions{Hosts: testHosts()})

	for _, r := range "db" {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(browserModel)
	}

	if len(m.visible) != 1 || m.visible[0].Name != "db1" {
		t.Fatalf("visible = %+v, want just db1", m.visible)
	}
}

func TestBrowserFilterMatchesTagsAndUser(t *testing.T) {
	web1, db1 := testHosts()[0], testHosts()[1]

	// Name, hostname, user and tags are all searchable.
	for _, query := range []string{"web1", "10.0.0.1", "deploy", "prod"} {
		if !hostMatches(web1, query) {
			t.Errorf("hostMatches(web1, %q) = false, want true", query)
		}
	}
	// A query that matches nothing about the host must not match it.
	for _, query := range []string{"prod", "deploy", "10.0.0.1"} {
		if hostMatches(db1, query) {
			t.Errorf("hostMatches(db1, %q) = true, want false", query)
		}
	}
}

// "q" is a literal character while a filter is being typed; only a bare "q"
// quits, so host names containing it stay typable.
func TestBrowserQuitKeyDependsOnFilter(t *testing.T) {
	bare := sized(BrowserOptions{Hosts: testHosts()})
	updated, _ := bare.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if !updated.(browserModel).quit {
		t.Error("q with an empty filter should quit")
	}

	filtered := sized(BrowserOptions{Hosts: testHosts()})
	for _, r := range "db" {
		next, _ := filtered.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		filtered = next.(browserModel)
	}
	next, _ := filtered.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	filtered = next.(browserModel)

	if filtered.quit {
		t.Error("q inside a filter should not quit")
	}
	if got := filtered.filter.Value(); got != "dbq" {
		t.Errorf("filter value = %q, want %q", got, "dbq")
	}
}

func TestBrowserStatusReportsSessionOutcome(t *testing.T) {
	m := sized(BrowserOptions{Hosts: testHosts()})
	host := testHosts()[0]

	updated, _ := m.Update(sessionFinishedMsg{host: host})
	view := updated.(browserModel).View()
	if !strings.Contains(view, "disconnected from web1") {
		t.Errorf("view missing the disconnect status:\n%s", view)
	}

	updated, _ = m.Update(sessionFinishedMsg{host: host, err: errFailed})
	failed := updated.(browserModel)
	if !failed.failed || !strings.Contains(failed.View(), "web1: dial refused") {
		t.Errorf("view missing the failure status:\n%s", failed.View())
	}
}

// The key hints are the only way to discover the bindings, so a session ending
// must not replace them: the status gets a line of its own above them.
func TestBrowserHintsSurviveASessionStatus(t *testing.T) {
	host := testHosts()[0]

	for name, msg := range map[string]sessionFinishedMsg{
		"clean exit": {host: host},
		"failed":     {host: host, err: errFailed},
	} {
		m := sized(BrowserOptions{Hosts: testHosts()})
		updated, _ := m.Update(msg)
		view := updated.(browserModel).View()

		for _, binding := range []string{
			"enter", "connect", "↑↓", "move", "type", "filter", "esc", "clear", "q", "quit",
			"U/D", "files", "?", "help",
		} {
			if !strings.Contains(view, binding) {
				t.Errorf("%s: hint %q missing from the view:\n%s", name, binding, view)
			}
		}
	}
}

// A full window of hosts used to overflow by the "more hosts" counter's line,
// which pushed the hints off the bottom of the screen.
func TestBrowserHintsStayOnTheLastLine(t *testing.T) {
	hosts := make([]config.SSHHost, 40)
	for i := range hosts {
		hosts[i] = config.SSHHost{Name: fmt.Sprintf("web%02d", i), Hostname: "10.0.0.1", User: "root", Port: "22"}
	}

	// Every binding the browser has, which is the bar at its longest and the only
	// case where anything has to be given up at 80 columns. All of them are keys
	// the user cannot guess from the screen; the filter hint is the one that can be
	// read off the filter box's own placeholder two lines above, so it is the one
	// that pays for the rest — hence last, since the bar gives up its segments from
	// the right.
	noService := BrowserOptions{Hosts: hosts}
	withService := BrowserOptions{
		Hosts:  hosts,
		Add:    func(context.Context, config.NewHost) ([]config.SSHHost, error) { return nil, nil },
		Remove: func(context.Context, config.SSHHost) ([]config.SSHHost, error) { return nil, nil },
	}

	// Without a host service the bar comes to 76 columns and fits entire. With one
	// it comes to 77 of the 78 an 80-column terminal leaves once the margin is
	// paid for, and something has to be given up — the filter hint, whose text the
	// filter box spells out in its own placeholder two lines above. That one
	// column of slack is the whole reason the help binding is on this bar at all at
	// the usual terminal width.
	cases := map[string]struct {
		opts       BrowserOptions
		wantFilter bool
	}{
		"without a host service": {opts: noService, wantFilter: true},
		"with one":               {opts: withService, wantFilter: false},
	}

	for name, tc := range cases {
		m := newBrowserModel(tc.opts)
		updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
		m = updated.(browserModel)

		lines := strings.Split(m.View(), "\n")
		if len(lines) != 12 {
			t.Fatalf("%s: view is %d lines, want the terminal's 12:\n%s", name, len(lines), m.View())
		}
		// The last line is the key hints, and a full window must not cost any of
		// them: a binding that is only ever listed when the list is short is one the
		// user cannot discover.
		last := lines[len(lines)-1]
		want := []string{"connect", "move", "quit", "files", "help"}
		if tc.opts.Add != nil {
			want = append(want, "add")
		}
		if tc.opts.Remove != nil {
			want = append(want, "delete")
		}
		if tc.wantFilter {
			want = append(want, "filter")
		}
		for _, hint := range want {
			if !strings.Contains(last, hint) {
				t.Errorf("%s: last line %q is missing the %q hint:\n%s", name, last, hint, m.View())
			}
		}
		if !tc.wantFilter && strings.Contains(last, "filter") {
			t.Errorf("%s: the filter hint kept its place; it is the one that pays for delete:\n%s",
				name, m.View())
		}
	}
}

// columnOf is the display column a substring starts at. Byte offsets are not
// columns: the selection marker is a multi-byte rune.
func columnOf(line, sub string) int {
	before, _, found := strings.Cut(line, sub)
	if !found {
		return -1
	}
	return lipgloss.Width(before)
}

// The table is a fixed grid: each column starts at the same display column in
// every row, including the one carrying the selection bar.
func TestBrowserRowsShareOneGrid(t *testing.T) {
	m := sized(BrowserOptions{Hosts: testHosts()})
	lines := strings.Split(m.View(), "\n")

	// Take the labels as the reference: the data has to land under them.
	var header string
	for _, line := range lines {
		if strings.Contains(line, "NAME") && strings.Contains(line, "HOST") {
			header = line
			break
		}
	}
	if header == "" {
		t.Fatalf("no column header in the view:\n%s", m.View())
	}

	hosts := testHosts()
	rows := 0
	for _, line := range lines {
		for _, h := range hosts {
			if !strings.Contains(line, h.Name) {
				continue
			}
			rows++
			for label, cell := range map[string]string{
				"NAME": h.Name,
				"USER": h.DisplayUser(),
				"HOST": h.Hostname,
			} {
				if got, want := columnOf(line, cell), columnOf(header, label); got != want {
					t.Errorf("%q starts at column %d, want %d under %s:\n%s",
						cell, got, want, label, line)
				}
			}
		}
	}
	if rows != len(hosts) {
		t.Errorf("matched %d rows, want %d:\n%s", rows, len(hosts), m.View())
	}
}

// The selection bar is as wide as the table, not as wide as the terminal. A
// highlight running to the right edge of a wide terminal is a slab of colour
// with a host name in its corner: it draws the eye to the empty space rather
// than to the row.
func TestBrowserSelectionBarIsTheWidthOfTheTable(t *testing.T) {
	m := newBrowserModel(BrowserOptions{Hosts: testHosts()})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 24})
	m = updated.(browserModel)

	barWidth := func(m browserModel) int {
		for line := range strings.SplitSeq(m.View(), "\n") {
			if strings.Contains(line, rowMarker) {
				return lipgloss.Width(line)
			}
		}
		t.Fatal("no selected row in the view")
		return 0
	}

	bar := barWidth(m)
	// The bar covers the marker and the columns, and stops there. It sits inside
	// the margin, which is not part of it.
	if want := lipgloss.Width(margin) + rowIndent + totalWidth(m.tableLayout(m.contentWidth())); bar != want {
		t.Errorf("the bar is %d columns, want the table's %d", bar, want)
	}
	if bar >= 200 {
		t.Errorf("the bar runs to the edge of a 200 column terminal (%d columns)", bar)
	}
	// The width is the columns', so it does not jump as the cursor goes from a row
	// with tags to one without.
	m.move(1)
	if got := barWidth(m); got != bar {
		t.Errorf("the bar changed width with the cursor: %d then %d", bar, got)
	}
}

// Space is given up from the columns that carry least: tags go before the port,
// the port before the user, and the alias and address never go at all. The
// widths below are the points where testHosts' measured columns stop fitting.
func TestBrowserTableDropsColumnsWhenNarrow(t *testing.T) {
	cases := []struct {
		width   int
		columns []string
	}{
		{44, []string{"NAME", "USER", "HOST", "PORT", "TAGS"}},
		{40, []string{"NAME", "USER", "HOST", "PORT"}},
		{32, []string{"NAME", "USER", "HOST"}},
		{22, []string{"NAME", "HOST"}},
	}
	for _, tc := range cases {
		m := newBrowserModel(BrowserOptions{Hosts: testHosts()})
		updated, _ := m.Update(tea.WindowSizeMsg{Width: tc.width, Height: 20})
		view := updated.(browserModel).View()

		for _, label := range []string{"NAME", "USER", "HOST", "PORT", "TAGS"} {
			want := slices.Contains(tc.columns, label)
			if got := strings.Contains(view, label); got != want {
				t.Errorf("width %d: column %s present = %v, want %v:\n%s",
					tc.width, label, got, want, view)
			}
		}
		if !slices.Contains(tc.columns, "NAME") || !slices.Contains(tc.columns, "HOST") {
			t.Errorf("width %d: case %v is missing a column that must never drop", tc.width, tc.columns)
		}
	}
}

// No line of the list is wider than the terminal, from the width the table's
// columns stop fitting at upward. A line that is would wrap, and a wrapped line
// pushes everything below it past the bottom of a frame whose height was counted
// without it — which is what the title does on its own below the width it takes
// up.
//
// Below 22 the table has no columns left to give up, and the list is unreadable
// whatever it draws; the file view's grid has no such floor and is asserted from
// 12 up in TestFileViewFrameIsExactlyTheTerminalWidth.
func TestBrowserFrameIsExactlyTheTerminalWidth(t *testing.T) {
	for width := 22; width <= 200; width++ {
		// Both states of the filter box. Nothing typed, it draws the placeholder
		// and is exactly the width it was given; with text in it, bubbles draws
		// the cursor cell on top of its own padding and the box is a column wider
		// than that — which is the case that used to run a column past the edge.
		for _, query := range []string{"", "db", "a query long enough to scroll the box"} {
			m := newBrowserModel(BrowserOptions{Hosts: testHosts()})
			m.filter.SetValue(query)
			updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
			view := updated.(browserModel).View()

			for i, line := range strings.Split(view, "\n") {
				if got := lipgloss.Width(line); got > width {
					t.Fatalf("width %d, filter %q: line %d is %d columns, %d past the edge:\n%s",
						width, query, i, got, got-width, view)
				}
			}
		}
	}
}

func TestClipKeepsCellsInsideTheirColumn(t *testing.T) {
	if got := clip("deploy@10.0.0.1:22", 8); lipgloss.Width(got) != 8 {
		t.Errorf("clip(%q, 8) = %q, width %d, want 8", "deploy@10.0.0.1:22", got, lipgloss.Width(got))
	}
	// Text that already fits is left exactly as it is.
	if got := clip("web1", 8); got != "web1" {
		t.Errorf("clip(web1, 8) = %q, want it untouched", got)
	}
	// Degenerate widths must not produce a lone ellipsis or panic.
	if got := clip("web1", 0); got != "" {
		t.Errorf("clip(web1, 0) = %q, want empty", got)
	}
}

func TestFilterWidthClampsToSomethingUsable(t *testing.T) {
	if got := filterWidth(100); got != 96 {
		t.Errorf("filterWidth(100) = %d, want 96", got)
	}
	// A terminal narrower than the minimum still needs a positive width.
	if got := filterWidth(1); got != 8 {
		t.Errorf("filterWidth(1) = %d, want 8", got)
	}
}

func TestBrowserEmptyStatesDifferByCause(t *testing.T) {
	noHosts := sized(BrowserOptions{})
	if view := noHosts.View(); !strings.Contains(view, "No hosts found") {
		t.Errorf("empty config view = %q", view)
	}

	m := sized(BrowserOptions{Hosts: testHosts()})
	for _, r := range "zzz" {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(browserModel)
	}
	if view := m.View(); !strings.Contains(view, "No hosts match the filter") {
		t.Errorf("empty filter view = %q", view)
	}
}
