// Package welcome fills the output pane before anything has run: what
// this session is wired to, and somewhere to start. It is handed facts
// rather than reading the harness or the process.
package welcome

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/vitzeno/detent/ui/layout"
	"github.com/vitzeno/detent/ui/theme"
)

const (
	notches = 5
	labelW  = 16
	// compactBelow is the pane height under which the banner gives up
	// its breathing room and tagline, so where commands run still fits.
	compactBelow = 22
)

// Facts is what the pane reports about this run.
type Facts struct {
	Version string
	Model   string
	Judge   string // "" when no judge is wired
	RunMode string // "host" or "sandbox"
	// Where this process runs: GOOS, GOARCH, CPU count and the working
	// directory as it should be shown.
	OS, Arch string
	CPUs     int
	WorkDir  string
	// Sandbox wiring, empty in host mode.
	Image   string
	Mount   string
	Runtime string // "" means containerd's own default
	Network string // "host" shares the containerd daemon's network

	Turns     int
	ToolCalls int
	// Sessions is how many are on disk, 0 until a listing arrives.
	Sessions int
	// Session is this run's own id, and Resumed how many records it
	// began from. Recorded is false when nothing is writing it down.
	Session  string
	Resumed  int
	Recorded bool
}

// Lines renders the pane at width by height, frame advancing the
// animation. Whole sections drop from the bottom when it is too short.
func Lines(f Facts, width, height, frame int) []string {
	sections := [][]string{
		banner(f, width, frame, height < compactBelow),
		section("environment", width, environment(f, width)),
		section("models", width, models(f, width)),
		section("session", width, session(f)),
		section("start with", width, examples(width)),
	}
	for len(sections) > 1 && countLines(sections) > height {
		sections = sections[:len(sections)-1]
	}

	var out []string
	for i, s := range sections {
		if i > 0 {
			out = append(out, "")
		}
		out = append(out, s...)
	}
	return centreVertically(out, height)
}

// Track draws the namesake mechanism: a pawl that advances one notch
// and is held there, stepped since a detent has no positions between.
func Track(frame int) []string {
	seated := frame % notches
	var t strings.Builder
	for i := range notches {
		if i > 0 {
			t.WriteString(faint.Render("━━━"))
		}
		switch {
		case i < seated:
			t.WriteString(muted.Render("●"))
		case i == seated:
			t.WriteString(brand.Render("◆"))
		default:
			t.WriteString(faint.Render("○"))
		}
	}
	// Each notch is one glyph plus three of rail, so the pawl lands
	// under its notch at 4 columns per step.
	pawl := strings.Repeat(" ", seated*4) + brand.Render("▲")
	return []string{t.String(), pawl}
}

var brand, primary, muted, faint, safe, caution, hint = bake(theme.Current())

// RefreshStyles rebuilds this package's styles from the current
// theme. Call it after theme.Apply.
func RefreshStyles() {
	brand, primary, muted, faint, safe, caution, hint = bake(theme.Current())
}

func bake(p theme.Theme) (brand, primary, muted, faint, safe, caution, hint lipgloss.Style) {
	return lipgloss.NewStyle().Foreground(p.Accent).Bold(true),
		lipgloss.NewStyle().Foreground(p.TextPrimary),
		lipgloss.NewStyle().Foreground(p.TextMuted),
		lipgloss.NewStyle().Foreground(p.TextFaint),
		lipgloss.NewStyle().Foreground(p.Safe),
		lipgloss.NewStyle().Foreground(p.Caution).Bold(true),
		lipgloss.NewStyle().Foreground(p.TextFaint).Italic(true)
}

// banner is the namesake mechanism, the name, and one line on what the
// harness does, centred as a block so the pawl stays under its notch.
func banner(f Facts, width, frame int, compact bool) []string {
	var out []string
	if !compact {
		out = append(out, "")
	}
	t := Track(frame)
	pad := strings.Repeat(" ", indentToCentre(t[0], width))
	for _, l := range t {
		out = append(out, pad+l)
	}
	name := centreLine(brand.Render("d e t e n t")+faint.Render("  "+f.Version), width)
	if compact {
		return append(out, name)
	}
	return append(out,
		"",
		name,
		// On the host, the environment section corrects this tagline.
		centreLine(faint.Render("an agent at your terminal, every request checkpointed and undoable"), width),
	)
}

func environment(f Facts, width int) []string {
	wd := f.WorkDir
	if wd == "" {
		wd = "unknown"
	}
	out := []string{
		row("this machine", primary.Render(fmt.Sprintf("%s/%s · %d cpu", f.OS, f.Arch, f.CPUs))),
		row("working dir", primary.Render(value(wd, width))),
	}
	return append(out, sandboxRows(f, width)...)
}

