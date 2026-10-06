package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

// Nothing brings a deleted session back, so the page names what goes
// rather than counting it.
func TestForgetPage_NamesWhatGoesAndWhatStays(t *testing.T) {
	m, other := sessionsListed(t)
	got, _ := m.runForget("/delete the sandbox bug")
	require.Equal(t, modeForget, got.mode)

	page := stripANSI(strings.Join(got.forgetLines(), "\n"))
	assert.Contains(t, page, other.String())
	assert.Contains(t, page, "the sandbox bug")
	assert.Contains(t, page, "871 events")
	assert.Contains(t, page, "container")
	assert.Contains(t, page, "log stays", "the log being kept is worth saying")

	assert.Contains(t, stripANSI(got.questionBox()), "cannot be undone")
}

// The running session has its store and container open.
func TestForgetPage_RefusesTheRunningSession(t *testing.T) {
	m, _ := sessionsListed(t)
	got, _ := m.runForget("/delete current")

	assert.NotEqual(t, modeForget, got.mode, "it asked about the running session")
	assert.Contains(t, got.notice.text, "running session")
}

func TestForgetPage_RefusesWhatItCannotFind(t *testing.T) {
	m, _ := sessionsListed(t)
	for _, arg := range []string{"", "no-such-session"} {
		got, _ := m.runForget("/delete " + arg)
		assert.NotEqual(t, modeForget, got.mode)
		assert.NotEmpty(t, got.notice.text)
	}
}

// Every key but y cancels: a deleted session does not come back.
func TestForgetPage_OnlyYDeletes(t *testing.T) {
	for _, key := range []struct {
		name string
		msg  tea.KeyPressMsg
	}{
		{"n", tea.KeyPressMsg{Code: 'n', Text: "n"}},
		{"enter", tea.KeyPressMsg{Code: tea.KeyEnter}},
		{"esc", tea.KeyPressMsg{Code: tea.KeyEscape}},
		{"a stray letter", tea.KeyPressMsg{Code: 'q', Text: "q"}},
	} {
		t.Run(key.name, func(t *testing.T) {
			mine, other := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			m := New(t.Context(), event.New(), SessionInfo{})
			m.apply(event.SessionStarted{Session: mine, Model: "m"})
			m.apply(event.SessionsListed{Sessions: []event.SessionSummary{
				{ID: other, Name: "doomed", Started: time.Now(), Model: "m", Events: 3},
			}})

			next, _ := m.runForget("/delete doomed")
			after, cmd := next.forgetKey(key.msg)
			assert.Nil(t, after.forget.target, "%s left it armed", key.name)
			assert.NotEqual(t, modeForget, after.mode)

			// Clearing the state is what cancel and confirm have in common.
			// A command to publish is the only thing that tells them apart.
			assert.Nil(t, cmd, "%s deleted the session", key.name)
		})
	}
}

// And y publishes the intent, claiming nothing itself.
func TestForgetPage_YPublishesTheIntent(t *testing.T) {
	bus := event.New()
	seen, stop := bus.Subscribe(event.Only(event.DeleteSessionKind))
	defer stop()

	mine, other := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	m := New(t.Context(), bus, SessionInfo{})
	m.apply(event.SessionStarted{Session: mine, Model: "m"})
	m.apply(event.SessionsListed{Sessions: []event.SessionSummary{
		{ID: other, Name: "doomed", Started: time.Now(), Model: "m", Events: 3},
	}})

	next, _ := m.runForget("/delete doomed")
	next.forgetKey(tea.KeyPressMsg{Code: 'y', Text: "y"})

	select {
	case rec := <-seen:
		assert.Equal(t, other, rec.Event.(event.DeleteSession).Session)
	case <-time.After(2 * time.Second):
		t.Fatal("y published nothing")
	}
}

// sessionsListed puts two sessions in view, one of them the running one.
func sessionsListed(t *testing.T) (Model, uuid.UUID) {
	t.Helper()
	mine, other := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	m := feed(t, event.SessionStarted{Session: mine, Model: "m"},
		event.SessionsListed{Sessions: []event.SessionSummary{
			{ID: mine, Name: "current", Started: time.Now(), Model: "m", Events: 12},
			{ID: other, Name: "the sandbox bug", Started: time.Now(), Model: "m", Events: 871},
		}})
	m.layout.width, m.layout.height = 150, 45
	m.sizeViewport()
	return m, other
}
