package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/vitzeno/detent/internal/ui/editor"
	"github.com/vitzeno/detent/internal/ui/island"
	"github.com/vitzeno/detent/internal/ui/layout"
	"github.com/vitzeno/detent/internal/ui/markdown"
	"github.com/vitzeno/detent/internal/ui/slash"
	"github.com/vitzeno/detent/internal/ui/status"
	"github.com/vitzeno/detent/internal/ui/tabular"
)

var bodyWeights = []int{3, 2} // [output, history]; output gets the larger share

// minPaneWidth is the outer-width floor below which a pane stops being
// worth rendering as its own island.
const minPaneWidth = 28

func (m *Model) sizeViewport() {
	// Bottom zone height is measured, not guessed — content varies with
	// rationale and danger flags.
	bottom := m.slashRows() + 1 + 2
	switch m.mode {
	case modeConfirm:
		bottom = len(strings.Split(m.confirmBox(), "\n"))
	case modeSaveConfirm:
		bottom = len(strings.Split(m.saveConfirmBox(), "\n"))
	}
	avail := m.layout.height - 2 - islandOverhead - bottom
	if avail < 6 {
		avail = 6
	}
	m.nav.histHeight = avail
	m.output.Height = avail

	widths := layout.Split(m.layout.width, bodyWeights, minPaneWidth)
	m.layout.outputColW, m.layout.histColW = widths[0], widths[1]
	m.output.Width = paneInner(m.layout.outputColW)
	m.refreshViewport()
}

// islandOverhead is a titled zone island's non-content lines: header
// plus top and bottom border.
const islandOverhead = 3

// paneInner matches island.Render's own inner := width-4.
func paneInner(outer int) int {
	return max(20, outer-4)
}

// slashRows caps the dropdown so it can't eat the history pane.
func (m Model) slashRows() int {
	return min(len(m.slash.matches), slash.MaxRows)
}

func (m Model) View() string {
	return m.baseView()
}

func (m Model) baseView() string {
	if m.layout.width <= 0 {
		return "loading…"
	}
	outputBlock := island.Render(m.viewportHeader(), m.nav.focus == focusOutput, m.detailLines(), m.layout.outputColW, m.output.Height+1)
	historyBlock := island.Render(m.historyHeader(), m.nav.focus == focusHistory, m.nav.histWindow, m.layout.histColW, m.nav.histHeight+1)

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
		b.WriteString(island.Render("", m.nav.focus == focusInput, strings.Split(m.inputBar(), "\n"), m.layout.width, m.slashRows()+1))
	}
	return b.String()
}

// updateHistoryWindow recomputes which history lines are visible and
// caches the result on nav.histWindow. Must run via refreshViewport (a
// pointer receiver), not inside View()'s call chain — Bubble Tea always
// renders View() on a throwaway copy of Model, so writes there never
// persist. Entries flatten to lines first since expanded rows/banners
// span several lines each.
func (m *Model) updateHistoryWindow() {
	histLines := m.historyLines() // also sets nav.cursorLine
	var lines []string
	cursorLine := 0
	for i, e := range histLines {
		if i == m.nav.cursorLine {
			cursorLine = len(lines)
		}
		lines = append(lines, strings.Split(e, "\n")...)
	}
	m.nav.cursorLine = cursorLine
	start := 0
	if len(lines) > m.nav.histHeight {
		if m.nav.follow {
			start = len(lines) - m.nav.histHeight
		} else {
			start = m.nav.histOffset
			if m.nav.cursorLine < start {
				start = m.nav.cursorLine
			}
			if m.nav.cursorLine >= start+m.nav.histHeight {
				start = m.nav.cursorLine - m.nav.histHeight + 1
			}
			if start < 0 {
				start = 0
			}
		}
	}
	m.nav.histOffset = start
	end := start + m.nav.histHeight
	if end > len(lines) {
		end = len(lines)
	}
	m.nav.histWindow = append([]string(nil), lines[start:end]...)
}

func paneMark(active bool) string {
	if active {
		return styleBrand.Render("●")
	}
	return styleFaint.Render("○")
}

