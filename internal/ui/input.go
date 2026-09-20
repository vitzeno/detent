package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/lipgloss"
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

func newInput() textarea.Model {
	ta := textarea.New()
	ta.Placeholder = "describe a goal, e.g. what is listening on port 3000?"
	ta.CharLimit = 4000
	ta.ShowLineNumbers = false
	// Enter submits the goal (see inputKey), so a literal newline
	// moves off it rather than doing both.
	ta.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("alt+enter", "ctrl+j"),
		key.WithHelp("alt+enter", "newline"),
	)
	// Prompt on the first row only; wrapped rows align under it.
	ta.SetPromptFunc(inputPromptW, func(row int) string {
		if row == 0 {
			return "❯ "
		}
		return "  "
	})
	ta.SetHeight(1)
	applyInputTheme(&ta)
	ta.Focus()
	return ta
}

// applyInputTheme keeps the input in the app's palette and strips the
// editor-style chrome (cursor line highlight) that suits a document
// but not a one-shot prompt.
func applyInputTheme(ta *textarea.Model) {
	plain := lipgloss.NewStyle()
	for _, s := range []*textarea.Style{&ta.FocusedStyle, &ta.BlurredStyle} {
		s.CursorLine = plain
		s.EndOfBuffer = plain
		s.Placeholder = styleFaint
	}
	ta.FocusedStyle.Prompt = styleRowCursor
	ta.BlurredStyle.Prompt = styleFaint
	ta.Cursor.Style = lipgloss.NewStyle().Foreground(accent)
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

// syncInputSize refits the input to the window and to what's typed in
// it. Called from sizeViewport, so the panes above give up the rows
// the input takes.
func (m *Model) syncInputSize() {
	outer := max(inputPromptW+1, m.layout.width-inputFrameW-inputMarkW)
	m.input.SetWidth(outer)
	m.input.SetHeight(inputRows(m.input.Value(), outer-inputPromptW))
}
