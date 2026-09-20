package ui

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/vitzeno/detent/internal/ui/status"
)

const (
	detentNotches   = 5
	welcomeTickRate = 420 * time.Millisecond
	welcomeLabelW   = 16
)

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

// shortPath trades the home prefix for ~, so a deep working directory
// still fits the pane.
func shortPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || !strings.HasPrefix(p, home) {
		return p
	}
	return "~" + p[len(home):]
}

// welcomeLines fills the output pane before anything has run. Not a
// RenderKind: those classify a finished command's output, and this is
// a pane state, closer to the tool views.
func (m Model) welcomeLines() []string {
	width := paneInner(m.layout.outputColW)
	out := []string{""}
	for _, l := range detentTrack(m.welcomeFrame) {
		out = append(out, "      "+l)
	}
	out = append(out,
		"",
		"  "+styleMuted.Render("every sandboxed step is checkpointed, so any of them can be undone"),
		"",
	)

	wd, err := os.Getwd()
	if err != nil {
		wd = "unknown"
	}
	out = append(out,
		welcomeRow("this machine", styleGoal.Render(fmt.Sprintf("%s/%s · %d cpu",
			runtime.GOOS, runtime.GOARCH, runtime.NumCPU()))),
		welcomeRow("working dir", styleGoal.Render(truncateWidth(shortPath(wd), width-welcomeLabelW-4))),
		"",
	)

	out = append(out, m.welcomeSandboxLines(width)...)

	snap := m.sess.UsageSnapshot()
	out = append(out,
		"",
		welcomeRow("this session", styleGoal.Render(fmt.Sprintf("%d goal(s) · %d command(s) · %s",
			snap.Goals, snap.Commands, status.Dur(snap.MachineTime())))),
		// Placeholders, not invented numbers: these need the
		// persistence layer to mean anything.
		welcomeRow("all time", styleFaint.Render("— sessions · — goals   (persistence pending)")),
		"",
		"  "+styleHint.Render("describe a goal below, or type / for commands"),
	)
	return out
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
		welcomeRow("image", styleGoal.Render(truncateWidth(m.info.Image, width-welcomeLabelW-4))),
		welcomeRow("runtime", styleGoal.Render(rt)),
		welcomeRow("workspace", styleGoal.Render(m.info.Mount+styleFaint.Render("  (bind mount, not rolled back)"))),
	}
}
