package ui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/vitzeno/detent/ui/slash"
)

const (
	// maxInputRows caps how far the input box grows; past it the
	// textarea scrolls its own content instead.
	maxInputRows = 10
	// inputPromptW is the width SetPromptFunc reserves for "❯ ".
	inputPromptW = 2
	// inputFrameW is the island's border and padding around the text.
	inputFrameW = 4
	// inputMarkW is the pane-mark gutter ("● ") to the left of the
	// box, which every row has to clear, not just the first.
	inputMarkW = 2
)

// prompt owns everything the human types before a goal starts: the
// input box and the slash dropdown above it. Nothing outside reaches
// into the textarea or the match list, so the rules about keeping
// those two in step live here rather than in seven other files.
type prompt struct {
	input   textarea.Model
	matches []slash.Cmd
	cursor  int
}

func newPrompt() prompt {
	ta := textarea.New()
	ta.Placeholder = "describe a goal, e.g. what is listening on port 3000?"
	ta.CharLimit = 4000
	ta.ShowLineNumbers = false
	// Enter submits the goal, so a literal newline moves off it rather
	// than doing both.
	ta.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("alt+enter", "ctrl+j"),
		key.WithHelp("alt+enter", "newline"),
	)
	// Prompt on the first row only; wrapped rows align under it.
	ta.SetPromptFunc(inputPromptW, func(p textarea.PromptInfo) string {
		if p.LineNumber == 0 {
			return "❯ "
		}
		return "  "
	})
	ta.SetHeight(1)
	applyInputTheme(&ta)
	ta.Focus()
	return prompt{input: ta}
}

// applyInputTheme keeps the input in the app's palette and strips the
// editor-style chrome (cursor line highlight) that suits a document
// but not a one-shot prompt.
func applyInputTheme(ta *textarea.Model) {
	plain := lipgloss.NewStyle()
	s := ta.Styles()
	for _, st := range []*textarea.StyleState{&s.Focused, &s.Blurred} {
		st.CursorLine = plain
		st.EndOfBuffer = plain
		st.Placeholder = styleFaint
	}
	s.Focused.Prompt = styleRowCursor
	s.Blurred.Prompt = styleFaint
	s.Cursor.Color = accent
	ta.SetStyles(s)
}

func (p prompt) Value() string      { return p.input.Value() }
func (p prompt) Focused() bool      { return p.input.Focused() }
func (p *prompt) Focus()            { p.input.Focus() }
func (p *prompt) Blur()             { p.input.Blur() }
func (p *prompt) SetValue(s string) { p.input.SetValue(s) }

// Key routes a keystroke into the box and re-matches the dropdown, so
// the two can't drift apart.
func (p *prompt) Key(msg tea.KeyPressMsg) tea.Cmd {
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	p.rematch()
	return cmd
}

// Clear empties the box and closes the dropdown.
func (p *prompt) Clear() {
	p.input.SetValue("")
	p.Close()
}

func (p *prompt) rematch() {
	p.matches = slash.Match(p.input.Value())
	if p.cursor >= len(p.matches) {
		p.cursor = 0
	}
}

// Open reports whether the dropdown has anything to show.
func (p prompt) Open() bool { return len(p.matches) > 0 }

// Move walks the dropdown selection, clamped at both ends.
func (p *prompt) Move(delta int) {
	p.cursor = min(max(p.cursor+delta, 0), len(p.matches)-1)
}

// Accept completes the highlighted entry into the box and closes the
// dropdown. False when there was nothing to accept.
func (p *prompt) Accept() bool {
	if !p.Open() {
		return false
	}
	p.input.SetValue(p.matches[p.cursor].Name + " ")
	p.Close()
	return true
}

func (p *prompt) Close() {
	p.matches = nil
	p.cursor = 0
}

// IsExactCommand reports whether what's typed is already a whole
// command, so enter should run it rather than complete it.
func (p prompt) IsExactCommand() bool { return slash.Exact(p.input.Value()) }

// DropdownRows is capped so the dropdown can't eat the history pane.
func (p prompt) DropdownRows() int { return min(len(p.matches), slash.MaxRows) }

// Rows is how many rows the whole prompt occupies, dropdown included.
func (p prompt) Rows() int { return p.DropdownRows() + p.input.Height() }

// Resize refits the box to the window and to what's typed in it.
func (p *prompt) Resize(width int) {
	outer := max(inputPromptW+1, width-inputFrameW-inputMarkW)
	p.input.SetWidth(outer)
	p.input.SetHeight(inputRows(p.input.Value(), outer-inputPromptW))
}

// inputRows is how many display rows value needs once soft-wrapped at
// width, clamped to maxInputRows. A line that exactly fills the width
// costs an extra row, since that's where the cursor sits.
func inputRows(value string, width int) int {
	if width < 1 {
		width = 1
	}
	rows := 0
	for _, line := range strings.Split(value, "\n") {
		rows += lipgloss.Width(line)/width + 1
	}
	return min(max(rows, 1), maxInputRows)
}

// View stacks the dropdown above the box. Both are multi-line, so the
// pane mark takes its own gutter beside every input row instead of
// being prefixed onto the block, which would indent only the first.
func (p prompt) View(mark string) string {
	var b strings.Builder
	b.WriteString(slash.View(p.matches, p.cursor))
	for i, line := range strings.Split(p.input.View(), "\n") {
		if i > 0 {
			b.WriteString("\n" + strings.Repeat(" ", inputMarkW))
		} else {
			b.WriteString(mark + " ")
		}
		b.WriteString(line)
	}
	return b.String()
}
