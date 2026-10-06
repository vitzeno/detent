package ui

import (
	"fmt"
	"time"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/ui/island"
	"github.com/vitzeno/detent/ui/layout"
)

// The resume picker's box: stored sessions beside the selected one's history.

func (p *resumeModal) box(m Model) string {
	left, right := m.resumePaneWidths()
	h := m.modalPaneHeight()
	body := m.modalPanes(left, right,
		modalPane{title: "sessions", lines: p.listLines(m, left-4, h), focused: !p.previewFocused},
		modalPane{title: "history", lines: p.previewLines(m, right, h), focused: p.previewFocused})
	k := keymap.resume
	return m.modalBox(m.resumeTitle(), body, boxLine(does("resume", k.resume),
		does("move", k.move.up, k.move.down), does("pane", k.pane), does("back", k.close),
		does("top/end", k.move.top, k.move.bottom)))
}

func (m Model) resumeTitle() string {
	title := styleBrand.Render("resume") + styleFaint.Render(" · "+countOf(len(m.sessions), "session"))
	if m.cur != nil {
		title += styleCaution.Render(" · a request is running, it must end first")
	}
	return title
}

// ageWidth fits every age the picker writes, "59m ago" to "Aug 2026".
const ageWidth = 9

// listLines is each session by name, or id until named, with how long ago
// it was last used and created: ages, so a narrow pane still has room for names.
func (p *resumeModal) listLines(m Model, width, height int) []string {
	if len(m.sessions) == 0 {
		return []string{styleFaint.Render("nothing recorded yet")}
	}
	room := max(1, width-2-2*(ageWidth+1))
	cell := func(s string, n int) string { return fmt.Sprintf("%-*s", n, layout.Truncate(s, n)) }
	out := []string{styleFaint.Render("  " + cell("session", room) + " " + cell("last used", ageWidth) +
		" " + cell("created", ageWidth))}
	start, end := listWindow(len(m.sessions), p.cursor, height-1)
	for i := start; i < end; i++ {
		s := m.sessions[i]
		mark, style := "  ", styleGoal
		if i == p.cursor {
			mark, style = "▸ ", styleRowCursor
		}
		label := s.Name
		if label == "" {
			label = s.ID.String()[:8]
		}
		out = append(out, style.Render(mark+cell(label, room))+" "+
			styleFaint.Render(cell(m.usedAgo(s), ageWidth)+" "+ago(s.Started)))
	}
	return out
}

// usedAgo is how long ago s was last used, or now for this session, which still is.
func (m Model) usedAgo(s event.SessionSummary) string {
	if s.ID == m.run.Session {
		return "now"
	}
	return ago(used(s))
}

// lastUsed is usedAgo as a date, for a page with room for one.
func (m Model) lastUsed(s event.SessionSummary, format string) string {
	if s.ID == m.run.Session {
		return "now"
	}
	return used(s).Local().Format(format)
}

// used is when s was last used. A listing stored before it was said leaves it
// zero, and a session is used at least when it starts.
func used(s event.SessionSummary) time.Time {
	if s.Used.IsZero() {
		return s.Started
	}
	return s.Used
}

// ago is how long before now t was, in its largest whole unit, or the month
// once it is too long ago for a count to mean much.
func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return t.Local().Format("Jan 2006")
}

// resumePaneWidths gives the history the wider pane, since it is what is read,
// less the two dates.
func (m Model) resumePaneWidths() (left, right int) {
	inner := island.Inner(m.modalWidth())
	left = inner * 9 / 20
	return left, inner - left
}

// previewLines is the selected session's history as the history pane
// draws it, from its newest line up by the scroll.
func (p *resumeModal) previewLines(m Model, paneWidth, height int) []string {
	s := p.selected(m)
	if s == nil {
		return nil
	}
	pv := p.loaded[s.ID]
	switch {
	case pv == nil || pv.model == nil && pv.err == "":
		return []string{styleFaint.Render("loading…")}
	case pv.err != "":
		return []string{styleDanger.Render(layout.Truncate(pv.err, paneWidth-4))}
	}
	lines := p.history(m, paneWidth)
	end := max(0, len(lines)-min(p.scroll, max(0, len(lines)-height)))
	return lines[max(0, end-height):end]
}

// history is every line of the selected session's history, nil until loaded.
func (p *resumeModal) history(m Model, paneWidth int) []string {
	s := p.selected(m)
	if s == nil {
		return nil
	}
	pv := p.loaded[s.ID]
	if pv == nil || pv.model == nil {
		return nil
	}
	// blockWidth fits rows to the history pane, so it is handed this one's width.
	pv.model.layout.histColW = paneWidth + railWidth
	lines, _ := pv.model.historyAll()
	return lines
}
