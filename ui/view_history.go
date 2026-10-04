package ui

import (
	"fmt"
	"maps"
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/ui/layout"
	"github.com/vitzeno/detent/ui/status"
)

// The history pane: one block per request, its calls under it. View
// calls this on a throwaway copy of Model, so nothing but the caches may write back.

// railWidth is the gutter the rail and its space occupy.
const railWidth = 2

// contPrefix indents a wrapped line's continuation so it reads as one
// paragraph under its label, not flush against the pane edge.
const contPrefix = "    "

// historyPaneLines is what sizeViewport laid out. The fallback is the first
// frame, which lands before any Update.
func (m Model) historyPaneLines() []string {
	if m.nav.histWindow != nil {
		return m.nav.histWindow
	}
	window, _ := m.historyWindow()
	return window
}

// historyWindow returns the visible slice and the offset it settled
// on. Pure, so sizeViewport can call it before anything is committed.
func (m Model) historyWindow() (window []string, offset int) {
	// Only the tail is on screen, so only the tail is drawn. Offset is
	// -1 because no total was counted, and navUp pins one before it matters.
	if m.nav.follow {
		lines := m.historyTail(m.nav.histHeight)
		if len(lines) > m.nav.histHeight {
			lines = lines[len(lines)-m.nav.histHeight:]
		}
		return lines, -1
	}

	lines, cursorLine := m.historyAll()
	start := 0
	if len(lines) > m.nav.histHeight {
		start = m.nav.histOffset
		start = min(start, cursorLine)
		if cursorLine >= start+m.nav.histHeight {
			start = cursorLine - m.nav.histHeight + 1
		}
		start = max(start, 0)
	}
	return lines[start:min(start+m.nav.histHeight, len(lines))], start
}

// historyAll is every line and the cursor's, cached whole since Update
// wants it for every message once the pane has scrolled off the end.
func (m Model) historyAll() (lines []string, cursorLine int) {
	key := histKey{
		rev: m.histRev, width: m.blockWidth(),
		cursor: m.nav.cursor, spinner: m.spinnerFrame(),
	}
	if m.hist != nil && m.hist.lines != nil && m.hist.key == key {
		return m.hist.lines, m.hist.cursorLine
	}
	lines, cursorLine = m.historyLines()
	if m.hist != nil {
		*m.hist = histCache{key: key, lines: lines, cursorLine: cursorLine}
	}
	return lines, cursorLine
}

// historyLines renders every entry and reports which one the cursor is
// on. Pure, so sizeViewport can call it before anything is committed.
func (m Model) historyLines() (lines []string, cursorEntry int) {
	focused := m.focusedRow()
	for bi, b := range m.blocks {
		if bi > 0 {
			lines = append(lines, "")
		}
		block, at := m.blockLines(b, focused)
		if at >= 0 {
			cursorEntry = len(lines) + at
		}
		lines = append(lines, block...)
	}
	if len(lines) == 0 {
		lines = append(lines, styleFaint.Render("  nothing yet — ask for something below"))
	}
	return lines, cursorEntry
}

// historyTail renders backwards from the newest block until the pane
// is full, so cost follows what is on screen, not session length.
func (m Model) historyTail(height int) []string {
	focused := m.focusedRow()
	var lines []string
	for bi := len(m.blocks) - 1; bi >= 0; bi-- {
		block, _ := m.blockLines(m.blocks[bi], focused)
		if bi > 0 {
			lines = join([]string{""}, block, lines)
		} else {
			lines = join(block, lines)
		}
		if len(lines) >= height {
			break
		}
	}
	if len(lines) == 0 {
		lines = append(lines, styleFaint.Render("  nothing yet — ask for something below"))
	}
	return lines
}

// focusedRow is the row the cursor is on, or nil. Resolved once and
// passed down: asking per row rebuilds the whole row list each time.
func (m Model) focusedRow() *historyRow {
	rows := m.rows()
	if len(rows) == 0 {
		return nil
	}
	return rows[min(max(m.nav.cursor, 0), len(rows)-1)]
}

