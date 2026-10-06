package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

// The picker opens past this session, asks for the selected one's records,
// and draws its history from them as the history pane would.
func TestResume_PreviewsTheSelectedSessionsHistory(t *testing.T) {
	k, other := resumable(t)
	k.openResume()
	require.NotNil(t, k.picker())
	assert.Equal(t, other, k.picker().selected(k.m).ID, "it opens on another session, not this one")
	k.m.apply(storedSession(other, "fix the flaky test"))
	screen := ansi.Strip(k.m.withOverlay(k.m.baseView()))
	assert.Contains(t, screen, "the sandbox", "sessions are listed by name")
	assert.Contains(t, screen, "fix the flaky test", "and the selected one's history drawn")
	assert.Contains(t, screen, "last used")
	assert.Contains(t, screen, "created")
}

// Each session says when it was last used and when it began, this one being used now.
func TestResume_ListsWhenEachSessionWasUsedAndCreated(t *testing.T) {
	k, other := resumable(t)
	used, created := time.Now().Add(-3*time.Hour), time.Now().Add(-12*24*time.Hour)
	k.m.sessions[1].Used, k.m.sessions[1].Started = used, created
	k.openResume()
	lines := k.picker().listLines(k.m, 40, 10)
	require.Len(t, lines, 3, "a header and two sessions")
	assert.Contains(t, ansi.Strip(lines[1]), "now", "this session is the one in use")
	row := ansi.Strip(lines[2])
	assert.Contains(t, row, "the sandbox", "a narrow pane still has room for the name")
	assert.Less(t, strings.Index(row, "3h ago"), strings.Index(row, "12d ago"), "last used, then created")
	assert.Equal(t, other, k.m.sessions[1].ID)

	panel := ansi.Strip(strings.Join(k.m.sessionLines(), "\n"))
	want := used.Local().Format("2006-01-02 15:04") + "  " + created.Local().Format("2006-01-02 15:04")
	assert.Contains(t, panel, want, "and /sessions has both columns, as dates")
}

// Moving onto a session asks for it once, so a preview is only loaded when looked at.
func TestResume_LoadsASessionWhenTheCursorFirstReachesIt(t *testing.T) {
	k, other := resumable(t)
	k.openResume()
	assert.Equal(t, other, k.intentOf(t, event.LoadSessionKind).(event.LoadSession).Session)

	k.press(t, "up")
	assert.Equal(t, k.m.run.Session, k.intentOf(t, event.LoadSessionKind).(event.LoadSession).Session)
	k.press(t, "down")
	k.noIntent(t)
}

// enter resumes it, and once it has started history is that session's,
// replayed from the records the picker already holds.
func TestResume_EnterSwapsHistoryForTheChosenSession(t *testing.T) {
	k, other := resumable(t)
	k.openResume()
	k.m.apply(storedSession(other, "fix the flaky test"))
	k.press(t, "enter")
	assert.Equal(t, other, k.intentOf(t, event.ResumeSessionKind).(event.ResumeSession).Session)
	assert.Equal(t, modeInput, k.m.mode)

	k.m.apply(event.SessionStarted{Session: other, Model: "m"})
	k.m.sizeViewport()
	prompts := make([]string, 0, len(k.m.blocks))
	for _, b := range k.m.blocks {
		prompts = append(prompts, b.prompt)
	}
	assert.Equal(t, []string{"fix the flaky test"}, prompts, "this session's history went, the other's came back")
	assert.Len(t, k.m.sessions, 2, "the listing is not the stale one the replay carried")
}

// The engine refuses a resume mid-request, so the picker says so rather than asking.
func TestResume_IsRefusedWhileARequestRuns(t *testing.T) {
	k, other := resumable(t)
	k.m.apply(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 2, Prompt: "still going"})
	k.openResume()
	k.m.apply(storedSession(other, "fix the flaky test"))
	k.intentOf(t, event.LoadSessionKind)
	k.press(t, "enter")
	k.noIntent(t)
	assert.Contains(t, k.m.notice.text, "a request is running")
	assert.NotNil(t, k.picker())
}

func TestResume_EscLeavesWithoutResuming(t *testing.T) {
	k, _ := resumable(t)
	k.openResume()
	k.intentOf(t, event.LoadSessionKind)
	k.press(t, "esc")
	assert.Equal(t, modeInput, k.m.mode)
	k.noIntent(t)
}

// picker is the open resume picker, nil when it is not.
func (k *keyed) picker() *resumeModal { return modalAs[*resumeModal](k.m) }

// openResume opens the picker and runs what it asks for.
func (k *keyed) openResume() {
	var cmd tea.Cmd
	k.m, cmd = k.m.showResume("/resume")
	runCmd(cmd)
}

// resumable is a session with a request done, and another stored beside it.
func resumable(t *testing.T) (*keyed, uuid.UUID) {
	t.Helper()
	k := newKeyed(t)
	mine, other := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	turn := uuid.Must(uuid.NewV7())
	for _, e := range []event.Event{
		event.SessionStarted{Session: mine, Model: "m"},
		event.TurnStarted{Turn: turn, N: 1, Prompt: "the current work"},
		event.TurnEnded{Turn: turn, Reason: event.EndDone},
		event.SessionsListed{Sessions: []event.SessionSummary{
			{ID: mine, Started: time.Now(), Model: "m", Events: 3},
			{ID: other, Name: "the sandbox bug", Started: time.Now().Add(-time.Hour),
				Used: time.Now().Add(-time.Minute), Model: "m", Events: 4},
		}},
	} {
		k.m.apply(e)
	}
	k.m.sizeViewport()
	return k, other
}

// storedSession is the store's answer for a session with one request.
func storedSession(id uuid.UUID, prompt string) event.SessionLoaded {
	turn := uuid.Must(uuid.NewV7())
	return event.SessionLoaded{Session: id, Records: asRecords([]event.Event{
		event.SessionStarted{Session: id, Model: "m"},
		event.TurnStarted{Turn: turn, N: 1, Prompt: prompt},
		event.ModelText{Turn: turn, Text: strings.ToUpper(prompt[:1]) + prompt[1:] + " is done."},
		event.TurnEnded{Turn: turn, Reason: event.EndDone},
		event.SessionsListed{},
	})}
}

// A listing stored before sessions said when they were used reads as never
// used, so it falls back to when the session began rather than year zero.
func TestResume_AListingWithNoLastUseReadsAsItsStart(t *testing.T) {
	started := time.Now().Add(-2 * time.Hour)
	assert.Equal(t, "2h ago", ago(used(event.SessionSummary{Started: started})))
}
