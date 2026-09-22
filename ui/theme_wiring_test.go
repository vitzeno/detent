package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/ui/theme"
)

// Every subpackage bakes its own styles from theme at init, so each
// one needs rebuilding when the theme changes. A package added without
// wiring its RefreshStyles into ui.RefreshStyles keeps rendering the
// old palette, and nothing else would notice.
func TestRefreshStyles_ReachesEverySubpackage(t *testing.T) {
	t.Cleanup(func() {
		theme.Apply(theme.Themes[theme.DefaultName])
		RefreshStyles()
	})

	// A pane wide enough to exercise the welcome pane, the history
	// pane, a judged row and the slash dropdown all at once.
	render := func(name string) string {
		theme.Apply(theme.Themes[name])
		RefreshStyles()
		m := themedModel()
		return m.View().Content
	}

	seen := map[string]string{}
	for _, name := range theme.Names() {
		out := render(name)
		require.NotEmpty(t, out)
		for other, prev := range seen {
			assert.NotEqual(t, prev, out,
				"%q and %q render identically — a subpackage is holding stale styles", other, name)
		}
		seen[name] = out
	}
	require.Len(t, seen, len(theme.Names()))
}

// Switching theme must change the colours every package draws with,
// not merely the ones ui owns directly.
func TestRefreshStyles_EachPackageFollowsTheTheme(t *testing.T) {
	t.Cleanup(func() {
		theme.Apply(theme.Themes[theme.DefaultName])
		RefreshStyles()
	})

	colours := func(name string) map[string]bool {
		theme.Apply(theme.Themes[name])
		RefreshStyles()
		out := map[string]bool{}
		for _, seq := range ansiPattern.FindAllString(themedModel().View().Content, -1) {
			out[seq] = true
		}
		return out
	}
	dark, light := colours("dark"), colours("light")

	shared := 0
	for c := range dark {
		if light[c] {
			shared++
		}
	}
	assert.Less(t, shared, len(dark)/2,
		"most colours should differ between dark and light; %d of %d were shared", shared, len(dark))
}

// themedModel renders as much of the UI at once as one frame can hold:
// the boot pane's facts, a finished goal with a jev verdict, and the
// slash dropdown.
func themedModel() Model {
	m := New(context.Background(), newFakeDriver(), SessionInfo{
		Proposer: "test-model", Judge: "jev", RunMode: "sandbox",
		Image: "img", Mount: "/workspace", Network: "host",
	})
	m.layout.width, m.layout.height = 120, 40
	p := PostJudgment{FromJudge: true, Status: "clean_success", RenderKind: KindText, GoalAchieved: 0.9}
	b := &goalBlock{
		goal: "g", res: &GoalResult{Goal: "g"}, ended: true, end: EndDone, summary: "did the thing",
		steps: []*stepRow{{command: "ls -la", cmd: cmdState{ec: &ExecutedCommand{
			Command: "ls -la", Result: Result{Stdout: "a\nb\n"}, SnapshotID: "s1", Post: &p,
		}}}, {prose: "did the thing"}},
	}
	b.judge = goalVerdict(b)
	m.blocks = []*goalBlock{b}
	m.sizeViewport()
	return m
}

// A theme must not leave any text unstyled: a bare run would fall back
// to the terminal's own foreground and stop following the palette.
func TestThemes_StyleEveryRenderedLine(t *testing.T) {
	t.Cleanup(func() {
		theme.Apply(theme.Themes[theme.DefaultName])
		RefreshStyles()
	})
	for _, name := range theme.Names() {
		t.Run(name, func(t *testing.T) {
			theme.Apply(theme.Themes[name])
			RefreshStyles()
			for i, line := range strings.Split(themedModel().View().Content, "\n") {
				if strings.TrimSpace(stripANSI(line)) == "" {
					continue
				}
				assert.Contains(t, line, "\x1b[", "row %d renders unstyled: %q", i, stripANSI(line))
			}
		})
	}
}
