package ui

// The small state values Model is composed of. Grouped rather than
// spread across Model's own fields, so a handler takes one thing.

// mode is which input state the bottom zone is in.
type mode int

const (
	modeInput   mode = iota
	modeConfirm      // a dangerous call is waiting on an answer
	modeBound        // the engine hit its step bound and is asking
	modeUndo         // asks before reverting the human's own files
)

// focusPane is which zone the arrow keys act in.
type focusPane int

const (
	focusInput focusPane = iota
	focusHistory
	focusOutput
)

// navState is history and output navigation. histOffset is the only
// scroll state kept: what is visible is derived per render.
type navState struct {
	cursor int
	follow bool
	focus  focusPane

	histHeight int
	histOffset int
}

// layoutState is the body row's pane widths, recomputed by sizeViewport.
type layoutState struct {
	width, height int
	outputColW    int
	histColW      int
}

// panelState is the open read-only page, if any. An overlay, not a
// block, so opening one leaves history alone.
type panelState struct {
	open panelKind
}

type panelKind int

const (
	panelNone panelKind = iota
	panelUsage
	panelStatus
	panelHelp
	panelSessions
)

// undoState is an undo waiting on the human to say whether their own
// files go back with the container.
type undoState struct {
	target *turnBlock
}

// noticeState is the one-shot status flash: text and outcome in one
// value so they cannot disagree.
type noticeState struct {
	text string
	bad  bool
}

func (m *Model) noteOK(text string)  { m.notice = noticeState{text: text} }
func (m *Model) noteErr(text string) { m.notice = noticeState{text: text, bad: true} }
func (m *Model) clearNotice()        { m.notice = noticeState{} }

// noteLevel routes an engine Notice to the same flash.
func (m *Model) noteLevel(level, text string) {
	if level == "error" || level == "warn" {
		m.noteErr(text)
		return
	}
	m.noteOK(text)
}
