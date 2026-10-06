package ui

import tea "charm.land/bubbletea/v2"

// modal is a box over the panes that holds every key until it closes: the
// finder, the inspector, the resume picker or the review. Its state is its own
// and goes when it closes. m is the session it is open over.
type modal interface {
	// key handles a keystroke, closing the modal with m.closeModal when it is done.
	key(m *Model, msg tea.KeyPressMsg) tea.Cmd
	// sync catches up with what the last message changed, once the panes are sized.
	sync(m *Model)
	// box draws it, and hint is the status bar's line beneath.
	box(m Model) string
	hint(m Model) string
}

// openModal puts md over the panes, holding back any question until it closes.
func (m *Model) openModal(md modal) {
	m.modal, m.mode = md, modeModal
	m.prompt.Blur()
}

// closeModal returns to the pane back, and puts up whatever question waited.
func (m *Model) closeModal(back focusPane) {
	m.modal = nil
	m.backToInput()
	if m.mode == modeInput && back != focusInput {
		m.nav.focus = back
		m.prompt.Blur()
	}
}

// modalAs is the open modal as a T, nil when none is or another kind is.
func modalAs[T modal](m Model) T {
	md, _ := m.modal.(T)
	return md
}
