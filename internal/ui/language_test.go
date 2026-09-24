package ui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/maogou/ohmyssh/internal/config"
	"github.com/maogou/ohmyssh/internal/i18n"
	"github.com/maogou/ohmyssh/internal/sshclient"
)

// withLanguage installs a language for one test and puts English back when it
// ends.
//
// Every other test in this package reads the English it was written against, on
// the language the package starts with. No test here calls t.Parallel, so a test
// that changes the language cannot be running beside one that is reading it —
// which is what makes process-wide state the right shape for this rather than
// threading a language through forty renderers. A new test that reads the
// catalogue and does call t.Parallel would race, and nothing here would catch it.
func withLanguage(t *testing.T, lang i18n.Language) {
	t.Helper()

	i18n.Setup(lang)
	t.Cleanup(func() { i18n.Setup(i18n.English) })
}

// frameSizes are the terminals the layout is held to: the usual one, a wide one,
// and two cramped enough to make the layout give something up. Chinese is two
// columns a character, so the widths a line was measured at in English are the
// budget it has to meet here rather than one it can assume.
var frameSizes = []struct{ width, height int }{
	{80, 24}, {100, 30}, {200, 60}, {40, 12}, {24, 8},
}

// The point of the catalogue: a Chinese terminal gets a Chinese program. What is
// asserted is that the words are there and the English they replaced is not — a
// screen with both on it reads worse than one with neither.
func TestTheBrowserSpeaksChinese(t *testing.T) {
	withLanguage(t, i18n.Chinese)

	m := helpBrowser()

	view := m.View()
	for _, want := range []string{
		"名称", "用户", "主机", "端口", "标签",
		"2/2 台主机",
		"名称、主机、用户或标签过滤",
		"连接", "移动", "清空", "退出", "添加", "删除", "文件", "帮助", "过滤",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the list does not say %q in Chinese:\n%s", want, view)
		}
	}
	// The column headings are the ones that would go on being English while
	// everything around them changed, if they were read once when the package was
	// loaded rather than per frame.
	for _, unwanted := range []string{"NAME", "USER", "HOST", "PORT", "TAGS"} {
		if strings.Contains(view, unwanted) {
			t.Errorf("the list still heads a column %q:\n%s", unwanted, view)
		}
	}

	// The help, which is a frame of its own and a table of longer sentences.
	help := pressKey(t, m, rune2('?')).View()
	for _, want := range []string{
		"按键 · 主机列表",
		"连接到光标所在的主机",
		"按名称、主机、用户或标签过滤",
		"查看按键功能说明",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("the list's help does not say %q in Chinese:\n%s", want, help)
		}
	}
	for _, unwanted := range []string{"keys ·", "host list", "file view", "show key function help"} {
		if strings.Contains(help, unwanted) {
			t.Errorf("the list's help still says %q:\n%s", unwanted, help)
		}
	}

	// The form, whose labels are measured to lay the fields out: a label column
	// left at its English width would put the values out of line.
	form := pressKey(t, m, rune2('A')).View()
	for _, want := range []string{"新增主机", "别名", "标签", "必填；你输入的那个名字", "保存", "字段", "取消"} {
		if !strings.Contains(form, want) {
			t.Errorf("the form does not say %q in Chinese:\n%s", want, form)
		}
	}
	if strings.Contains(form, "the name you type") {
		t.Errorf("the form still has English placeholders:\n%s", form)
	}

	// A host that came from the ssh config is not ohmyssh's to delete, and saying
	// so is the refusal a user is most likely to meet in Chinese — after the one
	// for a browser with no host service attached at all, which is the other way
	// this key can be pressed for nothing.
	withoutService := pressKey(t, sized(BrowserOptions{Hosts: testHosts(), HostsFile: hostsFile}), rune2('X')).View()
	if !strings.Contains(withoutService, "无法删除主机：没有接入主机服务") {
		t.Errorf("deleting with no host service is not refused in Chinese:\n%s", withoutService)
	}

	oursButNotHere := sized(BrowserOptions{
		Hosts:     testHosts(),
		HostsFile: hostsFile,
		Remove:    func(context.Context, config.SSHHost) ([]config.SSHHost, error) { return nil, nil },
	})
	refused := pressKey(t, oursButNotHere, rune2('X')).View()
	if !strings.Contains(refused, "web1 在 /etc/ssh_config 里") {
		t.Errorf("deleting a host from the ssh config is not refused in Chinese:\n%s", refused)
	}

	// The delete question, which the catalogue holds as whole sentences rather
	// than one with the file appended to it.
	h := newRemoveHosts(t, hostsInOurFile())
	h.ask(t)

	question := h.model.View()
	if want := "从 " + hostsFile + " 删除 web1？ y/n"; !strings.Contains(question, want) {
		t.Errorf("the delete question is missing %q:\n%s", want, question)
	}
	if want := "取消"; !strings.Contains(question, want) {
		t.Errorf("the question's bar does not offer %q:\n%s", want, question)
	}
}

