package ui

import (
	"context"
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/maogou/ohmyssh/internal/config"
	"github.com/maogou/ohmyssh/internal/i18n"
)

// AddHostFunc writes a host the user filled in and returns the hosts as they
// read afterwards, so the browser can show the new one without reading the
// config again. Where the host is written and what counts as a clash are the
// service's business; the fields are the form's.
type AddHostFunc func(ctx context.Context, host config.NewHost) ([]config.SSHHost, error)

// hostAddedMsg reports the outcome of an add back to the browser, which is the
// only thing that can put a new host in the list.
type hostAddedMsg struct {
	host  config.NewHost
	hosts []config.SSHHost
	err   error
}

// The form's fields, in the order they are asked in — which is the order the
// block is written in: what to call the host, then where it is and who to be
// there.
const (
	formAlias = iota
	formHost
	formUser
	formPort
	formTags
	formFields
)

// formField is one line of the form: the label on the left, and what the field
// is for when it is left empty.
type formField struct {
	label       string
	placeholder string
}

// formLabels is the form's fields in the language that is installed. It is read
// on every frame rather than kept in a package variable, because the labels are
// words and the language is only decided at startup.
func formLabels() [formFields]formField {
	m := i18n.M()
	return [formFields]formField{
		formAlias: {m.FormAlias, m.FormAliasPlaceholder},
		formHost:  {m.FormHost, m.FormHostPlaceholder},
		formUser:  {m.FormUser, m.FormUserPlaceholder},
		formPort:  {m.FormPort, m.FormPortPlaceholder},
		formTags:  {m.FormTags, m.FormTagsPlaceholder},
	}
}

// formLabelWidth is the column the labels are padded to, measured from the
// labels themselves so that a longer one cannot silently push the values out of
// line. It is measured rather than fixed for the same reason it cannot be a
// constant: 名称 and NAME are not the same width, and neither are the labels of
// the next language.
func formLabelWidth() int {
	width := 0
	for _, field := range formLabels() {
		width = max(width, lipgloss.Width(field.label))
	}
	return width
}

const (
	// formGap is the space between the label column and the value, wide enough to
	// read as a gap rather than as part of either.
	formGap = 2
	// formCharLimit bounds one field. It is generous for a host name or a login,
	// and far short of anything that would have to be written out carefully.
	formCharLimit = 200
)

// hostForm is the add form. Each field is a text input, so editing, the cursor
// and pasting are bubbles' business; all this owns is the labels around them,
// which field the keyboard is on, and the region of the frame they are drawn in.
type hostForm struct {
	inputs [formFields]textinput.Model
	focus  int
}

// newHostForm returns an empty form drawn to the given content width, with the
// keyboard on the first field.
func newHostForm(contentWidth int) hostForm {
	f := hostForm{}
	for i, field := range formLabels() {
		input := textinput.New()
		// The label column is this form's prompt; the one bubbles draws by
		// default would be two columns the width was not counted with.
		input.Prompt = ""
		input.Placeholder = field.placeholder
		input.PlaceholderStyle = hintStyle
		input.Cursor.Style = cursorStyle
		input.CharLimit = formCharLimit
		f.inputs[i] = input
	}
	f.resize(contentWidth)
	f.focusOn(formAlias)
	return f
}

// resize records the width the fields are drawn to: what the content width has
// left once the marker, the label column and the gap have taken theirs. A text
// input sizes its own scrolling window from its Width, so it has to be told
// rather than measured.
func (f *hostForm) resize(contentWidth int) {
	valueWidth := max(contentWidth-rowIndent-formLabelWidth()-formGap-cursorCellSpare, 1)
	for i := range f.inputs {
		f.inputs[i].Width = valueWidth
	}
}

// focusOn puts the keyboard on one field, wrapping at either end, and returns
// the blink command the newly focused input asks for.
func (f *hostForm) focusOn(i int) tea.Cmd {
	count := len(f.inputs)
	f.focus = ((i % count) + count) % count

	for j := range f.inputs {
		if j == f.focus {
			f.inputs[j].TextStyle = nameStyle
			continue
		}
		f.inputs[j].Blur()
		// An unfocused field is a label and a value rather than a control, so it
		// reads as the line's meta instead of competing with the line being typed
		// on.
		f.inputs[j].TextStyle = metaStyle
	}
	return f.inputs[f.focus].Focus()
}

// update hands a message to the focused field. Only the one field the keyboard
// is on is ever told anything, so a stray key cannot edit a field the user is
// not looking at.
func (f *hostForm) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	f.inputs[f.focus], cmd = f.inputs[f.focus].Update(msg)
	return cmd
}

// spec is what the fields currently say, in the shape the config package writes.
// Space around a value is dropped and the tag list is split the way the parser
// splits it back, so what the form shows is what the block will hold.
func (f hostForm) spec() config.NewHost {
	return config.NewHost{
		Alias: strings.TrimSpace(f.inputs[formAlias].Value()),
		Host:  strings.TrimSpace(f.inputs[formHost].Value()),
		User:  strings.TrimSpace(f.inputs[formUser].Value()),
		Port:  strings.TrimSpace(f.inputs[formPort].Value()),
		Tags:  splitTags(f.inputs[formTags].Value()),
	}
}

// splitTags reads the tag field the way the config parser reads the line it goes
// on: a comma separates two tags, and space around one is not part of it.
func splitTags(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
}

// lines draws the fields into the region of the frame the table takes in the
// list. A field that does not fit is left out, but the one being typed on is
// always drawn: a form that hides its own cursor is worse than one that hides a
// label.
func (f hostForm) lines(contentWidth, rows int) []string {
	lines := make([]string, 0, rows)

	start := 0
	if len(f.inputs) > rows {
		start = min(f.focus, len(f.inputs)-rows)
	}
	for i := start; i < len(f.inputs) && len(lines) < rows; i++ {
		lines = append(lines, f.line(i))
	}
	// The rule under the fields is the first thing given up: five fields that do
	// not fit are a problem, a missing separator is not.
	if len(lines) < rows {
		lines = append(lines, rule(contentWidth))
	}
	for len(lines) < rows {
		lines = append(lines, "")
	}
	return lines
}

// line draws one field: the label in a fixed column so the values line up, then
// the value itself — the input's own view, which is where the cursor is drawn.
func (f hostForm) line(i int) string {
	marker, label := strings.Repeat(" ", rowIndent), metaStyle
	if i == f.focus {
		marker, label = rowMarker, cursorStyle
	}
	return margin + marker +
		label.Render(padTo(formLabels()[i].label, formLabelWidth(), false)) +
		strings.Repeat(" ", formGap) + f.inputs[i].View()
}

// formFooterSegments are the form's bindings. Saving is the one that has to
// survive a narrow terminal, so it is first; ctrl+c is not listed because the
// bar cannot wrap and a form is not where it is looked for.
func formFooterSegments() []binding {
	m := i18n.M()
	return []binding{
		{"enter", m.HintSave},
		{"tab", m.HintField},
		{"esc", m.HintCancel},
	}
}
