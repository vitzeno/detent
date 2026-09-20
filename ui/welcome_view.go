package ui

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/vitzeno/detent/ui/status"
)

const (
	detentNotches   = 5
	welcomeTickRate = 420 * time.Millisecond
	welcomeLabelW   = 16
)

// welcomeLines fills the output pane before anything has run. Not a
// RenderKind: those classify a finished command's output, and this is
// a pane state, closer to the tool views.
//
// Sections are dropped from the bottom when the pane is too short,
// rather than letting the island cut them mid-row: the banner and
// where commands run are what a human needs before typing anything.
func (m Model) welcomeLines() []string {
	width := paneInner(m.layout.outputColW)

	sections := [][]string{
		m.welcomeBanner(width, m.output.Height() < compactBannerBelow),
		m.welcomeSection("environment", width, m.welcomeEnvironment(width)),
		m.welcomeSection("models", width, m.welcomeModels(width)),
		m.welcomeSection("session", width, m.welcomeSession()),
		m.welcomeSection("start with", width, welcomeExamples(width)),
	}
	for len(sections) > 1 && countLines(sections) > m.output.Height() {
		sections = sections[:len(sections)-1]
	}

	var out []string
	for i, s := range sections {
		if i > 0 {
			out = append(out, "")
		}
		out = append(out, s...)
	}
	return centreVertically(out, m.output.Height())
}

// compactBannerBelow is the pane height under which the banner gives
// up its breathing room and tagline, so where commands run still fits.
const compactBannerBelow = 22

// welcomeBanner is the namesake mechanism, the name, and one line on
// what the harness does — centred as a block so the pawl stays under
// its notch.
func (m Model) welcomeBanner(width int, compact bool) []string {
	var out []string
	if !compact {
		out = append(out, "")
	}
	track := detentTrack(m.welcomeFrame)
	pad := strings.Repeat(" ", indentToCentre(track[0], width))
	for _, l := range track {
		out = append(out, pad+l)
	}
	if compact {
		return append(out, centreLine(styleBrand.Render("d e t e n t"), width))
	}
	return append(out,
		"",
		centreLine(styleBrand.Render("d e t e n t"), width),
		centreLine(styleFaint.Render(m.welcomeTagline()), width),
	)
}

// welcomeTagline is what detent is. Checkpointing is a sandbox thing,
// so on the host the environment section below carries the correction
// rather than the tagline hedging it.
func (m Model) welcomeTagline() string {
	return "one command at a time, every step checkpointed and undoable"
}

func (m Model) welcomeEnvironment(width int) []string {
	wd, err := os.Getwd()
	if err != nil {
		wd = "unknown"
	}
	out := []string{
		welcomeRow("this machine", styleGoal.Render(fmt.Sprintf("%s/%s · %d cpu",
			runtime.GOOS, runtime.GOARCH, runtime.NumCPU()))),
		welcomeRow("working dir", styleGoal.Render(welcomeValue(shortPath(wd), width))),
	}
	return append(out, m.welcomeSandboxLines(width)...)
}

func (m Model) welcomeSandboxLines(width int) []string {
	if m.info.RunMode != "sandbox" {
		return []string{
			welcomeRow("commands run", styleCaution.Render("⚠ on this host, unsandboxed")),
			welcomeRow("", styleFaint.Render("start with -sandbox auto to isolate them")),
		}
	}
	rt := m.info.Runtime
	if rt == "" {
		rt = "containerd default (runc)"
	}
	return []string{
		welcomeRow("commands run", styleSafe.Render("● sandboxed, in containerd")),
		welcomeRow("image", styleGoal.Render(welcomeValue(m.info.Image, width))),
		welcomeRow("runtime", styleGoal.Render(rt)),
		welcomeRow("network", m.networkLine()),
		welcomeRow("workspace", styleGoal.Render(m.info.Mount)+styleFaint.Render("  bind mount, never rolled back")),
	}
}

// networkLine spells out what the container can reach. "host" is the
// containerd daemon's host: a VM on macOS, this machine on Linux.
func (m Model) networkLine() string {
	if m.info.Network != "host" {
		return styleGoal.Render("isolated") + styleFaint.Render("  loopback only, no DNS")
	}
	if runtime.GOOS == "darwin" {
		return styleGoal.Render("the colima VM's") + styleFaint.Render("  your Mac is behind the VM")
	}
	return styleCaution.Render("⚠ this machine's") + styleFaint.Render("  localhost and LAN reachable")
}

func (m Model) welcomeModels(width int) []string {
	judge := styleFaint.Render("none — only the regex backstop flags danger")
	if m.info.Judge != "" {
		judge = styleGoal.Render(welcomeValue(m.info.Judge, width))
	}
	return []string{
		welcomeRow("proposes", styleGoal.Render(welcomeValue(m.info.Proposer, width))),
		welcomeRow("judges risk", judge),
	}
}

func (m Model) welcomeSession() []string {
	snap := m.sess.UsageSnapshot()
	return []string{
		welcomeRow("so far", styleGoal.Render(fmt.Sprintf("%s · %s · %s",
			plural(snap.Goals, "goal"), plural(snap.Commands, "command"),
			status.Dur(snap.MachineTime())))),
		// Placeholders, not invented numbers: these need the
		// persistence layer to mean anything.
		welcomeRow("all time", styleFaint.Render("— sessions · — goals   (persistence pending)")),
	}
}

// welcomeExamples gives a first goal to copy rather than a blank box.
func welcomeExamples(width int) []string {
	out := make([]string, 0, 4)
	for _, e := range []string{
		"what is listening on port 3000?",
		"find the biggest files in this directory",
		"why does the build fail?",
	} {
		out = append(out, "  "+styleFaint.Render("›")+" "+styleGoal.Render(welcomeValue(e, width)))
	}
	return append(out, "    "+styleHint.Render("or a slash command: /tree, /usage, /rollback, /help"))
}

// welcomeSection titles a group. The rule is short on purpose: four
// full-width ones read as a form rather than a place to start.
func (m Model) welcomeSection(title string, width int, body []string) []string {
	rule := min(24, max(0, width-lipgloss.Width(title)-3))
	head := "  " + styleMuted.Render(title) + " " + styleFaint.Render(strings.Repeat("─", rule))
	return append([]string{head}, body...)
}

// detentTrack draws the namesake mechanism: a pawl that advances one
// notch and is held there. Stepped rather than slid on purpose, since
// a detent has no positions in between.
func detentTrack(frame int) []string {
	seated := frame % detentNotches
	var track strings.Builder
	for i := range detentNotches {
		if i > 0 {
			track.WriteString(styleFaint.Render("━━━"))
		}
		switch {
		case i < seated:
			track.WriteString(styleMuted.Render("●"))
		case i == seated:
			track.WriteString(styleBrand.Render("◆"))
		default:
			track.WriteString(styleFaint.Render("○"))
		}
	}
	// Each notch is one glyph plus three of rail, so the pawl lands
	// under its notch at 4 columns per step.
	pawl := strings.Repeat(" ", seated*4) + styleBrand.Render("▲")
	return []string{track.String(), pawl}
}

func welcomeRow(label, value string) string {
	// Pad the label before styling: padding after would count the
	// escape bytes and silently drop it.
	return "  " + styleFaint.Render(fmt.Sprintf("%-*s", welcomeLabelW, label)) + value
}

// welcomeValue trims a value to what's left of the row after the label.
func welcomeValue(s string, width int) string {
	return truncateWidth(s, width-welcomeLabelW-4)
}

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
