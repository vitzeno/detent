package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/vitzeno/detent/internal/ui/island"
	"github.com/vitzeno/detent/internal/ui/markdown"
	"github.com/vitzeno/detent/internal/ui/slash"
	"github.com/vitzeno/detent/internal/ui/status"
	"github.com/vitzeno/detent/internal/ui/tabular"

	"github.com/vitzeno/detent/internal/agentloop"
)

func (m *Model) sizeViewport() {
	// Bottom zone: input island content (dropdown + line + border), or
	// the self-bordered confirm box, measured not guessed — its content
	// varies with rationale and danger flags.
	bottom := m.slashRows() + 1 + 2
	if m.mode == modeConfirm {
		bottom = len(strings.Split(m.confirmBox(), "\n"))
	}
	// Fixed chrome: session bar, status line, and both islands' chrome
	// (header + border each). The rest splits between history rows and
	// viewport lines.
	avail := m.height - 2 - 2*islandOverhead - bottom
	if avail < 10 {
		avail = 10
	}
	vpH := avail / 3
	if vpH < 6 {
		vpH = 6
	}
	m.histHeight = avail - vpH
	m.output.Width = m.islandInner()
	m.output.Height = vpH
	m.refreshViewport()
}

// islandOverhead is a titled zone island's non-content lines: its
// header plus the top and bottom border.
const islandOverhead = 3

// islandInner is the content width inside an island border.
func (m Model) islandInner() int {
	return max(20, m.width-2-2)
}

// slashRows is the dropdown's screen height, capped so it can't eat the history.
func (m Model) slashRows() int {
	return min(len(m.slash), slash.MaxRows)
}

func (m Model) View() string {
	if m.showUsage {
		return m.usageOverlay()
	}
	return m.baseView()
}

func (m Model) baseView() string {
	if m.width <= 0 {
		return "loading…"
	}
	var b strings.Builder
	b.WriteString(m.sessionBar())
	b.WriteString("\n")
	b.WriteString(island.Render(m.historyHeader(), m.focus == focusHistory, m.historyWindow(), m.width, m.histHeight+1))
	b.WriteString("\n")
	b.WriteString(island.Render(m.viewportHeader(), m.focus == focusOutput, m.detailLines(), m.width, m.output.Height+1))
	b.WriteString("\n")
	b.WriteString(m.statusLine() + "\n")

	if m.mode == modeConfirm {
		b.WriteString(m.confirmBox())
	} else {
		b.WriteString(island.Render("", m.focus == focusInput, strings.Split(m.inputBar(), "\n"), m.width, m.slashRows()+1))
	}
	return b.String()
}

// historyWindow returns the visible history slice; the island pads
// short content. Entries flatten to lines first: expanded rows and
// two-line banners span several lines each, and windowing entries
// instead would let the island grow past the terminal height and push
// the session bar off the top.
func (m Model) historyWindow() []string {
	histLines := m.historyLines()
	var lines []string
	cursorLine := 0
	for i, e := range histLines {
		if i == m.cursorLine {
			cursorLine = len(lines)
		}
		lines = append(lines, strings.Split(e, "\n")...)
	}
	m.cursorLine = cursorLine
	start := 0
	if len(lines) > m.histHeight {
		if m.follow {
			start = len(lines) - m.histHeight
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
	if end > len(lines) {
		end = len(lines)
	}
	return append([]string(nil), lines[start:end]...)
}

// paneMark is the active/inactive marker shared by every zone header:
// a filled accent dot for the focused pane, a faint ring otherwise.
func paneMark(active bool) string {
	if active {
		return styleBrand.Render("●")
	}
	return styleFaint.Render("○")
}

func (m Model) historyHeader() string {
	return fmt.Sprintf("%s %s", paneMark(m.focus == focusHistory), paneLabel("history", m.focus == focusHistory))
}

func paneLabel(name string, active bool) string {
	if active {
		return styleBrand.Render(name)
	}
	return styleFaint.Render(name)
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
	snap := m.sess.Tracker().Snapshot()
	usage := styleFaint.Render(fmt.Sprintf("⏱ %s · %stok",
		status.Dur(snap.MachineTime()), status.Tokens(snap.ProposerTokens+snap.JudgeTokens)))
	return fmt.Sprintf("%s %s · %d goal(s) · %d cmd(s)  %s  %s",
		styleBrand.Render("◆ detent v2"), styleFaint.Render(m.proposerName),
		goals, m.totalCmds, usage, jev)
}

func (m Model) viewportHeader() string {
	active := m.focus == focusOutput
	r := m.focused()
	if r == nil {
		return fmt.Sprintf("%s %s", paneMark(active), paneLabel("output", active))
	}
	label := "output"
	if k := rowKind(r); k != "" {
		label = status.KindLabel(k)
	}
	return fmt.Sprintf("%s %s — %s", paneMark(active), paneLabel(label, active),
		styleGoal.Render(truncateWidth(r.command, m.width-24)))
}

// detailLines renders the focused row's component as lines: a real
// table for tabular output, the scrolling viewport for everything
// else. The island pads to height.
func (m Model) detailLines() []string {
	if t, ok := m.focusedTable(); ok {
		return strings.Split(t, "\n")
	}
	return strings.Split(m.output.View(), "\n")
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
	t0 := time.Now()
	defer func() {
		m.uiPrep += time.Since(t0)
		m.uiPreps++
	}()
	r := m.focused()
	if r == nil {
		m.setViewContent(styleFaint.Render("(no output yet)"))
		return
	}
	var body string
	switch {
	case r.running:
		body = strings.Join(r.live, "\n")
	case r.ec != nil:
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
	combined := r.ec.Result.Stdout
	if r.ec.Result.Stderr != "" {
		if combined != "" && !strings.HasSuffix(combined, "\n") {
			combined += "\n"
		}
		combined += r.ec.Result.Stderr
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
	return status.Bar(m.spinner.View(), phase, m.statusHint(), m.notice, m.waiting)
}

// statusHint picks the key hint for the current owner — the same
// question handleKey's owner() answers for routing, asked again here
// for display, so the two never fall out of sync on which state wins.
func (m Model) statusHint() string {
	switch m.owner() {
	case ownerConfirm:
		return "[y] run · [n] stop goal"
	case ownerOutput:
		return "[tab] input · [j/k] inside · [pgup/pgdn] scroll · [esc] history · [q] quit"
	case ownerHistory:
		return "[tab] output · [j/k] move · [space] expand · [enter] expand · [q] quit"
	case ownerBusy:
		if len(m.slash) > 0 {
			return "[↑/↓] pick · [tab] complete · [enter] run · [esc] close"
		}
		return "[tab] history · type / + enter for commands · [esc] abort"
	default: // ownerInput
		if len(m.slash) > 0 {
			return "[↑/↓] pick · [tab] complete · [enter] run · [esc] close"
		}
		return "[tab] history · [↑/↓] rows · [enter] run · type / for cmds"
	}
}

func (m Model) inputBar() string {
	active := m.focus == focusInput
	m.input.PromptStyle = styleFaint
	if active {
		m.input.PromptStyle = styleRowCursor
	}
	return fmt.Sprintf("%s %s%s", paneMark(active), slash.View(m.slash, m.slashCursor), m.input.View())
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