// spinnerFrame is the frame a live block is drawing, or "" when none
// is. The whole history turns over with it, as blockKey does per block.
func (m Model) spinnerFrame() string {
	for _, b := range m.blocks {
		if !b.ended && (anyRunning(b) || (b == m.cur && m.waiting)) {
			return m.spinner.View()
		}
	}
	return ""
}

// blockLines is the cached front of drawBlock. Scrolling moves only
// the cursor, so every block but the two it touched is a hit.
func (m Model) blockLines(b *turnBlock, focused *historyRow) (lines []string, cursorAt int) {
	key := m.blockKey(b, focused)
	if b.cache != nil && b.cache.key == key {
		return b.cache.lines, b.cache.cursorAt
	}
	lines, cursorAt = m.drawBlock(b, key.focused)
	b.cache = &blockCache{key: key, lines: lines, cursorAt: cursorAt}
	return lines, cursorAt
}

// drawBlock renders one block against its rail, reporting where the
// cursor landed inside it or -1 when it is elsewhere.
func (m Model) drawBlock(b *turnBlock, focused *historyRow) (lines []string, cursorAt int) {
	cursorAt = -1
	var block []string
	// Split here, so a cached block is one entry per line.
	add := func(ss ...string) {
		for _, s := range ss {
			if strings.Contains(s, "\n") {
				block = append(block, strings.Split(s, "\n")...)
				continue
			}
			block = append(block, s)
		}
	}
	if b.seam != nil {
		return m.railed(b, []string{m.seamLine(b.seam)}), -1
	}
	// A user-command block has no prompt: nobody asked for anything.
	if !b.userCommands {
		for _, gl := range wrapPlain(b.prompt, m.blockWidth()) {
			add(styleGoal.Render(gl))
		}
	}
	for _, r := range b.rows {
		if focused != nil && r == focused {
			cursorAt = len(block)
		}
		add(m.rowLines(r, focused)...)
		if r.expanded {
			add(previewLines(r, m.blockWidth()-6)...)
		}
	}
	if b.ended {
		add(m.bannerLines(b)...)
	} else if b == m.cur && m.waiting && !anyRunning(b) {
		add(fmt.Sprintf("  %s %s", m.spinner.View(), styleFaint.Render("thinking…")))
	}
	return m.railed(b, block), cursorAt
}

// seamLine is what a seam draws: how much came back, and where this
// run is running.
func (m Model) seamLine(s *event.SessionResumed) string {
	where := "host"
	if s.Sandbox {
		where = "sandbox"
	}
	return styleFaint.Render(layout.Truncate(
		fmt.Sprintf("─── resumed · %d records · %s", s.Records, where),
		m.blockWidth()))
}

// rowLines draws one row: the model's words, or a call with its badge
// and command. Takes focused rather than looking it up, per row.
func (m Model) rowLines(r, focused *historyRow) []string {
	mark := "  "
	if focused != nil && r == focused {
		mark = styleRowCursor.Render("▸ ")
	}
	if r.signin != nil {
		return m.signInRowLines(mark, r.signin)
	}
	// The model speaking, not a command: no status badge, because
	// nothing ran and there is no exit code to report.
	if r.prose != "" {
		note := ""
		if k := r.kind(); k != "" && k != event.RendersText {
			note = styleFaint.Render("  " + status.KindLabel(k))
		}
		return []string{fmt.Sprintf("%s%s %s%s", mark, styleGoal.Render("❯"),
			styleMuted.Render(layout.Truncate(plainProse(r.prose), m.blockWidth()-10)), note)}
	}
	s := status.Row{}
	switch {
	case r.running:
		s.Running = true
		s.LiveLines = len(r.live)
		s.Dropped = r.dropped
	case r.result != nil:
		s.HasResult = true
		s.ExitCode = r.result.ExitCode
		s.Err = r.result.Err != ""
		s.Summary = resultSummary(r.result)
		s.NoVerdict = r.human
		if r.post != nil {
			s.Judged = true
			s.Status = r.post.status
			s.Attention = r.post.attention
		}
	}
	icon, detail := status.Badge(s, m.spinner.View())
	// A call a human had to approve must not read like `ls`.
	if r.risk.Dangerous {
		icon = styleDanger.Render("!") + icon
	}
	// Theirs, not the model's, wherever the row happened to land.
	if r.human {
		icon = styleGoal.Render(m.prompt.mark()) + icon
	}

	// Truncated, not wrapped: a command is often one unbreakable token.
	// Where there is no room for both, the command beats the detail.
	head := lipgloss.Width(stripStyle(mark)) + lipgloss.Width(icon) + 1
	tail := "· " + detail
	if m.blockWidth()-head-1-lipgloss.Width(tail) < layout.MinTruncate {
		cmd := commandCell(r, m.blockWidth()-head)
		return []string{fmt.Sprintf("%s%s %s", mark, icon, cmd)}
	}
	cmd := commandCell(r, m.blockWidth()-head-1-lipgloss.Width(tail))
	return []string{fmt.Sprintf("%s%s %s %s", mark, icon, cmd, styleMuted.Render(tail))}
}

