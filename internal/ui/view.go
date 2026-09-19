package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/vitzeno/detent/internal/editfile"
	"github.com/vitzeno/detent/internal/ui/editor"
	"github.com/vitzeno/detent/internal/ui/island"
	"github.com/vitzeno/detent/internal/ui/layout"
	"github.com/vitzeno/detent/internal/ui/markdown"
	"github.com/vitzeno/detent/internal/ui/slash"
	"github.com/vitzeno/detent/internal/ui/status"
	"github.com/vitzeno/detent/internal/ui/tabular"

	"github.com/vitzeno/detent/internal/agentloop"
)

// bodyWeights sizes the body row's two panes: output (left) gets the
// larger share — it's where a running or just-finished command's
// output actually lives — history (right) the smaller. Rebalancing or
// swapping the arrangement is a change here and in baseView's Row
// call, not a rederivation of this sizing math.
var bodyWeights = []int{3, 2} // [output, history]

// minPaneWidth is the outer-width floor below which a pane stops being
// worth rendering as its own island.
const minPaneWidth = 28

func (m *Model) sizeViewport() {
	// Bottom zone: input island content (dropdown + line + border), or
	// the self-bordered confirm box, measured not guessed — its content
	// varies with rationale and danger flags.
	bottom := m.slashRows() + 1 + 2
	switch m.mode {
	case modeConfirm:
		bottom = len(strings.Split(m.confirmBox(), "\n"))
	case modeSaveConfirm:
		bottom = len(strings.Split(m.saveConfirmBox(), "\n"))
	}
	// Fixed chrome: session bar, status line, and the body row's one
	// set of island chrome (header + border) — output and history sit
	// side by side at the same height now, so there's only one row's
	// worth to account for, not two.
	avail := m.height - 2 - islandOverhead - bottom
	if avail < 6 {
		avail = 6
	}
	m.histHeight = avail
	m.output.Height = avail

	widths := layout.Split(m.width, bodyWeights, minPaneWidth)
	m.outputColW, m.histColW = widths[0], widths[1]
	m.output.Width = paneInner(m.outputColW)
	m.refreshViewport()
}

// islandOverhead is a titled zone island's non-content lines: its
// header plus the top and bottom border.
const islandOverhead = 3

// paneInner is the content width inside an island border of the given
// outer width — matches island.Render's own inner := width-4.
func paneInner(outer int) int {
	return max(20, outer-4)
}

// slashRows is the dropdown's screen height, capped so it can't eat the history.
func (m Model) slashRows() int {
	return min(len(m.slash), slash.MaxRows)
}

func (m Model) View() string {
	return m.baseView()
}

func (m Model) baseView() string {
	if m.width <= 0 {
		return "loading…"
	}
	// Output (left, primary) and history (right, smaller) side by
	// side — see bodyWeights. Swapping or restacking this arrangement
	// is this one Row call, not a resizing rewrite.
	outputBlock := island.Render(m.viewportHeader(), m.focus == focusOutput, m.detailLines(), m.outputColW, m.output.Height+1)
	historyBlock := island.Render(m.historyHeader(), m.focus == focusHistory, m.historyWindow(), m.histColW, m.histHeight+1)

	var b strings.Builder
	b.WriteString(m.sessionBar())
	b.WriteString("\n")
	b.WriteString(layout.Row(outputBlock, historyBlock))
	b.WriteString("\n")
	b.WriteString(m.statusLine() + "\n")

	switch m.mode {
	case modeConfirm:
		b.WriteString(m.confirmBox())
	case modeSaveConfirm:
		b.WriteString(m.saveConfirmBox())
	default:
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
		if b.ended && b.tool == "" {
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
	if r.editor != nil && r.editor.Err() == nil {
		label := "file"
		if m.editing {
			label = "editing — ctrl+s save"
		}
		dirty := ""
		if r.editor.Dirty() {
			dirty = styleCaution.Render(" ●")
		}
		return fmt.Sprintf("%s %s — %s%s", paneMark(active), paneLabel(label, active),
			styleGoal.Render(truncateWidth(r.editor.Path, m.outputColW-28)), dirty)
	}
	if r.toolKind != "" {
		return fmt.Sprintf("%s %s — %s", paneMark(active), paneLabel(r.toolKind, active),
			styleGoal.Render(truncateWidth(r.command, m.outputColW-24)))
	}
	label := "output"
	if k := rowKind(r); k != "" {
		label = status.KindLabel(k)
	}
	return fmt.Sprintf("%s %s — %s", paneMark(active), paneLabel(label, active),
		styleGoal.Render(truncateWidth(r.command, m.outputColW-24)))
}

// detailLines renders the focused row's component as lines: the
// editor when this row has one — it's the whole point of a row that
// wrote a file, so it takes priority over a table or the plain
// viewport — otherwise a real table for tabular output, or the
// scrolling viewport for everything else. The island pads to height.
func (m Model) detailLines() []string {
	if r := m.focused(); r != nil {
		if r.editor != nil {
			return m.editorLines(r.editor)
		}
		switch r.toolKind {
		case "tree":
			return r.tree.View(paneInner(m.outputColW), m.output.Height)
		case "usage":
			return m.usageLines(r)
		case "help":
			return helpLines()
		}
	}
	if t, ok := m.focusedTable(); ok {
		return strings.Split(t, "\n")
	}
	return strings.Split(m.output.View(), "\n")
}

// helpLines lists every slash command from the registry — Match("/")
// with an empty suffix matches all of them, so there's no separate
// list to keep in sync with slash's own.
func helpLines() []string {
	var lines []string
	for _, c := range slash.Match("/") {
		lines = append(lines, fmt.Sprintf("  %-10s %s", c.Name, c.Desc))
	}
	return lines
}

func (m Model) editorLines(e *editor.Model) []string {
	if e.Err() != nil {
		return []string{styleDanger.Render(e.View())} // "could not open <path>: <err>"
	}
	e.Resize(paneInner(m.outputColW), m.output.Height)
	lines := strings.Split(e.View(), "\n")
	if e.Truncated() {
		lines = append(lines, styleCaution.Render(fmt.Sprintf("… file truncated at %d bytes", editfile.MaxBytes)))
	}
	return lines
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
	if m.mode == modeSaveConfirm {
		return "[y] save · [n] keep editing"
	}
	if m.editing {
		return "[ctrl+s] save · [esc] done editing"
	}
	switch m.owner() {
	case ownerConfirm:
		return "[y] run · [n] stop goal"
	case ownerOutput:
		return "[tab] input · [↑/↓] inside · [pgup/pgdn] scroll · [esc] history · [q] quit"
	case ownerHistory:
		return "[tab] output · [↑/↓] move · [space] expand · [enter] expand · [q] quit"
	case ownerBusy:
		if len(m.slash) > 0 {
			return "[↑/↓] pick · [tab] complete · [enter] run · [esc] close"
		}
		return "[tab] history · type / + enter for commands · [esc] abort"
	default: // ownerInput
		if len(m.slash) > 0 {
			return "[↑/↓] pick · [tab] complete · [enter] run · [esc] close"
		}
		return "[tab] history · [enter] run · type / for cmds"
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
