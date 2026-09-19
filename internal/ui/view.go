package ui

import (
	"fmt"
	"strings"

	"github.com/vitzeno/detent/internal/ui/markdown"
	"github.com/vitzeno/detent/internal/ui/slash"
	"github.com/vitzeno/detent/internal/ui/status"
	"github.com/vitzeno/detent/internal/ui/tabular"

	"github.com/vitzeno/detent/internal/agentloop"
)

func (m *Model) sizeViewport() {
	bottom := 3
	if m.mode == modeConfirm {
		bottom = 12
	}
	bottom += m.slashRows()
	avail := m.height - 1 - 1 - bottom - 3
	if avail < 10 {
		avail = 10
	}
	vpH := avail / 3
	if vpH < 6 {
		vpH = 6
	}
	m.histHeight = avail - vpH
	m.output.Width = m.width - 4
	m.output.Height = vpH
	m.refreshViewport()
}

// slashRows is the dropdown's screen height, capped so it can't eat the history.
func (m Model) slashRows() int {
	return min(len(m.slash), slash.MaxRows)
}

func (m Model) View() string {
	if m.width <= 0 {
		return "loading…"
	}
	var b strings.Builder
	b.WriteString(m.sessionBar())
	b.WriteString("\n")

	histLines := m.historyLines()
	// Window the history: follow the tail, or keep the cursor visible.
	start := 0
	if len(histLines) > m.histHeight {
		if m.follow {
			start = len(histLines) - m.histHeight
		} else {
			start = m.histOffset
			if m.cursorLine < start {
				start = m.cursorLine
			}
			if m.cursorLine >= start+m.histHeight {
				start = m.cursorLine - m.histHeight + 1
			}
			if start < 0 {
				start = 0
			}
		}
	}
	m.histOffset = start
	end := start + m.histHeight
	if end > len(histLines) {
		end = len(histLines)
	}
	for _, l := range histLines[start:end] {
		b.WriteString(l + "\n")
	}
	for i := end - start; i < m.histHeight; i++ {
		b.WriteString("\n")
	}

	b.WriteString(styleFaint.Render(strings.Repeat("─", max(1, m.width-2))) + "\n")
	b.WriteString(m.viewportHeader() + "\n")
	b.WriteString(m.detailView() + "\n")
	b.WriteString(m.statusLine() + "\n")

	if m.mode == modeConfirm {
		b.WriteString(m.confirmBox())
	} else {
		b.WriteString(m.inputBar())
	}
	return b.String()
}

func (m Model) sessionBar() string {
	jev := styleFaint.Render("jev ○ off")
	if m.judgeName != "" {
		jev = styleSafe.Render("jev ● " + m.judgeName)
	}
	goals := 0
	for _, b := range m.blocks {
		if b.ended {
			goals++
		}
	}
	if m.cur != nil {
		goals++
	}
	return fmt.Sprintf("%s %s · %d goal(s) · %d cmd(s)",
		styleBrand.Render("◆ detent v2"), styleFaint.Render(m.proposerName),
		goals, m.totalCmds) + "  " + jev
}

func (m Model) viewportHeader() string {
	r := m.focused()
	if r == nil {
		return styleFaint.Render("output")
	}
	label := "output"
	if k := rowKind(r); k != "" {
		label = kindLabel(k)
	}
	if m.focus == focusOutput {
		label = styleRowCursor.Render("▸ ") + label
	}
	return styleMuted.Render(label+" — ") + styleGoal.Render(truncateWidth(r.command, m.width-18))
}

func kindLabel(k string) string {
	switch k {
	case agentloop.KindTable:
		return "table"
	case agentloop.KindError:
		return "errors"
	case agentloop.KindDiff:
		return "diff"
	case agentloop.KindJSON:
		return "json"
	case agentloop.KindContent:
		return "file"
	case agentloop.KindFiles:
		return "files"
	case agentloop.KindLog:
		return "log"
	default:
		return "output"
	}
}

// detailView renders the focused row's component: a real table for
// tabular output, the scrolling viewport for everything else.
func (m Model) detailView() string {
	if content, ok := m.focusedTable(); ok {
		return content
	}
	return m.output.View()
}