func (m Model) historyHeader() string {
	return fmt.Sprintf("%s %s", paneMark(m.nav.focus == focusHistory), paneLabel("history", m.nav.focus == focusHistory))
}

func paneLabel(name string, active bool) string {
	if active {
		return styleBrand.Render(name)
	}
	return styleFaint.Render(name)
}

// runModeBadge says where commands actually run. Host is called out
// rather than left implicit: it's the unsandboxed case.
func runModeBadge(mode string) string {
	if mode == "sandbox" {
		return styleSafe.Render("sandbox ●")
	}
	return styleCaution.Render("host ⚠ unsandboxed")
}

func (m Model) sessionBar() string {
	jev := styleFaint.Render("jev ○ off")
	if m.info.Judge != "" {
		jev = styleSafe.Render("jev ● " + m.info.Judge)
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
	snap := m.sess.UsageSnapshot()
	usage := styleFaint.Render(fmt.Sprintf("⏱ %s · %stok",
		status.Dur(snap.MachineTime()), status.Tokens(snap.ProposerTokens+snap.JudgeTokens)))
	return fmt.Sprintf("%s %s · %d goal(s) · %d cmd(s)  %s  %s  %s",
		styleBrand.Render("◆ detent v2"), styleFaint.Render(m.info.Proposer),
		goals, m.totalCmds, usage, runModeBadge(m.info.RunMode), jev)
}

func (m Model) viewportHeader() string {
	active := m.nav.focus == focusOutput
	r := m.focused()
	if r == nil {
		return fmt.Sprintf("%s %s", paneMark(active), paneLabel("output", active))
	}
	if r.editor != nil && r.editor.Err() == nil {
		label := "file"
		if m.save.editing {
			label = "editing — ctrl+s save"
		}
		dirty := ""
		if r.editor.Dirty() {
			dirty = styleCaution.Render(" ●")
		}
		return fmt.Sprintf("%s %s — %s%s", paneMark(active), paneLabel(label, active),
			styleGoal.Render(truncateWidth(r.editor.Path, m.layout.outputColW-28)), dirty)
	}
	if r.toolKind != "" {
		return fmt.Sprintf("%s %s — %s", paneMark(active), paneLabel(r.toolKind, active),
			styleGoal.Render(truncateWidth(r.command, m.layout.outputColW-24)))
	}
	label := "output"
	if k := rowKind(r); k != "" {
		label = status.KindLabel(string(k))
	}
	return fmt.Sprintf("%s %s — %s", paneMark(active), paneLabel(label, active),
		styleGoal.Render(truncateWidth(r.command, m.layout.outputColW-24)))
}

// detailLines renders the focused row's component: editor takes
// priority when present, then a table for tabular output, else the
// scrolling viewport.
func (m Model) detailLines() []string {
	if r := m.focused(); r != nil {
		if r.editor != nil {
			return m.editorLines(r.editor)
		}
		switch r.toolKind {
		case "tree":
			return r.tool.tree.View(paneInner(m.layout.outputColW), m.output.Height)
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

// helpLines lists every slash command via Match("/") (empty suffix
// matches all), so there's no separate list to keep in sync.
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
	e.Resize(paneInner(m.layout.outputColW), m.output.Height)
	lines := strings.Split(e.View(), "\n")
	if e.Truncated() {
		lines = append(lines, styleCaution.Render(fmt.Sprintf("… file truncated at %d bytes", e.MaxBytes())))
	}
	return lines
}

// focusedTable falls back (ok=false) when the output won't parse — a
// wrong component is worse than a plain viewport.
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
	cursor := min(r.cmd.tableCursor, len(rows)-1)
	t := tabular.Build(cols, rows, cursor, min(len(rows)+1, m.output.Height), m.nav.focus == focusOutput)
	return t.View(), true
}

func (m *Model) refreshViewport() {
	t0 := time.Now()
	defer func() {
		m.perf.uiPrep += time.Since(t0)
		m.perf.uiPreps++
	}()
	m.updateHistoryWindow()
	r := m.focused()
	if r == nil {
		m.setViewContent(styleFaint.Render("(no output yet)"))
		return
	}
	var body string
	switch {
	case r.cmd.running:
		body = strings.Join(r.cmd.live, "\n")
	case r.cmd.ec != nil:
		body = m.styledBody(r)
	}
	if body == "" {
		m.setViewContent(styleFaint.Render("(no output)"))
		return
	}
	m.setViewContent(body)
	if r.cmd.running {
		m.output.GotoBottom()
	}
}

// styledBody renders per judged kind, capped to maxViewportLines.
// Unknown kinds render raw.
func (m *Model) styledBody(r *stepRow) string {
	combined := r.cmd.ec.Result.Stdout
	if r.cmd.ec.Result.Stderr != "" {
		if combined != "" && !strings.HasSuffix(combined, "\n") {
			combined += "\n"
		}
		combined += r.cmd.ec.Result.Stderr
	}
	lines := strings.Split(strings.TrimSuffix(combined, "\n"), "\n")
	switch rowKind(r) {
	case KindError:
		for i, l := range lines {
			lines[i] = styleErrorLine(l)
		}
	case KindDiff:
		for i, l := range lines {
			lines[i] = styleDiffLine(l)
		}
	case KindJSON:
		if pretty, ok := prettyJSON(combined); ok {
			lines = strings.Split(pretty, "\n")
		}
	case KindContent:
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

// markdownBody caches by width — a re-render per frame would churn on
// every scroll tick.
func (m *Model) markdownBody(r *stepRow, combined string) string {
	if r.cmd.styled == "" || r.cmd.styledWidth != m.output.Width {
		rendered, err := markdown.Render(combined, m.output.Width)
		if err != nil {
			rendered = combined
		}
		r.cmd.styled = strings.TrimSuffix(rendered, "\n")
		r.cmd.styledWidth = m.output.Width
	}
	return r.cmd.styled
}

// setViewContent skips identical content: SetContent resets scroll
// position, which must not happen just from resizing for the dropdown.
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
				if r.cmd.running {
					phase = "running…"
				}
			}
		}
	}
	return status.Bar(m.spinner.View(), phase, m.statusHint(), m.notice, m.waiting)
}