// The file view is a frame of its own with its own words: the title, the two
// panes, its help, and the reasons a pane can be empty.
func TestTheFileViewSpeaksChinese(t *testing.T) {
	withLanguage(t, i18n.Chinese)

	m := openFileView(t, newFakeSession()).files

	view := m.View()
	for _, want := range []string{"文件 · web1", "[本地]", "[远端]", "栏位", "移动", "发送", "复制", "查找", "重读"} {
		if !strings.Contains(view, want) {
			t.Errorf("the file view does not say %q in Chinese:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"[LOCAL]", "[REMOTE]", "files ·", "reload"} {
		if strings.Contains(view, unwanted) {
			t.Errorf("the file view still says %q:\n%s", unwanted, view)
		}
	}

	// Its own help, which is a different table of keys.
	help := pressKey(t, openFileView(t, newFakeSession()), rune2('?')).View()
	for _, want := range []string{"按键 · 文件视图", "切换栏位", "进入目录；文件则发送到另一栏", "返回上一级目录"} {
		if !strings.Contains(help, want) {
			t.Errorf("the file view's help does not say %q in Chinese:\n%s", want, help)
		}
	}
	if strings.Contains(help, "keys · file view") {
		t.Errorf("the file view's help is still English:\n%s", help)
	}

	// And the ways a pane can have nothing to draw, which are the sentences a user
	// reads when something is wrong rather than when a directory is empty.
	connecting := m
	connecting.opening = true
	noSession := m
	noSession.session = nil
	noMatch := m
	noMatch.filter.SetValue("zzz")

	for _, tc := range []struct {
		why  string
		pane paneSide
		m    filesModel
		want string
	}{
		{"a directory with no entries", paneLocal, m, "空目录"},
		{"a session that is still dialling", paneRemote, connecting, "连接中…"},
		{"a pane with no session at all", paneRemote, noSession, "无会话"},
		{"a filter that matches nothing", paneLocal, noMatch, "无匹配"},
	} {
		if got := tc.m.paneEmpty(tc.pane); got != tc.want {
			t.Errorf("%s: the pane says %q, want %q", tc.why, got, tc.want)
		}
	}
}

// A transfer's summary line is built here and printed by both front ends, so it
// is the one line the command line and the browser have to agree on word for
// word. What it counts in is the catalogue's business; the verb is not, because
// it is the subcommand the user typed.
func TestTheTransferLineSpeaksChinese(t *testing.T) {
	withLanguage(t, i18n.Chinese)

	line := TransferSummary{
		Direction: sshclient.Upload,
		From:      "./dist",
		To:        "/opt/app/",
		Files:     3,
		Bytes:     4096,
	}.Line()

	for _, want := range []string{"put ./dist → /opt/app/", "3 个文件", "4.0 KB"} {
		if !strings.Contains(line, want) {
			t.Errorf("the summary line is %q, want it to say %q", line, want)
		}
	}
	// The verb stays the subcommand name: the line echoes what the user typed,
	// and put is what they typed.
	if strings.Contains(line, "files") || strings.Contains(line, "upload") {
		t.Errorf("the summary line reads %q, want the verb and the count to be the user's", line)
	}
}

// Every frame in every language holds the terminal it is drawn in: exactly its
// height, and no line wider than it. This is the test the width work is for —
// Chinese is two columns a character, so a sentence that fitted in English is
// not the same sentence here.
func TestEveryFrameFitsTheTerminalInEveryLanguage(t *testing.T) {
	for _, lang := range i18n.Languages {
		t.Run(string(lang), func(t *testing.T) {
			withLanguage(t, lang)

			for _, size := range frameSizes {
				m := helpBrowser()
				updated, _ := m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
				m = updated.(browserModel)

				frames := map[string]string{
					"the host list":     m.View(),
					"the help":          pressKey(t, m, rune2('?')).View(),
					"the add form":      pressKey(t, m, rune2('A')).View(),
					"the delete answer": pressKey(t, m, rune2('X')).View(),
					"the file view":     sizedFor(t, size.width, size.height, newFakeSession()).files.View(),
				}
				for name, frame := range frames {
					assertFrameFits(t, name, frame, size.width, size.height)
				}
			}
		})
	}
}

// The empty states are the sentences a user reads when the list has nothing in
// it, and they are drawn without a table column to clip them to — which makes
// them the easiest line in the program to push past the edge of a terminal. One
// of them is long in Chinese and has to fit the usual 80 columns.
func TestTheEmptyListIsNotClippedInEveryLanguage(t *testing.T) {
	// The three causes the list has nothing to show, each with its own sentence.
	empty := func(opts BrowserOptions) browserModel {
		m := sized(opts)
		updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		return updated.(browserModel)
	}

	for _, lang := range i18n.Languages {
		t.Run(string(lang), func(t *testing.T) {
			withLanguage(t, lang)

			noHosts := empty(BrowserOptions{})
			noConfig := empty(BrowserOptions{HostsFile: "/home/deploy/.ssh/config"})
			noMatch := empty(BrowserOptions{Hosts: testHosts()})
			for _, r := range "zzz" {
				noMatch = pressKey(t, noMatch, rune2(r))
			}

			for name, m := range map[string]browserModel{
				"no hosts at all":       noHosts,
				"nothing in the config": noConfig,
				"nothing matches":       noMatch,
			} {
				assertFrameFits(t, name, m.View(), 80, 24)
			}

			// Each cause says something different, which is the whole reason there
			// are three sentences rather than one.
			said := map[string]bool{}
			for _, m := range []browserModel{noHosts, noMatch} {
				said[m.View()] = true
			}
			if len(said) != 2 {
				t.Error("an empty config and a filter that matches nothing read the same")
			}
		})
	}
}

// assertFrameFits is the promise every frame makes: it is exactly the terminal's
// height, and no line in it is wider than the terminal.
func assertFrameFits(t *testing.T, name, frame string, width, height int) {
	t.Helper()

	lines := strings.Split(frame, "\n")
	if len(lines) != height {
		t.Errorf("%s at %dx%d is %d lines:\n%s", name, width, height, len(lines), frame)
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got > width {
			t.Errorf("%s at %dx%d: line %d is %d columns, %d past the edge:\n%s",
				name, width, height, i, got, got-width, frame)
		}
	}
}
