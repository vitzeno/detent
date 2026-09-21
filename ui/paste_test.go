package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPaste_LandsInTheInput(t *testing.T) {
	m := testUIModel()
	m.prompt.Focus()

	nm, _ := m.Update(tea.PasteMsg{Content: "find . -name '*.go' -size +1M"})
	m = nm.(Model)
	assert.Equal(t, "find . -name '*.go' -size +1M", m.prompt.Value())

	// At the cursor, not over what is already there.
	nm, _ = m.Update(tea.PasteMsg{Content: " | head"})
	m = nm.(Model)
	assert.Equal(t, "find . -name '*.go' -size +1M | head", m.prompt.Value())
}

// A pasted command has to open its dropdown, or the box and the list
// disagree about what is being typed.
func TestPaste_OpensTheSlashDropdown(t *testing.T) {
	m := testUIModel()
	m.prompt.Focus()

	nm, _ := m.Update(tea.PasteMsg{Content: "/roll"})
	m = nm.(Model)
	assert.True(t, m.prompt.Open(), "matched a slash command")
}

func TestPaste_MultipleLinesSurvive(t *testing.T) {
	m := testUIModel()
	m.prompt.Focus()

	nm, _ := m.Update(tea.PasteMsg{Content: "one\ntwo\nthree"})
	m = nm.(Model)
	assert.Equal(t, "one\ntwo\nthree", m.prompt.Value(),
		"a multi-line paste is not flattened; alt+enter types one by hand")
}

func TestPaste_GoesToTheEditorWhileEditing(t *testing.T) {
	row, _ := rowWithEditor(t, "hi\n")
	m := testUIModel()
	m.blocks = []*goalBlock{{goal: "g", steps: []*stepRow{row}}}
	m.nav.focus = focusOutput
	m.nav.cursor = 0
	m.save.editing = true
	row.editor.Focus()

	nm, _ := m.Update(tea.PasteMsg{Content: "pasted"})
	m = nm.(Model)
	assert.Contains(t, row.editor.Value(), "pasted")
	assert.True(t, row.editor.Dirty())
	assert.Empty(t, m.prompt.Value(), "and not into the prompt behind it")
}

// Panes with nothing to type into drop it rather than sending it
// somewhere the human cannot see.
func TestPaste_IgnoredWhereThereIsNothingToTypeInto(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(m *Model)
	}{
		{"history", func(m *Model) { m.nav.focus = focusHistory }},
		{"output", func(m *Model) { m.nav.focus = focusOutput }},
		{"a confirm", func(m *Model) { m.mode = modeConfirm }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := testUIModel()
			m.prompt.Focus()
			tc.setup(&m)

			nm, cmd := m.Update(tea.PasteMsg{Content: "rm -rf /"})
			m = nm.(Model)
			require.Nil(t, cmd)
			assert.Empty(t, m.prompt.Value())
		})
	}
}
