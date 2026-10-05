package ui

import (
	"time"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
)

// The small state values Model is composed of. Grouped rather than
// spread across Model's own fields, so a handler takes one thing.

// mode is which input state the bottom zone is in.
type mode int

const (
	modeInput     mode = iota
	modeConfirm        // a dangerous call is waiting on an answer
	modeBound          // the engine hit its step bound and is asking
	modeUndo           // asks before reverting the human's own files
	modeForget         // asks before deleting a stored session
	modeFinder         // the finder holds every key until it jumps or closes
	modeInspector      // one subagent's work, over the panes, until esc
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
	// histWindow is what sizeViewport laid out, so View need not.
	histWindow []string
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
	panelContext
	panelStatus
	panelHelp
	panelSessions
	panelMCP
	panelSkills
)

// finderState is the finder: what is typed, what it matched, and where the
// human was so esc can put them back.
type finderState struct {
	query  string
	kind   finderKind
	hits   []finderHit
	cursor int
	// scroll moves the preview from where it centres on the match.
	scroll int
	saved  navState
}

// inspectorState is the subagent being looked into and where in its work.
type inspectorState struct {
	agent  *agentState
	cursor int
	// output is the left pane's scroll, and outputFocused that arrows move it.
	output        int
	outputFocused bool
	// shown is the question on screen and when it appeared, so a key typed
	// before it did is not an answer, and seenEnd whether all of it was read.
	shown   uuid.UUID
	shownAt time.Time
	seenEnd bool
	// back is the pane it was opened from, which esc returns to.
	back focusPane
}

// forgetState is the session /delete is asking about.
type forgetState struct {
	target *event.SessionSummary
}

// confirmState is how far the human has read a command too tall for
// its box. seenEnd sticks, so scrolling back up does not unread it.
type confirmState struct {
	top     int
	seenEnd bool
}

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

func (m *Model) noteOK(text string)  { m.note(noticeState{text: text}) }
func (m *Model) noteErr(text string) { m.note(noticeState{text: text, bad: true}) }

// note drops the flash while replaying: resuming a session would
// otherwise announce every notice the original run ever showed.
func (m *Model) note(n noticeState) {
	if m.replaying {
		return
	}
	m.notice = n
}

func (m *Model) clearNotice() { m.notice = noticeState{} }

// noteLevel routes an engine Notice to the same flash.
func (m *Model) noteLevel(level, text string) {
	if level == "error" || level == "warn" {
		m.noteErr(text)
		return
	}
	m.noteOK(text)
}
