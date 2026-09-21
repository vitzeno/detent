// Package welcome fills the output pane before anything has run: what
// this session is wired to, and somewhere to start. It is handed facts
// rather than reading the harness, so it can't reach past the pane it
// draws.
package welcome

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/vitzeno/detent/ui/layout"
	"github.com/vitzeno/detent/ui/status"
	"github.com/vitzeno/detent/ui/theme"
)

// TickRate is how often the detent animation advances.
const TickRate = 420 * time.Millisecond

// Facts is what the pane reports about this run.
type Facts struct {
	Proposer string
	Judge    string // "" when no judge is wired
	RunMode  string // "host" or "sandbox"
	// Views is "saved" or "generate": whether a model may author a
	// spec the pane has none for.
	Views string

	// Sandbox wiring; empty in host mode.
	Image   string
	Mount   string
	Runtime string // "" means containerd's own default
	Network string // "host" shares the containerd daemon's network

	Goals       int
	Commands    int
	MachineTime time.Duration
}

// Lines renders the pane at width by height, frame advancing the
// animation. Sections drop from the bottom when it's too short to fit
// them, since a cut mid-row reads worse than one section fewer.
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

// banner is the namesake mechanism, the name, and one line on what the
// harness does — centred as a block so the pawl stays under its notch.
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
	if compact {
		return append(out, centreLine(brand.Render("d e t e n t"), width))
	}
	return append(out,
		"",
		centreLine(brand.Render("d e t e n t"), width),
		// Checkpointing is a sandbox thing; on the host the
		// environment section below carries the correction rather than
		// the tagline hedging it.
		centreLine(faint.Render("one command at a time, every step checkpointed and undoable"), width),
	)
}

func environment(f Facts, width int) []string {
	wd, err := os.Getwd()
	if err != nil {
		wd = "unknown"
	}
	out := []string{
		row("this machine", goal.Render(fmt.Sprintf("%s/%s · %d cpu",
			runtime.GOOS, runtime.GOARCH, runtime.NumCPU()))),
		row("working dir", goal.Render(value(shortPath(wd), width))),
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
		row("image", goal.Render(value(f.Image, width))),
		row("runtime", goal.Render(rt)),
		row("network", networkLine(f)),
		row("workspace", goal.Render(f.Mount)+faint.Render("  bind mount, never rolled back")),
	}
}

// networkLine spells out what the container can reach. "host" is the
// containerd daemon's host: a VM on macOS, this machine on Linux.
func networkLine(f Facts) string {
	if f.Network != "host" {
		return goal.Render("isolated") + faint.Render("  loopback only, no DNS")
	}
	if runtime.GOOS == "darwin" {
		return goal.Render("the colima VM's") + faint.Render("  your Mac is behind the VM")
	}
	return caution.Render("⚠ this machine's") + faint.Render("  localhost and LAN reachable")
}

func models(f Facts, width int) []string {
	judge := faint.Render("none — only the regex backstop flags danger")
	if f.Judge != "" {
		judge = goal.Render(value(f.Judge, width))
	}
	return []string{
		row("proposes", goal.Render(value(f.Proposer, width))),
		row("judges risk", judge),
		row("draws output", views(f)),
	}
}

// views states what the output pane may do, and says plainly when a
// model is allowed to author the framing a human will read.
func views(f Facts) string {
	switch f.Views {
	case "generate":
		return goal.Render("generated") +
			faint.Render("  a model writes views for output nothing covers")
	}
	return goal.Render("built-in, shipped and saved") +
		faint.Render("  set views: generate to author new ones")
}

func session(f Facts) []string {
	return []string{
		row("so far", goal.Render(fmt.Sprintf("%s · %s · %s",
			plural(f.Goals, "goal"), plural(f.Commands, "command"), status.Dur(f.MachineTime)))),
		// Placeholders, not invented numbers: these need the
		// persistence layer to mean anything.
		row("all time", faint.Render("— sessions · — goals   (persistence pending)")),
	}
}

// examples gives a first goal to copy rather than a blank box.
func examples(width int) []string {
	out := make([]string, 0, 4)
	for _, e := range []string{
		"what is listening on port 3000?",
		"find the biggest files in this directory",
		"why does the build fail?",
	} {
		out = append(out, "  "+faint.Render("›")+" "+goal.Render(value(e, width)))
	}
	return append(out, "    "+hint.Render("or a slash command: /tree, /usage, /rollback, /help"))
}

// section titles a group. The rule is short on purpose: four
// full-width ones read as a form rather than a place to start.
func section(title string, width int, body []string) []string {
	rule := min(24, max(0, width-lipgloss.Width(title)-3))
	head := "  " + muted.Render(title) + " " + faint.Render(strings.Repeat("─", rule))
	return append([]string{head}, body...)
}

// Track draws the namesake mechanism: a pawl that advances one notch
// and is held there. Stepped rather than slid on purpose, since a
// detent has no positions in between.
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

func row(label, val string) string {
	// Pad the label before styling: padding after would count the
	// escape bytes and silently drop it.
	return "  " + faint.Render(fmt.Sprintf("%-*s", labelW, label)) + val
}

// value trims to what's left of the row after the label.
func value(s string, width int) string { return layout.Truncate(s, width-labelW-4) }

// shortPath trades the home prefix for ~, so a deep working directory
// still fits the pane.
func shortPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || !strings.HasPrefix(p, home) {
		return p
	}
	return "~" + p[len(home):]
}

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

const (
	notches = 5
	labelW  = 16
	// compactBelow is the pane height under which the banner gives up
	// its breathing room and tagline, so where commands run still fits.
	compactBelow = 22
)

var (
	brand   lipgloss.Style
	goal    lipgloss.Style
	muted   lipgloss.Style
	faint   lipgloss.Style
	safe    lipgloss.Style
	caution lipgloss.Style
	hint    lipgloss.Style
)

func init() { RefreshStyles() }

// RefreshStyles rebuilds this package's styles from the current
// theme — call after theme.Apply.
func RefreshStyles() {
	brand = lipgloss.NewStyle().Foreground(theme.Accent).Bold(true)
	goal = lipgloss.NewStyle().Foreground(theme.TextPrimary)
	muted = lipgloss.NewStyle().Foreground(theme.TextMuted)
	faint = lipgloss.NewStyle().Foreground(theme.TextFaint)
	safe = lipgloss.NewStyle().Foreground(theme.Safe)
	caution = lipgloss.NewStyle().Foreground(theme.Caution).Bold(true)
	hint = lipgloss.NewStyle().Foreground(theme.TextFaint).Italic(true)
}
