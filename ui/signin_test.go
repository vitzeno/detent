package ui

import (
	"image/color"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/ui/island"
)

const signInURL = "https://mcp.notion.com/authorize?response_type=code&client_id=c1&state=s&code_challenge=x"

func waitingFor(server string) event.AuthorizationWaiting {
	return event.AuthorizationWaiting{Server: server, URL: signInURL,
		Until: time.Date(2026, 10, 1, 14, 32, 0, 0, time.Local)}
}

// The link appears without a key pressed: the cursor follows onto the
// new row, and the output pane draws whatever row the cursor is on.
func TestSignIn_TheLinkIsInTheOutputPaneAtOnce(t *testing.T) {
	shell := uuid.Must(uuid.NewV7())
	m := feed(t,
		event.ShellStarted{Shell: shell, Command: "ls"},
		event.ShellEnded{Shell: shell, Result: event.Result{Stdout: "a\n"}})
	m.sizeViewport()
	require.Equal(t, "ls", m.focused().command, "a row is already in focus")
	m.apply(waitingFor("notion"))
	m.refreshViewport()
	r := m.focused()
	require.NotNil(t, r)
	require.NotNil(t, r.signin, "the new row is the one in focus")

	assert.Contains(t, m.viewContent, ansi.SetHyperlink(signInURL), "a clickable link")
	assert.Contains(t, m.viewContent, "waiting until 14:32")
	assert.Contains(t, m.notice.text, "the link is in the output pane")
}

// Every pane line is cut to its width; the link must survive the cut
// whole, or what is clicked is half a URL.
func TestSignIn_TheLinkSurvivesThePaneCuttingItsLine(t *testing.T) {
	lines := signInPageLines(&signInState{server: "notion", url: signInURL}, 40)
	pane := island.Render("output", color.White, lines, 24, 20)
	assert.Contains(t, pane, ansi.SetHyperlink(signInURL))
	assert.Contains(t, pane, ansi.ResetHyperlink(), "and it is closed again")
}

func TestSignIn_SaysHowItEnded(t *testing.T) {
	m := feed(t, waitingFor("notion"), event.ServerAuthorized{Server: "notion"})
	m.sizeViewport()
	assert.Equal(t, stageSignedIn, m.focused().signin.stage)
	assert.Contains(t, strings.Join(m.rowLines(m.focused(), nil), ""), "signed in")

	m = feed(t, waitingFor("notion"), event.AuthorizationFailed{Server: "notion", Reason: "the link expired"})
	assert.Equal(t, stageFailed, m.focused().signin.stage)
	assert.Contains(t, strings.Join(signInPageLines(m.focused().signin, 80), "\n"), "the link expired")
}

// A replayed link's listener died with the process that showed it.
func TestSignIn_ALinkFromAnEarlierRunIsStale(t *testing.T) {
	m := New(t.Context(), event.New(), SessionInfo{})
	m = m.Restore([]event.Record{{Event: waitingFor("notion")}})
	r := m.focused()
	require.NotNil(t, r)
	assert.Equal(t, stageStale, r.signin.stage)
	assert.NotContains(t, strings.Join(signInPageLines(r.signin, 80), "\n"), "Sign in to notion")
}

func intent(t *testing.T, k *keyed, kind event.Kind) event.Event {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case rec := <-k.seen:
			if rec.Event.Kind() == kind {
				return rec.Event
			}
		case <-deadline:
			t.Fatalf("no %s was published", kind)
			return nil
		}
	}
}

func TestSignIn_KeysOpenCopyAndRetry(t *testing.T) {
	k := newKeyed(t)
	k.m.apply(waitingFor("notion"))
	k.m.nav.focus = focusHistory

	k.press(t, "o")
	assert.Equal(t, event.OpenAuthorization{Server: "notion"}, intent(t, k, event.OpenAuthKind))

	_, cmd, ok := k.m.signInKey(k.m.focused().signin, "c")
	assert.True(t, ok)
	assert.NotNil(t, cmd, "c hands the link to the terminal's clipboard")

	k.m.apply(event.AuthorizationFailed{Server: "notion", Reason: "stopped"})
	k.press(t, "enter")
	assert.Equal(t, event.AuthorizeServer{Server: "notion"}, intent(t, k, event.AuthorizeServerKind),
		"on a link that is over, enter asks for a new one")
}

func TestSignIn_SlashMCPAuthAsksForANewLink(t *testing.T) {
	k := newKeyed(t)
	k.m.apply(event.ServersListed{Servers: []event.ServerSummary{
		{Name: "notion", Auth: event.AuthSignedOut}, {Name: "plain"}}})

	next, cmd := k.m.listServers("/mcp auth notion")
	k.m = next.(Model)
	runCmd(cmd)
	assert.Equal(t, event.AuthorizeServer{Server: "notion"}, intent(t, k, event.AuthorizeServerKind))

	next, _ = k.m.listServers("/mcp auth plain")
	assert.Contains(t, next.(Model).notice.text, "not an MCP server with auth")
}

func TestSignIn_MCPPageSaysWhereEachStands(t *testing.T) {
	m := feed(t, event.ServersListed{Servers: []event.ServerSummary{
		{Name: "notion", Auth: event.AuthWaiting},
		{Name: "linear", Auth: event.AuthSignedOut, Err: "the link expired"},
	}})
	page := strings.Join(m.mcpLines(), "\n")
	assert.Contains(t, page, "waiting for you to sign in")
	assert.Contains(t, page, "signed out · /mcp auth linear")
}

// The row is styled before it is cut, so the cut must measure what
// shows rather than the colour codes around it.
func TestSignIn_TheRowKeepsItsWordsWhenStyled(t *testing.T) {
	m := feed(t, waitingFor("notion"))
	m.sizeViewport()
	row := ansi.Strip(strings.Join(m.rowLines(m.focused(), nil), ""))
	assert.Contains(t, row, "notion · sign in · waiting until 14:32")
}
