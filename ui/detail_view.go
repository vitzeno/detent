package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/vitzeno/detent/ui/editor"
	"github.com/vitzeno/detent/ui/layout"
	"github.com/vitzeno/detent/ui/markdown"
	"github.com/vitzeno/detent/ui/render"
	"github.com/vitzeno/detent/ui/tabular"
	"github.com/vitzeno/detent/ui/welcome"
)

// The output pane: what the focused row's component renders into
// it, and the viewport refresh that keeps it in step.

// detailLines renders the focused row's component: editor takes
// priority when present, then a table for tabular output, else the
// scrolling viewport.
func (m Model) detailLines() []string {
	// A pending rollback takes the pane: its file list is the thing
	// being reviewed, and it can run to hundreds of paths. The confirm
	// box below stays small and asks the question.
	if m.mode == modeRollbackConfirm {
		return strings.Split(m.output.View(), "\n")
	}
	if m.showWelcome() {
		return m.welcomePane()
	}
	if r := m.focused(); r != nil {
		if r.editor != nil {
			return m.editorLines(r.editor)
		}
		switch r.toolKind {
		case "tree":
			return r.tool.tree.View(paneInner(m.layout.outputColW), m.output.Height())
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
	for _, c := range matchSlash("/") {
		lines = append(lines, fmt.Sprintf("  %-10s %s", c.Name, c.Desc))
	}
	return lines
}

func (m Model) editorLines(e *editor.Model) []string {
	if e.Err() != nil {
		return []string{styleDanger.Render(e.View())} // "could not open <path>: <err>"
	}
	e.Resize(paneInner(m.layout.outputColW), m.output.Height())
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
	cols, rows, ok := tabular.Parse(src, m.output.Width())
	if !ok || len(rows) == 0 {
		return "", false
	}
	cursor := min(r.cmd.tableCursor, len(rows)-1)
	t := tabular.Build(cols, rows, cursor, m.output.Height(), m.output.Width(), m.nav.focus == focusOutput)
	return t.View(), true
}

func (m *Model) refreshViewport() {
	t0 := time.Now()
	defer func() {
		m.perf.uiPrep += time.Since(t0)
		m.perf.uiPreps++
	}()
	_, m.nav.histOffset = m.historyWindow()
	if m.mode == modeRollbackConfirm {
		m.setViewContent(m.rollbackFileLines())
		return
	}
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
			lines[i] = render.ErrorLine(l)
		}
	case KindDiff:
		for i, l := range lines {
			lines[i] = render.DiffLine(l)
		}
	case KindJSON:
		if pretty, ok := render.JSON(combined); ok {
			lines = strings.Split(pretty, "\n")
		}
	case KindContent:
		if markdown.Wants(r.command, combined) {
			lines = strings.Split(m.markdownBody(r, combined), "\n")
		} else {
			lines = render.NumberLines(lines)
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
	if r.cmd.styled == "" || r.cmd.styledWidth != m.output.Width() {
		rendered, err := markdown.Render(combined, m.output.Width())
		if err != nil {
			rendered = combined
		}
		r.cmd.styled = strings.TrimSuffix(rendered, "\n")
		r.cmd.styledWidth = m.output.Width()
	}
	return r.cmd.styled
}

// welcomePane hands the boot pane the facts it reports, so nothing in
// ui/welcome reaches into Model.
func (m Model) welcomePane() []string {
	snap := m.sess.UsageSnapshot()
	return welcome.Lines(welcome.Facts{
		Proposer: m.info.Proposer, Judge: m.info.Judge, RunMode: m.info.RunMode,
		Image: m.info.Image, Mount: m.info.Mount,
		Runtime: m.info.Runtime, Network: m.info.Network,
		Goals: snap.Goals, Commands: snap.Commands, MachineTime: snap.MachineTime(),
	}, paneInner(m.layout.outputColW), m.output.Height(), m.welcomeFrame)
}

// rollbackFileLines is every path a revert would touch, in full. The
// viewport scrolls it, so a goal that rewrote a whole tree is still
// reviewable rather than cut off at an arbitrary count.
func (m Model) rollbackFileLines() string {
	width := paneInner(m.layout.outputColW)
	var b strings.Builder
	for i, f := range m.rollback.files {
		if i > 0 {
			b.WriteString("\n")
		}
		mark, style := "restore", styleDiffAdd
		if f.Removed {
			mark, style = "delete ", styleDiffDel
		}
		note := ""
		if f.Unseen {
			note = styleDanger.Render("  ⚠ not detent's")
		}
		b.WriteString(" " + style.Render(mark) + " " +
			styleGoal.Render(layout.Truncate(f.Path, width-24)) + note)
	}
	return b.String()
}

// showWelcome reports whether the output pane has nothing of its own
// to show yet, which is the boot state: no row has ever been focused.
func (m Model) showWelcome() bool {
	return m.focused() == nil
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
