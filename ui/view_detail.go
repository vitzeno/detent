package ui

import (
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/ui/layout"
	"github.com/vitzeno/detent/ui/syntax"
	"github.com/vitzeno/detent/ui/theme"
	"github.com/vitzeno/detent/ui/view"
	"github.com/vitzeno/detent/ui/welcome"
	"github.com/vitzeno/detent/version"
)

// The output pane: what the focused row renders into it, and the
// viewport refresh that keeps it in step.

// scrollMargin is how many lines of context a jump keeps above the line it lands on.
const scrollMargin = 2

// detailLines renders the focused row: a pending question first,
// else the scrolling viewport.
func (m Model) detailLines() []string {
	// The undo list can run to hundreds of paths, so it gets the pane
	// and the box below stays small and asks.
	if m.mode == modeUndo || m.mode == modeForget {
		return strings.Split(m.output.View(), "\n")
	}
	// A panel is a page about the session, drawn through the viewport
	// so it scrolls however long it runs.
	if m.panel.open != panelNone {
		return strings.Split(m.output.View(), "\n")
	}
	if m.showWelcome() {
		return m.welcomeLines()
	}
	return strings.Split(m.output.View(), "\n")
}

// showWelcome reports whether the output pane has nothing of its own
// to show yet, which is the boot state: nothing has been asked.
func (m Model) showWelcome() bool {
	return m.focused() == nil && len(m.blocks) == 0
}

// welcomeLines hands the boot pane the facts it reports, so nothing in
// ui/welcome reaches into Model.
func (m Model) welcomeLines() []string {
	return welcome.Lines(welcome.Facts{
		Version: version.String(),
		Model:   m.run.Model, Judge: m.run.Judge, RunMode: m.runMode(), MaxAgents: m.run.MaxAgents,
		OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), WorkDir: m.workDir,
		Image: m.info.Image, Mount: m.info.Mount,
		Runtime: m.info.Runtime, Network: m.info.Network,
		Turns: len(m.blocks), ToolCalls: m.calls, Sessions: len(m.sessions),
		Session: m.run.Session.String(), Resumed: m.run.Resumed, Recorded: m.run.Recorded,
	}, paneInner(m.layout.outputColW), m.output.Height(), m.welcomeFrame)
}

// refreshViewport redraws the output pane when its detailKey changed.
func (m *Model) refreshViewport() {
	window, offset := m.historyWindow()
	m.nav.histWindow = window
	// -1 means following, which never counted a total to offset into.
	if offset >= 0 {
		m.nav.histOffset = offset
	}
	// Scrolling moves the viewport, not the content, so it must not redraw.
	key := m.detailKey()
	if key == m.detail {
		return
	}
	prev := m.detail
	m.detail = key
	if m.panel.open != panelNone {
		m.setViewContent(strings.Join(m.panelLines(), "\n"))
		return
	}
	if m.mode == modeForget {
		m.setViewContent(strings.Join(m.forgetLines(), "\n"))
		return
	}
	if m.mode == modeUndo {
		m.setViewContent(strings.Join(m.undoLines(), "\n"))
		return
	}
	r := m.focused()
	// A review is its comments and how its reviewer stands, not output.
	if r != nil && r.review != uuid.Nil {
		m.setViewContent(strings.Join(m.reviewSummary(r.review), "\n"))
		return
	}
	if r == nil {
		for _, a := range m.pinned() {
			if rv := reviewerOf(a); rv != nil {
				m.setViewContent(strings.Join(m.reviewSummary(rv.review), "\n"))
				return
			}
		}
	}
	if r == nil && len(m.pinned()) > 0 {
		// The request's only rows are its subagents, drawn in the block.
		m.setViewContent(styleFaint.Render("subagents are working · [a] looks into them"))
		return
	}
	if r == nil {
		m.setViewContent(styleFaint.Render("(no output yet)"))
		return
	}
	if r.signin != nil {
		m.setViewContent(strings.Join(signInPageLines(r.signin, paneInner(m.layout.outputColW)), "\n"))
		return
	}
	var body string
	switch {
	case r.running:
		body = strings.Join(r.live, "\n")
	case r.drawable():
		body = strings.Join(m.rowBody(r, paneInner(m.layout.outputColW)), "\n")
	}
	if body == "" {
		m.setViewContent(styleFaint.Render("(no output)"))
		return
	}
	// Follow a running row from its end only, so scrolling up to read stays put.
	follow := r.running && (prev.row != r || m.output.AtBottom())
	m.setViewContent(body)
	if follow {
		m.output.GotoBottom()
	}
}

// setViewContent skips identical content: SetContent resets scroll
// position, which must not happen just from resizing for the dropdown.
func (m *Model) setViewContent(s string) {
	s = wrapWide(s, m.output.Width())
	if s == m.viewContent {
		return
	}
	m.viewContent = s
	m.output.SetContent(s)
}

