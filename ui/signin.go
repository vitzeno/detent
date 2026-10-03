package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
)

// signInState is one sign-in a server asked for, and how far it got.
// It ran nothing, so it has no badge, no output and no Turn of its own.
type signInState struct {
	server string
	url    string
	until  time.Time
	stage  signInStage
	reason string
}

// signInStage is where a sign-in stands.
type signInStage int

const (
	stageWaiting signInStage = iota
	stageSignedIn
	stageFailed
	// stageStale is a link from an earlier run, whose listener is gone.
	stageStale
)

// addSignIn puts the row where a Shell would go: in the Turn a Call got
// its 401 in, or between Turns for one asked at startup.
func (m *Model) addSignIn(v event.AuthorizationWaiting) {
	b := m.cur
	if b == nil {
		b = m.shellBlock()
	}
	b.rev++
	b.rows = append(b.rows, &callRow{
		id: uuid.Must(uuid.NewV7()), command: v.Server,
		signin: &signInState{server: v.Server, url: v.URL, until: v.Until},
	})
	m.trackNewest()
	// Following, the cursor is on it now, so the pane already shows the link.
	m.noteOK(v.Server + " wants you to sign in · the link is in the output pane")
}

// signInFor is the newest row still waiting on server's sign-in.
func (m *Model) signInFor(server string) *signInState {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		rows := m.blocks[i].rows
		for j := len(rows) - 1; j >= 0; j-- {
			if s := rows[j].signin; s != nil && s.server == server && s.stage == stageWaiting {
				m.blocks[i].rev++
				return s
			}
		}
	}
	return nil
}

// staleSignIns marks what a replay left waiting: its listener died
// with the process that published it, so the link leads nowhere.
func (m *Model) staleSignIns() {
	for _, b := range m.blocks {
		for _, r := range b.rows {
			if r.signin != nil && r.signin.stage == stageWaiting {
				r.signin.stage = stageStale
				b.rev++
				m.histRev++
			}
		}
	}
}

// signInRowLines is the row in history.
func (m Model) signInRowLines(mark string, s *signInState) []string {
	glyph, text := styleCaution.Render("⚿"), "sign in"
	switch s.stage {
	case stageWaiting:
		text += styleMuted.Render(" · waiting until " + s.until.Format("15:04"))
	case stageSignedIn:
		glyph, text = styleSafe.Render("⚿"), "signed in"
	case stageFailed:
		glyph, text = styleDanger.Render("⚿"), "sign-in failed"+styleMuted.Render(" · "+s.reason)
	case stageStale:
		glyph, text = styleFaint.Render("⚿"), styleFaint.Render("sign-in link from an earlier run")
	}
	// Cut by what shows: the line is already styled.
	line := mark + glyph + " " + styleGoal.Render(s.server) + " · " + text
	return []string{ansi.Truncate(line, m.blockWidth(), "…")}
}

// signInPageLines is what the output pane shows for the row: a hyperlink
// with short text, since the URL runs to hundreds of characters.
func signInPageLines(s *signInState, width int) []string {
	retry := styleMuted.Render("/mcp auth " + s.server + " for a new link")
	switch s.stage {
	case stageSignedIn:
		return []string{styleSafe.Render(s.server + " is signed in"), "",
			styleMuted.Render("its tools are offered from the next step")}
	case stageFailed:
		return []string{styleDanger.Render("the sign-in for " + s.server + " did not finish"), "",
			styleMuted.Render(s.reason), "", retry}
	case stageStale:
		return []string{styleFaint.Render("this link is from an earlier run, and nothing is listening for it now"),
			"", retry}
	case stageWaiting:
		// Drawn below, the one stage that asks for something.
	}
	link := lipgloss.NewStyle().Foreground(accent).Bold(true).Hyperlink(s.url).Render("Sign in to " + s.server + " ↗")
	out := []string{styleGoal.Render(s.server + " wants you to sign in"), "", "  " + link, "",
		styleMuted.Render("  enter or o  open it in your browser"),
		styleMuted.Render("  c           copy the link"), "",
		styleMuted.Render("  waiting until " + s.until.Format("15:04")), ""}
	// Whole, for a terminal that draws no hyperlink. c copies it cleanly.
	for _, chunk := range chunks(s.url, max(width, 20)) {
		out = append(out, styleFaint.Render(chunk))
	}
	return out
}

// signInKey is what enter, o and c do on a sign-in row. ok is false
// for any other key, which the pane handles as usual.
func (m Model) signInKey(s *signInState, key string) (tea.Model, tea.Cmd, bool) {
	switch key {
	case "enter", "o":
		switch s.stage {
		case stageWaiting:
			return m, m.send(event.OpenAuthorization{Server: s.server}), true
		case stageSignedIn:
			// Asking again would throw a working token away.
			m.noteOK(s.server + " is already signed in · /mcp auth " + s.server + " signs in again")
			return m, nil, true
		case stageFailed, stageStale:
			m.noteOK("asking " + s.server + " for a new link")
			return m, m.send(event.AuthorizeServer{Server: s.server}), true
		}
	case "c":
		if s.stage != stageWaiting {
			return m, nil, false
		}
		m.noteOK("link copied")
		return m, tea.SetClipboard(s.url), true
	}
	return m, nil, false
}

// chunks cuts s every n runes, for a URL that has nowhere to wrap.
func chunks(s string, n int) []string {
	var out []string
	r := []rune(s)
	for len(r) > n {
		out = append(out, string(r[:n]))
		r = r[n:]
	}
	return append(out, string(r))
}
