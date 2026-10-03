package ui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// prompt is the input box and the slash dropdown above it, owned
// together so nothing else can put the two out of step.
type prompt struct { //nolint:recvcheck // readers take a value so View can call them on a copy
	input   textarea.Model
	matches []slashCmd
	// extra are commands beyond the built-ins, a skill's /name.
	extra  []slashCmd
	cursor int

	// richKeys is true once the terminal agreed to the Kitty keyboard
	// protocol, without which shift+enter arrives as a plain enter.
	richKeys bool
	// shell switches the box to commands, which closes the dropdown:
	// /usr/bin has to be typable.
	shell bool
	// powershell is true when those commands are PowerShell, which prompts PS>.
	powershell bool
}

func newPrompt(powershell bool) prompt {
	ta := textarea.New()
	// No limit: a cut paste would send the model less than the human did.
	ta.CharLimit = 0
	ta.ShowLineNumbers = false
	// Enter submits, so a newline moves off it. shift+enter needs the
	// Kitty protocol, the other two work everywhere.
	ta.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("shift+enter", "alt+enter", "ctrl+j"),
		key.WithHelp("shift+enter", "newline"),
	)
	ta.SetHeight(1)
	applyInputTheme(&ta)
	ta.Focus()
	p := prompt{input: ta, powershell: powershell}
	// Sets the glyph and the placeholder, so they have one spelling.
	p.SetShell(false)
	return p
}

// SetShell switches the box between a request and a command, glyph
// and all: the glyph is the only thing on screen that says which.
func (p *prompt) SetShell(on bool) {
	p.shell = on
	glyph, hint := p.glyph(), "describe a goal, e.g. what is listening on port 3000?"
	if on {
		hint = "run a command, e.g. git status"
	}
	width := lipgloss.Width(glyph)
	p.input.Placeholder = hint
	// The glyph carries the same signal as the border around it.
	st := p.input.Styles()
	st.Focused.Prompt = styleRowCursor
	if on {
		st.Focused.Prompt = styleCaution
	}
	p.input.SetStyles(st)
	// Prompt on the first row only, wrapped rows align under it.
	p.input.SetPromptFunc(width, func(i textarea.PromptInfo) string {
		if i.LineNumber == 0 {
			return glyph
		}
		return strings.Repeat(" ", width)
	})
	p.rematch()
}

// mark is what a human's own command is shown with, the prompt it was typed at.
func (p prompt) mark() string {
	if p.powershell {
		return "PS>"
	}
	return "$"
}

// glyph opens the box's first row: a request, or a command as the shell prompts for one.
func (p prompt) glyph() string {
	switch {
	case !p.shell:
		return "❯ "
	case p.powershell:
		return "PS> "
	}
	return "$ "
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

// Paste drops text in at the cursor and re-matches, so pasting a
// slash command opens its dropdown exactly as typing one does.
func (p *prompt) Paste(s string) {
	p.input.InsertString(s)
	p.rematch()
}

// Clear empties the box and closes the dropdown.
func (p *prompt) Clear() {
	p.input.SetValue("")
	p.Close()
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

// SetExtraCommands adds commands beyond the built-ins to the dropdown.
func (p *prompt) SetExtraCommands(cmds []slashCmd) { p.extra = cmds }

// SetRichKeys records what the terminal agreed to, so the hint can
// name a key that actually works there.
func (p *prompt) SetRichKeys(ok bool) { p.richKeys = ok }

// NewlineKey is which binding to advertise. All three stay bound, but
// without the Kitty protocol shift+enter is just enter.
func (p prompt) NewlineKey() string {
	if p.richKeys {
		return "shift+enter"
	}
	return "alt+enter"
}

// DropdownRows is how tall the dropdown renders: the visible slice,
// plus the row naming what's scrolled out of view.
func (p prompt) DropdownRows() int {
	start, end := slashWindow(len(p.matches), p.cursor)
	rows := end - start
	if rows < len(p.matches) {
		rows++
	}
	return rows
}

// Rows is how many rows the whole prompt occupies, dropdown included.
func (p prompt) Rows() int { return p.DropdownRows() + p.input.Height() }

// Resize refits the box to the window and to what's typed in it.
func (p *prompt) Resize(width int) {
	glyphW := lipgloss.Width(p.glyph())
	outer := max(glyphW+1, width-inputFrameW-inputMarkW)
	p.input.SetWidth(outer)
	p.input.SetHeight(inputRows(p.input.Value(), outer-glyphW))
}

// View stacks the dropdown above the box. The pane mark gets a gutter
// beside every input row, since prefixing it would indent only the first.
func (p prompt) View(mark string) string {
	var b strings.Builder
	b.WriteString(slashDropdown(p.matches, p.cursor))
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

const (
	// maxInputRows caps how far the input box grows. Past it the
	// textarea scrolls its own content instead.
	maxInputRows = 10
	// inputFrameW is the island's border and padding around the text.
	inputFrameW = 4
	// inputMarkW is the pane-mark gutter ("● ") to the left of the
	// box, which every row has to clear, not just the first.
	inputMarkW = 2
)

func (p *prompt) rematch() {
	if p.shell {
		p.Close()
		return
	}
	p.matches = matchSlash(p.input.Value(), p.extra)
	if p.cursor >= len(p.matches) {
		p.cursor = 0
	}
}

// applyInputTheme keeps the input in the app's palette and strips
// the document chrome that suits an editor but not a prompt.
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

// inputRows is the display rows value needs soft-wrapped at width, up to
// maxInputRows. A line that fills the width costs a row for the cursor.
func inputRows(value string, width int) int {
	if width < 1 {
		width = 1
	}
	rows := 0
	for line := range strings.SplitSeq(value, "\n") {
		rows += lipgloss.Width(line)/width + 1
	}
	return min(max(rows, 1), maxInputRows)
}