// commandCell is a row's command in width cells: the tool's name in its
// colour and then what it was given, so a read, a write or a search stands out.
func commandCell(r *historyRow, width int) string {
	style, ok := toolStyle(r)
	if !ok {
		return boldProgram(layout.Truncate(r.command, width))
	}
	tag := style.Render(r.tool)
	room := width - lipgloss.Width(tag) - 1
	if room < layout.MinTruncate {
		if lipgloss.Width(tag) > width {
			return layout.Truncate(r.command, width)
		}
		return tag
	}
	if r.tool == "bash" || r.tool == "powershell" {
		return tag + " " + boldProgram(layout.Truncate(r.command, room))
	}
	if r.headline == "" {
		return tag
	}
	return tag + " " + faintKeys(layout.Truncate(r.headline, room))
}

// leadArgs say what a tool touched, so history shows the first one present
// bare and ahead of the rest, which sort by name.
var leadArgs = []string{"pattern", "query", "path", "name", "url"}

// headline is a tool's arguments as a history row shows them.
func headline(tool string, args map[string]any) string {
	rest := maps.Clone(args)
	var lead string
	for _, k := range leadArgs {
		if v, ok := rest[k]; ok && v != nil {
			// Rendered by event.Command, so it is quoted as an approval quotes it.
			lead = strings.TrimPrefix(event.Command(tool, map[string]any{k: v}), tool+" "+k+"=")
			delete(rest, k)
			break
		}
	}
	others := strings.TrimSpace(strings.TrimPrefix(event.Command(tool, rest), tool))
	return strings.TrimSpace(lead + " " + others)
}

// toolStyle is the colour a row's tool name gets, by what kind of thing it does.
// A command the human ran has none: its row is marked as theirs already.
func toolStyle(r *historyRow) (lipgloss.Style, bool) {
	switch {
	case r.tool == "":
		return lipgloss.Style{}, false
	case r.executor != "":
		return toolName.server, true
	}
	switch r.tool {
	case "bash", "powershell":
		return toolName.shell, true
	case "read_file", "list_dir", "grep", "find_files":
		return toolName.read, true
	case "write_file", "edit_file":
		return toolName.write, true
	case "web_search":
		return toolName.web, true
	}
	return toolName.other, true
}

// argKey finds each key=value key, which reads quieter than its value.
var argKey = regexp.MustCompile(`(^|\s)([A-Za-z_][A-Za-z0-9_]*=)`)

func faintKeys(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range argKey.FindAllStringSubmatchIndex(s, -1) {
		b.WriteString(s[last:m[4]])
		b.WriteString(styleFaint.Render(s[m[4]:m[5]]))
		last = m[5]
	}
	b.WriteString(s[last:])
	return b.String()
}

// boldProgram picks out what a shell command runs, its first word.
func boldProgram(s string) string {
	prog, rest, found := strings.Cut(s, " ")
	if !found {
		return toolName.program.Render(prog)
	}
	return toolName.program.Render(prog) + " " + rest
}

// bannerLines is the one line under a finished block. The model's own
// words have a row of their own, so this is the outcome alone.
func (m Model) bannerLines(b *turnBlock) []string {
	w := m.blockWidth() - 2
	ran := fmt.Sprintf("%d tool call(s)", len(b.rows))
	switch b.end {
	case event.EndDone:
		return nil
	case event.EndStopped:
		return []string{"  " + styleFaint.Render("✓ stopped early — "+ran)}
	case event.EndBound:
		return []string{"  " + styleCaution.Render("⚠ step bound reached — "+ran)}
	case event.EndAborted:
		return []string{"  " + styleCaution.Render("⚠ aborted — "+ran)}
	case event.EndError:
		return wrapStyled(styleDanger, "✗ error: "+b.err, w)
	}
	return []string{"  " + styleMuted.Render("ended: "+string(b.end))}
}