// focusedTable builds the table component for a table-kind row. Falls
// back (ok=false) when the output won't parse — a wrong component is
// worse than a plain viewport.
func (m Model) focusedTable() (string, bool) {
	r := m.focused()
	src, ok := r.tableText()
	if !ok {
		return "", false
	}
	cols, rows, ok := tabular.Parse(src, m.output.Width)
	if !ok || len(rows) == 0 {
		return "", false
	}
	cursor := min(r.tableCursor, len(rows)-1)
	t := tabular.Build(cols, rows, cursor, min(len(rows)+1, m.output.Height), m.focus == focusOutput)
	return t.View(), true
}

func (m *Model) refreshViewport() {
	r := m.focused()
	if r == nil {
		m.setViewContent(styleFaint.Render("(no output yet)"))
		return
	}
	var body string
	switch {
	case r.running:
		body = strings.Join(r.live, "\n")
	case r.result != nil:
		body = m.styledBody(r)
	}
	if body == "" {
		m.setViewContent(styleFaint.Render("(no output)"))
		return
	}
	m.setViewContent(body)
	if r.running {
		m.output.GotoBottom()
	}
}

// styledBody renders a finished result per its judged kind, capped to
// maxViewportLines. Unknown kinds render raw — the classifier suggests,
// never mandates.
func (m *Model) styledBody(r *stepRow) string {
	combined := r.result.Stdout
	if r.result.Stderr != "" {
		if combined != "" && !strings.HasSuffix(combined, "\n") {
			combined += "\n"
		}
		combined += r.result.Stderr
	}
	lines := strings.Split(strings.TrimSuffix(combined, "\n"), "\n")
	switch rowKind(r) {
	case agentloop.KindError:
		for i, l := range lines {
			lines[i] = styleErrorLine(l)
		}
	case agentloop.KindDiff:
		for i, l := range lines {
			lines[i] = styleDiffLine(l)
		}
	case agentloop.KindJSON:
		if pretty, ok := prettyJSON(combined); ok {
			lines = strings.Split(pretty, "\n")
		}
	case agentloop.KindContent:
		if markdown.Wants(r.command, combined) {
			lines = strings.Split(m.markdownBody(r, combined), "\n")
		} else {
			lines = numberLines(lines)
		}
	}
	if len(lines) > maxViewportLines {
		lines = append([]string{fmt.Sprintf("… +%d earlier lines", len(lines)-maxViewportLines)}, lines[len(lines)-maxViewportLines:]...)
	}
	return strings.Join(lines, "\n")
}

// markdownBody renders through glamour, cached by width — a re-render
// per frame would churn on every scroll tick.
func (m *Model) markdownBody(r *stepRow, combined string) string {
	if r.styled == "" || r.styledWidth != m.output.Width {
		rendered, err := markdown.Render(combined, m.output.Width)
		if err != nil {
			rendered = combined
		}
		r.styled = strings.TrimSuffix(rendered, "\n")
		r.styledWidth = m.output.Width
	}
	return r.styled
}

// setViewContent skips identical content: SetContent resets the scroll
// position, so resizing for the slash dropdown must not disturb a
// viewport the user deliberately scrolled.
func (m *Model) setViewContent(s string) {
	if s == m.viewContent {
		return
	}
	m.viewContent = s
	m.output.SetContent(s)
}

func (m Model) statusLine() string {
	phase := "idle"
	if m.waiting {
		phase = "thinking…"
		for _, b := range m.blocks {
			for _, r := range b.steps {
				if r.running {
					phase = "running…"
				}
			}
		}
	}
	keys := "[tab] history · [enter] run goal · type / for commands"
	if m.waiting && m.focus == focusInput {
		keys = "[tab] history · type / + enter for commands · [esc] abort"
	}
	if len(m.slash) > 0 {
		keys = "[↑/↓] pick · [tab] complete · [enter] run · [esc] close"
	}
	if m.focus == focusHistory {
		keys = "[tab] output · [j/k] move · [space] expand · [enter] expand · [q] quit"
	}
	if m.focus == focusOutput {
		keys = "[tab] input · [j/k] inside · [pgup/pgdn] scroll · [esc] history · [q] quit"
	}
	if m.mode == modeConfirm {
		keys = "[y] run · [n] stop goal"
	}
	return status.Bar(m.spinner.View(), phase, keys, m.notice, m.waiting)
}

func (m Model) inputBar() string {
	return slash.View(m.slash, m.slashCursor) + "  " + m.input.View()
}

func truncateWidth(s string, w int) string {
	if w < 4 {
		w = 4
	}
	if len(s) <= w {
		return s
	}
	return s[:w-1] + "…"
}
