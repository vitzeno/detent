package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

// Quitting mid-request abandons work a human is watching, so ctrl+c
// asks once. Idle, it is the ordinary way out and must not.
func TestQuit_AsksOnlyWhileARequestIsRunning(t *testing.T) {
	tests := []struct {
		name    string
		evs     []event.Event
		wantsUp bool
	}{
		{"idle", nil, true},
		{"mid-request", runningTurn(), false},
		{"request finished", finishedTurn(), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := feed(t, tt.evs...)
			_, cmd := m.update(ctrlC)
			assert.Equal(t, tt.wantsUp, quits(cmd))
		})
	}
}

// Having asked, the next ctrl+c is the answer.
func TestQuit_TheSecondPressGoes(t *testing.T) {
	m := feed(t, runningTurn()...)

	m, cmd := m.update(ctrlC)
	require.False(t, quits(cmd))
	assert.Contains(t, m.notice.text, "again to quit", "nothing told them it was asking")

	_, cmd = m.update(ctrlC)
	assert.True(t, quits(cmd), "the second ctrl+c did not quit")
}

// A half-asked quit is taken back by carrying on typing: the next
// ctrl+c after that is a fresh question, not the answer to the old one.
func TestQuit_AnotherKeyTakesItBack(t *testing.T) {
	m := feed(t, runningTurn()...)

	next, _ := m.update(ctrlC)
	next, _ = next.update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	_, cmd := next.update(ctrlC)

	assert.False(t, quits(cmd), "a quit asked for and typed past still went through")
}

// /quit is the same question, and says so in the words they used.
func TestQuit_TheSlashCommandAsksToo(t *testing.T) {
	m := feed(t, runningTurn()...)

	m, cmd := m.runSlash("/quit")
	assert.False(t, quits(cmd), "/quit quit mid-request without asking")
	assert.Contains(t, m.notice.text, "/quit again to quit")
}

var ctrlC = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}

func runningTurn() []event.Event {
	_, evs := aTurn("count the go files")
	return evs
}

func finishedTurn() []event.Event {
	turn, evs := aTurn("count the go files")
	return append(evs, event.TurnEnded{Turn: turn, Reason: event.EndDone})
}

// quits reports whether a command is the one that ends the program.
func quits(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}
