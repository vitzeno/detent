package ui

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

// The undo question offers only what the checkpoint covers: no files
// question without a tree, and no container without a snapshot.
func TestUndo_AsksWhatTheCheckpointCovers(t *testing.T) {
	cases := []struct {
		name       string
		checkpoint event.CheckpointTaken
		hint, page string
		key        string
		revert     bool
	}{
		{"sandbox and files", event.CheckpointTaken{Snapshot: "s", Tree: "t"},
			"[n/enter] container only · [y] revert your files too", "container goes back either way", "y", true},
		{"files only, on the host", event.CheckpointTaken{Tree: "t"},
			"[n/enter] conversation only · [y] revert your files too", "conversation goes back either way", "enter", false},
		{"sandbox only", event.CheckpointTaken{Snapshot: "s"},
			"[y/enter] undo · [n/esc] cancel", "were not checkpointed", "y", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			k := newKeyed(t)
			turn := uuid.Must(uuid.NewV7())
			c.checkpoint.Turn = turn
			for _, ev := range []event.Event{
				event.TurnStarted{Turn: turn, N: 1, Prompt: "p"}, c.checkpoint,
				event.TurnEnded{Turn: turn, Reason: event.EndDone},
			} {
				k.m.apply(ev)
			}
			k.m, _ = k.m.runUndo("/undo")
			require.Equal(t, modeUndo, k.m.mode)
			assert.Contains(t, k.m.statusHint(), c.hint)
			assert.Contains(t, stripANSI(strings.Join(k.m.undoLines(), "\n")), c.page)

			k.press(t, c.key)
			got, ok := k.intent(t).(event.RequestRollback)
			require.True(t, ok)
			assert.Equal(t, c.revert, got.RevertFiles)
		})
	}
}