// statusHint mirrors handleKey's owner() so the hint never falls out of
// sync with what actually routes the keystroke.
func (m Model) statusHint() string {
	if m.mode == modeSaveConfirm {
		return "[y/enter] save · [n] keep editing"
	}
	if m.save.editing {
		return "[ctrl+s] save · [esc] done editing"
	}
	switch m.owner() {
	case ownerConfirm:
		return "[y/enter] run · [n] stop goal"
	case ownerOutput:
		// Must match onEscape's actual behavior (keys.go): it aborts a
		// running command here instead of stepping back to history.
		esc := "[esc] history"
		if m.abort != nil {
			esc = "[esc] abort"
		}
		return "[tab] input · [↑/↓] inside · " + esc + " · [q] quit"
	case ownerHistory:
		return "[tab] output · [↑/↓] move · [space] expand · [enter] expand · [q] quit"
	case ownerBusy:
		if len(m.slash.matches) > 0 {
			return "[↑/↓] pick · [tab] complete · [enter] run · [esc] close"
		}
		return "[tab] history · type / + enter for commands · [esc] abort"
	default: // ownerInput
		if len(m.slash.matches) > 0 {
			return "[↑/↓] pick · [tab] complete · [enter] run · [esc] close"
		}
		return "[tab] history · [enter] run · type / for cmds"
	}
}

func (m Model) inputBar() string {
	active := m.nav.focus == focusInput
	m.input.PromptStyle = styleFaint
	if active {
		m.input.PromptStyle = styleRowCursor
	}
	// The dropdown is multi-line, so it goes first: prefixing it with
	// the pane mark would indent only its first row and strand the
	// mark away from the prompt it belongs to.
	// The dropdown is multi-line, so it goes first: prefixing it with
	// the pane mark would indent only its first row and strand the
	// mark away from the prompt it belongs to.
	return fmt.Sprintf("%s%s %s", slash.View(m.slash.matches, m.slash.cursor), paneMark(active), m.input.View())
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