// wrapWide wraps any line wider than the pane, so a page or live output reads
// whole rather than running off the edge. A view's lines already fit.
func wrapWide(s string, width int) string {
	if width <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, wrapANSI(l, width)...)
	}
	return strings.Join(out, "\n")
}

// wrapANSI breaks at spaces, and mid-word only for a word wider than n, keeping
// a command's colours on whichever line they fall.
func wrapANSI(s string, n int) []string {
	if n <= 0 || ansi.StringWidth(s) <= n {
		return []string{s}
	}
	return strings.Split(ansi.Wrap(s, n, ""), "\n")
}

// rowBody draws a row width wide wherever it is shown: the model's words as a view,
// a diff as /review colours one, and any other output as the text it is.
func (m Model) rowBody(r *historyRow, width int) []string {
	if r.prose != "" {
		return strings.Split(m.proseBody(r, width), "\n")
	}
	text := r.text()
	if diffHunk.MatchString(text) {
		return diffLines(text, width)
	}
	return strings.Split(wrapWide(text, width), "\n")
}

// proseBody draws the model's words through ui/view, kept on the row since the
// pane redraws on every focus change and glamour is the slow part.
func (m Model) proseBody(r *historyRow, width int) string {
	style := theme.Current().Markdown
	if r.drawn.width != width || r.drawn.style != style {
		text := view.Render(r.prose, theme.Current(), width)
		if r.thinking {
			text = thoughtBody(r.prose, width)
		}
		r.drawn = proseDrawing{width: width, style: style, text: text}
	}
	return r.drawn.text
}

// thoughtBody is reasoning wrapped and dimmed, read as background to the view.
func thoughtBody(text string, width int) string {
	var lines []string
	for line := range strings.SplitSeq(text, "\n") {
		for _, part := range wrapANSI(line, width) {
			lines = append(lines, styleFaint.Render(part))
		}
	}
	return strings.Join(lines, "\n")
}

// hunkPattern is a unified diff's hunk header, its counts 1 when git leaves them out.
const hunkPattern = `@@ -\d+(,\d+)? \+\d+(,\d+)? @@`

// diffHunk finds a hunk header anywhere, which is what says an output is a diff.
var diffHunk, hunkHeader = regexp.MustCompile(`(?m)^` + hunkPattern), regexp.MustCompile(`^` + hunkPattern)

// diffLines draws a unified diff as /review does: each hunk coloured by its file's
// language, read for exactly the lines its header counts, so a removed "-- x" stays code.
func diffLines(text string, width int) []string {
	var out []string
	path := ""
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		m := hunkHeader.FindStringSubmatch(line)
		if m == nil {
			if after, ok := strings.CutPrefix(line, "+++ "); ok {
				path = strings.TrimPrefix(after, "b/")
			}
			out = append(out, styleMuted.Render(layout.Truncate(line, width)))
			continue
		}
		out = append(out, styleFaint.Render(layout.Truncate(line, width)))
		old, nu := count(m[1]), count(m[2])
		var hunk []string
		for i+1 < len(lines) && (old > 0 || nu > 0) {
			next := lines[i+1]
			switch {
			case strings.HasPrefix(next, "-"):
				old--
			case strings.HasPrefix(next, "+"):
				nu--
			case strings.HasPrefix(next, `\`):
			default:
				old, nu = old-1, nu-1
			}
			hunk = append(hunk, next)
			i++
		}
		out = append(out, hunkLines(path, hunk, width)...)
	}
	return out
}

func count(group string) int {
	if group == "" {
		return 1
	}
	n, _ := strconv.Atoi(group[1:])
	return n
}

// hunkLines colours one hunk's lines by the language of the file at path.
func hunkLines(path string, lines []string, width int) []string {
	texts := make([]string, len(lines))
	for i, l := range lines {
		if l != "" {
			texts[i] = l[1:]
		}
	}
	code := syntax.Hunk(path, texts, palette.Syntax)
	out := make([]string, len(lines))
	for i, l := range lines {
		op := event.LineContext
		if l != "" {
			op = event.LineOp(l[0])
		}
		if code == nil || l == "" || op == '\\' {
			out[i] = lineStyle(op).Render(layout.Truncate(l, width))
			continue
		}
		out[i] = tinted(op, code[i], width)
	}
	return out
}

// tildePath trades a home prefix for ~, so a deep working directory
// still fits the pane. Whole path elements only.
func tildePath(dir, home string) string {
	if home == "" {
		return dir
	}
	if dir == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(dir, home+string(filepath.Separator)); ok {
		return "~" + string(filepath.Separator) + rest
	}
	return dir
}