// railed draws a block's rows against a coloured bar spanning all of
// them, whose colour says how the request went.
func (m Model) railed(b *turnBlock, block []string) []string {
	bar := m.railStyle(b).Render("┃") + " "
	out := make([]string, len(block))
	for i, l := range block {
		out[i] = bar + l
	}
	return out
}

// railStyle colours the rail by outcome: accent while the request is
// still running, then whatever it came to.
func (m Model) railStyle(b *turnBlock) lipgloss.Style {
	switch {
	case b.seam != nil:
		return styleFaint
	case b.userCommands:
		// Nothing ends a user-command block, so the live colour would stay on
		// it for the rest of the session.
		return styleMuted
	case !b.ended:
		return styleRowCursor
	case b.err != "" || b.end == event.EndError:
		return styleDanger
	case b.end == event.EndDone:
		return styleSafe
	case b.end == event.EndStopped:
		return styleSafe
	default:
		return styleCaution
	}
}

// blockWidth is what a block's own content gets, once the island's
// padding and the rail gutter have taken theirs.
func (m Model) blockWidth() int { return max(12, m.layout.histColW-4-railWidth) }

func previewLines(r *historyRow, width int) []string {
	src := r.live
	if r.result != nil {
		src = strings.Split(strings.TrimSuffix(outputOf(r.result), "\n"), "\n")
	}
	var out []string
	for i, l := range src {
		if i >= 3 {
			out = append(out, styleFaint.Render(fmt.Sprintf("    … +%d more (viewport below)", len(src)-3)))
			break
		}
		out = append(out, "    "+styleFaint.Render(layout.Truncate(l, width)))
	}
	return out
}

// resultSummary is the short form a row shows beside its badge.
func resultSummary(r *event.Result) string {
	if r.Err != "" {
		return r.Err
	}
	out := outputOf(r)
	if out == "" {
		return "no output"
	}
	if i := strings.IndexByte(out, '\n'); i >= 0 {
		out = out[:i]
	}
	return out
}

func anyRunning(b *turnBlock) bool {
	for _, r := range b.rows {
		if r.running {
			return true
		}
	}
	return false
}

// stripStyle measures what a styled string occupies, since a row's
// budget is cells and ANSI is bytes.
func stripStyle(s string) string {
	out := make([]rune, 0, len(s))
	var inEsc bool
	for _, r := range s {
		switch {
		case r == 0x1b:
			inEsc = true
		case inEsc && (r == 'm' || r == 'K'):
			inEsc = false
		case !inEsc:
			out = append(out, r)
		}
	}
	return string(out)
}

// join copies, because appending onto a cached slice writes into a
// backing array the cache still owns.
func join(parts ...[]string) []string {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	out := make([]string, 0, n)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// wrapPlain word-wraps unstyled text. History wraps rather than
// truncates, since the part that would be cut may be the one wanted.
func wrapPlain(s string, width int) []string {
	if width < 8 {
		width = 8
	}
	wrapped := lipgloss.NewStyle().Width(width).Render(s)
	lines := strings.Split(strings.TrimRight(wrapped, "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ") // undo Width()'s padding
	}
	return lines
}

// wrapStyled styles each physical line, indenting every continuation
// by contPrefix so a multi-line banner reads as one paragraph.
func wrapStyled(style lipgloss.Style, text string, width int) []string {
	lines := wrapPlain(text, width)
	out := make([]string, len(lines))
	for i, l := range lines {
		p := contPrefix
		if i == 0 {
			p = "  "
		}
		out[i] = p + style.Render(l)
	}
	return out
}

// plainProse drops the markup a one-line row cannot render, so a
// markdown summary reads as a sentence. The pane draws the real thing.
func plainProse(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimLeft(s, "#-*> \t")
	return strings.NewReplacer("`", "", "**", "", "__", "").Replace(s)
}