func sandboxRows(f Facts, width int) []string {
	if f.RunMode != "sandbox" {
		return []string{
			row("commands run", caution.Render("⚠ on this host, unsandboxed")),
			row("", faint.Render("start with -sandbox auto to isolate them")),
		}
	}
	rt := f.Runtime
	if rt == "" {
		rt = "containerd default (runc)"
	}
	return []string{
		row("commands run", safe.Render("● sandboxed, in containerd")),
		row("image", primary.Render(value(f.Image, width))),
		row("runtime", primary.Render(rt)),
		row("network", networkLine(f)),
		row("workspace", primary.Render(f.Mount)+faint.Render("  bind mount, never rolled back")),
	}
}

// networkLine spells out what the container can reach. "host" is the
// containerd daemon's host: a VM on macOS, this machine on Linux.
func networkLine(f Facts) string {
	if f.Network != "host" {
		return primary.Render("isolated") + faint.Render("  loopback only, no DNS")
	}
	if f.OS == "darwin" {
		return primary.Render("the colima VM's") + faint.Render("  your Mac is behind the VM")
	}
	return caution.Render("⚠ this machine's") + faint.Render("  localhost and LAN reachable")
}

func models(f Facts, width int) []string {
	judge := faint.Render("none — mutability, regex and repeat checks still flag")
	if f.Judge != "" {
		judge = primary.Render(value(f.Judge, width))
	}
	return []string{
		row("proposes", primary.Render(value(f.Model, width))),
		row("judges risk", judge),
		row("draws output", views()),
	}
}

// views says where the output pane's framing comes from.
func views() string {
	return primary.Render("built-in and shipped") +
		faint.Render("  composed views land with the judge")
}

func session(f Facts) []string {
	out := []string{
		row("this session", sessionID(f)),
		row("so far", primary.Render(fmt.Sprintf("%s · %s",
			plural(f.Turns, "request"), plural(f.ToolCalls, "tool call")))),
	}
	if f.Resumed > 0 {
		out = append(out, row("resumed", primary.Render(plural(f.Resumed, "record"))+
			faint.Render("  picking up where it left off")))
	}
	return append(out, row("all time", allTime(f)))
}

// sessionID is what you type after -resume, so it is shown whole. A
// session nothing records says so instead.
func sessionID(f Facts) string {
	if !f.Recorded {
		return caution.Render("⚠ not being recorded") +
			faint.Render("  this session cannot be resumed")
	}
	return primary.Render(f.Session)
}

// examples gives a first goal to copy rather than a blank box.
func examples(width int) []string {
	out := make([]string, 0, 4)
	for _, e := range []string{
		"what is listening on port 3000?",
		"find the biggest files in this directory and delete the logs",
		"why does the build fail? fix it if you can",
	} {
		out = append(out, "  "+faint.Render("›")+" "+primary.Render(value(e, width)))
	}
	return append(out, "    "+hint.Render("or a slash command: /status, /context, /undo, /help"))
}

// section titles a group. The rule is short on purpose: four
// full-width ones read as a form rather than a place to start.
func section(title string, width int, body []string) []string {
	rule := min(24, max(0, width-lipgloss.Width(title)-3))
	head := "  " + muted.Render(title) + " " + faint.Render(strings.Repeat("─", rule))
	return append([]string{head}, body...)
}

func row(label, val string) string {
	// Pad before styling, or the escape bytes count toward the width.
	return "  " + faint.Render(fmt.Sprintf("%-*s", labelW, label)) + val
}

// value trims to what's left of the row after the label.
func value(s string, width int) string { return layout.Truncate(s, width-labelW-4) }

func centreLine(s string, width int) string {
	return strings.Repeat(" ", indentToCentre(s, width)) + s
}

// indentToCentre is how far to push a rendered line to sit centred in
// width. ANSI-aware, so a styled line doesn't drift left.
func indentToCentre(s string, width int) int {
	return max(0, (width-lipgloss.Width(s))/2)
}

// centreVertically pads above the block so it sits in the middle of an
// otherwise empty pane. Never pads when the content already fills it.
func centreVertically(lines []string, height int) []string {
	pad := (height - len(lines)) / 2
	if pad <= 0 {
		return lines
	}
	return append(make([]string, pad), lines...)
}

// allTime says nothing rather than zero until a listing arrives:
// "0 sessions" would read as none recorded, not as not yet asked.
func allTime(f Facts) string {
	if f.Sessions == 0 {
		return faint.Render("—")
	}
	return primary.Render(plural(f.Sessions, "session")) +
		faint.Render("  resume one with /sessions")
}

// plural counts a thing without the "(s)" hedge.
func plural(n int, thing string) string {
	if n == 1 {
		return "1 " + thing
	}
	return fmt.Sprintf("%d %ss", n, thing)
}

func countLines(sections [][]string) int {
	n := len(sections) - 1 // the blank line between each
	for _, s := range sections {
		n += len(s)
	}
	return n
}
