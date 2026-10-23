package view

import (
	"fmt"
	"image/color"
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/ui/theme"
)

const sample = "# Fixed undo\n\nIt works **outside git**.\n\n" +
	"```chart\ntype: bar\ntitle: Test time\nunit: s\ndata:\n  engine: 4.0\n  ui: 5.7\n  store: 0.6\n```\n\n" +
	"```stats\n- label: Tests\n  value: 412\n  delta: +9\n- label: Coverage\n  value: 81%\n```\n\n" +
	"```status\n- pass: [engine, store]\n- fail: worktree\n- skip: sandbox\n```\n\nAfter the blocks.\n"

// A block is drawn where it was written, its data in the order written, with
// the markdown around it still markdown.
func TestRender_DrawsATypedBlockInPlace(t *testing.T) {
	out := plain(Render(sample, theme.Current(), 80))
	at := func(s string) int {
		t.Helper()
		i := strings.Index(out, s)
		require.GreaterOrEqual(t, i, 0, "missing %q in\n%s", s, out)
		return i
	}
	assert.Less(t, at("outside git"), at("Test time"))
	assert.Less(t, at("engine █"), at("ui     █"), "the order written, not sorted")
	assert.Less(t, at("ui     █"), at("store  █"))
	assert.Less(t, at("store  █"), at("After the blocks."))
	assert.NotContains(t, out, "type: bar", "drawn, not shown as code")
	for _, s := range []string{"412 +9", "Coverage", "✓ pass  engine, store", "✗ fail  worktree", "○ skip  sandbox"} {
		at(s)
	}
}

// The longest bar is the largest value, whatever order the data came in.
func TestRender_ScalesBarsToTheLargest(t *testing.T) {
	out := plain(Render("```chart\ntype: bar\ndata: {small: 1, big: 10}\n```\n", theme.Current(), 60))
	var small, big int
	for line := range strings.SplitSeq(out, "\n") {
		switch {
		case strings.Contains(line, "small"):
			small = strings.Count(line, "█")
		case strings.Contains(line, "big"):
			big = strings.Count(line, "█")
		}
	}
	assert.Positive(t, small)
	assert.Greater(t, big, small*5)
}

// Models write inline code and colons inside a block's text as they do in prose,
// and a status block read as YAML refused both, so the first live one never drew.
func TestRender_ReadsABlockWrittenAsAModelWritesIt(t *testing.T) {
	block := "```status\n- pass: `go test ./ui/view/` passed (cached)\n- fail: TestX: boom\n" +
		"- skip:\n  - sandbox\n  - `windows`\n```\n" +
		"```chart\ntype: bar\ndata:\n  `ui`: 3\n  `engine`: 1\n```\n"
	out := plain(Render(block, theme.Current(), 80))
	assert.NotContains(t, out, "not drawn")
	for _, want := range []string{"✓ pass  go test ./ui/view/ passed (cached)", "✗ fail  TestX: boom",
		"○ skip  sandbox, windows", "ui     █"} {
		assert.Contains(t, out, want)
	}
}

// A word unit reads apart from its number, and a symbol stays on it.
func TestRender_SpacesAWordUnit(t *testing.T) {
	out := plain(Render("```chart\ntype: bar\nunit: lines\ndata: {a: 10}\n```\n```chart\ntype: bar\nunit: ms\ndata: {b: 5}\n```\n",
		theme.Current(), 60))
	assert.Contains(t, out, "10 lines")
	assert.Contains(t, out, "5ms")
}

// A block that does not parse costs its drawing and never the reply: it is
// shown as written, the reason above it, and what follows still draws.
func TestRender_ShowsABadBlockAsWrittenWithWhy(t *testing.T) {
	for name, block := range map[string]string{
		"an unknown type":  "```chart\ntype: pie\ndata: {a: 1}\n```\n",
		"a misspelt key":   "```chart\ntype: bar\ndatta: {a: 1}\n```\n",
		"not yaml":         "```stats\n- label: [unclosed\n```\n",
		"an empty block":   "```status\n```\n",
		"an unknown state": "```status\n- maybe: x\n```\n",
	} {
		t.Run(name, func(t *testing.T) {
			out := plain(Render(block+"\nAfter.\n", theme.Current(), 80))
			assert.Contains(t, out, "block not drawn:")
			assert.Contains(t, out, "After.")
		})
	}
}

// Only a fence opened at the top level is drawn, so a chart quoted inside
// another fence, as an example, stays the code it is. So does any other language.
func TestRender_LeavesOtherFencesAsCode(t *testing.T) {
	quoted := "````markdown\n```chart\ntype: bar\ndata: {a: 1}\n```\n````\n"
	out := plain(Render(quoted, theme.Current(), 80))
	assert.Contains(t, out, "type: bar")
	assert.NotContains(t, out, "█")

	out = plain(Render("```go\nfunc main() {}\n```\n", theme.Current(), 80))
	assert.Contains(t, out, "func main()")
}

// The pane draws exactly width cells, so no line may be wider, however narrow.
func TestRender_NoLineIsWiderThanTheWidth(t *testing.T) {
	for width := 1; width <= 100; width += 3 {
		for line := range strings.SplitSeq(Render(sample, theme.Current(), width), "\n") {
			require.LessOrEqual(t, ansi.StringWidth(line), width, "at width %d: %q", width, line)
		}
	}
}

// The model's text can carry an escape meant for the terminal, a clipboard
// write here, which is shown rather than sent.
func TestRender_ShowsControlsRatherThanSendingThem(t *testing.T) {
	out := Render("Done \x1b]52;c;ZXZpbA==\x07 there\n\n```status\n- pass: \"x\x1b[2J\"\n```\n", theme.Current(), 80)
	assert.NotContains(t, out, "\x1b]52")
	assert.NotContains(t, out, "\x1b[2J")
}

// Columns stand on one baseline, each value above its own and its label below.
func TestRender_StandsColumnsOnABaseline(t *testing.T) {
	out := plain(Render("```chart\ntype: columns\ndata: {short: 1, tall: 8}\n```\n", theme.Current(), 40))
	lines := strings.Split(out, "\n")
	require.Len(t, lines, 10, "a value row, eight rows of column and a label row")
	assert.Contains(t, lines[0], "1")
	assert.Contains(t, lines[0], "8")
	assert.Contains(t, lines[9], "short")
	assert.Contains(t, lines[9], "tall")
	assert.Equal(t, 2, strings.Count(lines[8], "██"), "both stand on the bottom row")
	assert.Equal(t, 1, strings.Count(lines[1], "██"), "only the tall one reaches the top")
}

// A line is plotted in braille between its lowest and highest values, which label its axis.
func TestRender_PlotsALineBetweenItsExtremes(t *testing.T) {
	out := plain(Render("```chart\ntype: line\nunit: MB\ndata: [12, 20, 60]\n```\n", theme.Current(), 60))
	lines := strings.Split(out, "\n")
	assert.True(t, strings.HasPrefix(strings.TrimSpace(lines[0]), "60MB ┤"), lines[0])
	assert.True(t, strings.HasPrefix(strings.TrimSpace(lines[5]), "12MB ┤"), lines[5])
	dots := 0
	for _, r := range out {
		if r > 0x2800 && r <= 0x28FF {
			dots++
		}
	}
	assert.Greater(t, dots, 20, "the line is drawn")
}

// A share's strip is the whole width, split by value, and its legend says each part's share.
func TestRender_SharesAStripByValue(t *testing.T) {
	out := plain(Render("```chart\ntype: share\ndata: {decode: 3, sqlite: 1}\n```\n", theme.Current(), 42))
	lines := strings.Split(out, "\n")
	assert.Equal(t, 40, strings.Count(lines[0], "█"), "the strip fills the block's width")
	assert.Contains(t, lines[1], "75%")
	assert.Contains(t, lines[2], "25%")
}

// The split adds up exactly, and a part too small for a cell still gets one.
func TestApportion_AddsUpAndKeepsEveryPart(t *testing.T) {
	cuts := apportion([]point{{"big", 1000}, {"tiny", 1}, {"mid", 300}}, 1301, 30)
	sum := 0
	for _, n := range cuts {
		sum += n
		assert.Positive(t, n)
	}
	assert.Equal(t, 30, sum)
}

// A compare chart names the last series against the first, coloured by which way
// the block says is better, so the same drop reads as a win or a loss.
func TestRender_ColoursACompareByWhichWayIsBetter(t *testing.T) {
	block := func(better string) string {
		return Render("```chart\ntype: compare\nbetter: "+better+"\nseries: [before, after]\ndata:\n  replay: [300, 100]\n```\n",
			theme.Current(), 70)
	}
	safe := lipgloss.NewStyle().Foreground(theme.Current().Safe).Render("-67%")
	danger := lipgloss.NewStyle().Foreground(theme.Current().Danger).Render("-67%")
	assert.Contains(t, block("lower"), safe)
	assert.Contains(t, block("higher"), danger)
	assert.Contains(t, plain(block("lower")), "■ before  ■ after")

	out := plain(Render("```chart\ntype: compare\nbetter: smaller\ndata: {a: [1, 2]}\n```\n", theme.Current(), 70))
	assert.Contains(t, out, "better is lower or higher")
}

// Paths sharing directories are drawn under them, a lone directory chain as one
// path, and a note's counts coloured as a diff's.
func TestRender_DrawsPathsAsTheirTree(t *testing.T) {
	block := "```tree\ninternal/store/store.go  +40 -12  batch\ninternal/store/migrate.go  +3\nui/facts.go  -4\n```\n"
	out := plain(Render(block, theme.Current(), 80))
	for _, want := range []string{"internal/store/", "├── store.go", "└── migrate.go", "ui/", "└── facts.go"} {
		assert.Contains(t, out, want)
	}
	assert.NotContains(t, out, "internal/\n", "a directory holding one directory is one path")
	raw := Render(block, theme.Current(), 80)
	assert.Contains(t, raw, lipgloss.NewStyle().Foreground(theme.Current().Safe).Render("+40"))
	assert.Contains(t, raw, lipgloss.NewStyle().Foreground(theme.Current().Danger).Render("-12"))
}

// Steps are a timeline in the order written, each with its state's mark.
func TestRender_DrawsStepsAsATimeline(t *testing.T) {
	out := plain(Render("```steps\n- done: profile it\n- doing: batch the decodes\n- todo: measure on Windows\n```\n",
		theme.Current(), 60))
	assert.Equal(t, "  ● profile it\n  │\n  ◉ batch the decodes\n  │\n  ○ measure on Windows", out)

	bad := plain(Render("```steps\n- maybe: x\n```\n", theme.Current(), 100))
	assert.Contains(t, bad, `"maybe" is not done, doing, todo or failed`)
}

// A flow is boxes joined by arrows, across while it fits and down once it does not.
func TestRender_DrawsAFlowAcrossOrDown(t *testing.T) {
	block := "```flow\nstore -> decode → bus\n```\n"
	across := strings.Split(plain(Render(block, theme.Current(), 80)), "\n")
	require.Len(t, across, 3)
	assert.Equal(t, "  │ store │──▶│ decode │──▶│ bus │", across[1])

	down := plain(Render(block, theme.Current(), 20))
	assert.Contains(t, down, "▼")
	assert.Len(t, strings.Split(down, "\n"), 3*3+2*2, "three boxes, two arrows down")

	// Models write a long chain a step a line, as both live runs did.
	for _, written := range []string{"store\n  -> decode\n  -> bus\n", "store ->\ndecode ->\nbus\n"} {
		lines := strings.Split(plain(Render("```flow\n"+written+"```\n", theme.Current(), 80)), "\n")
		require.Len(t, lines, 3, "one chain, not three: %q", written)
		assert.Equal(t, "  │ store │──▶│ decode │──▶│ bus │", lines[1])
	}
}

// GitHub's alerts are drawn as callouts, and any other quote stays a quote.
func TestRender_DrawsAnAlertAsACallout(t *testing.T) {
	out := plain(Render("Before.\n\n> [!WARNING]\n> Not measured on **Windows**.\n\nAfter.\n", theme.Current(), 60))
	assert.Contains(t, out, "▌ ▲ Warning")
	assert.Contains(t, out, "▌ Not measured on Windows.")
	assert.Less(t, strings.Index(out, "Before."), strings.Index(out, "Warning"))
	assert.Less(t, strings.Index(out, "Windows."), strings.Index(out, "After."))

	out = plain(Render("> [!SHRUG]\n> just a quote\n", theme.Current(), 60))
	assert.NotContains(t, out, "▌")
	assert.Contains(t, out, "just a quote")
}

// A model puts its tiles under a key, as one did live, and they still draw, while a
// misspelt key inside them still says so.
func TestRender_DrawsStatsUnderAKey(t *testing.T) {
	out := plain(Render("```stats\ntiles:\n  - label: Go files\n    value: 323\n```\n", theme.Current(), 60))
	assert.Equal(t, "  323\n  Go files", out)
	bad := plain(Render("```stats\ntiles:\n  - lable: Go files\n```\n", theme.Current(), 120))
	assert.Contains(t, bad, "field lable not found")
}

// A tile's trend is a sparkline under its label.
func TestRender_DrawsATilesTrend(t *testing.T) {
	out := plain(Render("```stats\n- label: Replay\n  value: 1.2s\n  trend: [4, 3, 1]\n```\n", theme.Current(), 60))
	assert.Equal(t, "  1.2s\n  Replay\n  █▅▁", out)
}

// A table is drawn by the view, its columns of amounts each with a bar scaled to
// the column, and a column of row numbers left without one.
func TestRender_DrawsATableWithBarsForItsAmounts(t *testing.T) {
	table := "| # | Package | Lines | Notes |\n|---|---|---:|---|\n" +
		"| 1 | `ui/` | 18,008 | the **TUI** |\n| 2 | `internal/` | 34,511 | engine |\n"
	lines := strings.Split(plain(Render(table, theme.Current(), 80)), "\n")
	require.Len(t, lines, 4, "a header, a rule and two rows")
	assert.Contains(t, lines[1], "┼")
	assert.Contains(t, lines[2], "ui/", "a cell's markdown is drawn, not shown")
	assert.Contains(t, lines[2], "the TUI")
	assert.NotContains(t, lines[2], "`")
	assert.Equal(t, 6, strings.Count(lines[3], "█"), "the largest fills the bar")
	assert.Less(t, strings.Count(lines[2], "█"), 6)
	number, _, _ := strings.Cut(lines[2], "ui/")
	assert.NotContains(t, number, "█", "row numbers measure nothing")
}

// A table that cannot fit, however its text is cut, is left to glamour.
func TestDrawTable_GivesUpOnATableTooWideToFit(t *testing.T) {
	_, ok := drawTable("| a | b | c |\n|---|---|---|\n| 1 | 2 | 3 |\n", newStyles(theme.Current()), 6)
	assert.False(t, ok)
	_, ok = drawTable("| a | b |\n|---|\n| 1 | 2 |\n", newStyles(theme.Current()), 80)
	assert.False(t, ok, "a rule that does not match the header is not a table")
}

// Lists that are something else are drawn as it: task boxes as steps, measures in
// one unit as bars, and paths, one in a directory, as a tree.
func TestRender_DrawsAListAsWhatItIs(t *testing.T) {
	for name, c := range map[string]struct{ list, want, not string }{
		"tasks":      {"- [x] profile it\n- [ ] measure it\n", "● profile it\n  │\n  ○ measure it", "[x]"},
		"measures":   {"1. `view.go` — 167 lines\n2. `blocks.go` — 361 lines\n", "blocks.go █", "167 lines\n  2."},
		"paths":      {"- `ui/view/view.go` — frames\n- `ui/view/lift.go`: new\n", "├── view.go", "•"},
		"plain":      {"- go test ./... passed\n- nothing else changed\n", "• go test ./... passed", "█"},
		"two units":  {"- tests: 412\n- coverage: 81%\n", "• tests: 412", "█"},
		"flat paths": {"- `README.md` — updated\n- `go.mod` — bumped\n", "— updated", "├"},
		"named":      {"- **TUI:** Bubble Tea, Glamour\n- **Storage**: `modernc.org/sqlite`\n", "TUI      Bubble Tea, Glamour", "**"},
		"half named": {"- **TUI:** Bubble Tea\n- and a plain item\n", "• TUI: Bubble Tea", "TUI   "},
	} {
		t.Run(name, func(t *testing.T) {
			out := plain(Render(c.list, theme.Current(), 80))
			assert.Contains(t, out, c.want)
			assert.NotContains(t, out, c.not)
		})
	}
}

// A lifted piece sits one blank line from the next, as glamour spaces them, however
// glamour pads it on its own.
func TestRender_SpacesLiftedPiecesAsGlamourDoes(t *testing.T) {
	lines := strings.Split(plain(Render("# Title\n\nText.\n\n```\ncode\n```\n\nAfter.\n", theme.Current(), 40)), "\n")
	require.Len(t, lines, 7, "%q", lines)
	for _, i := range []int{1, 3, 5} {
		assert.Empty(t, lines[i], "line %d", i)
	}
}

// Only top-level markdown is lifted: a table or heading inside code stays the code it is.
func TestRender_LiftsNothingInsideCode(t *testing.T) {
	out := plain(Render("```\n# not a heading\n| a | b |\n|---|---|\n```\n", theme.Current(), 60))
	assert.Contains(t, out, "# not a heading")
	assert.Contains(t, out, "| a | b |")
}

// A bold amount counts up in place as it arrives, a version does not, and a heading
// and a code block arrive in their own ways, each resting as glamour draws it.
func TestLayout_MarkdownArrivesInItsOwnWays(t *testing.T) {
	text := "# Faster now\n\nFrom **1,240ms** on **v2.40.7**.\n\n```go\nfunc replayAllTheSessions() {}\n```\n"
	d := Layout(text, theme.Current(), 60)
	rest, mid := plain(d.Frame(Frames)), plain(d.Frame(Frames/2))
	assert.Contains(t, rest, "From 1,240ms on v2.40.7.")
	assert.NotContains(t, mid, "1,240ms", "it is still counting")
	assert.Regexp(t, `From +\d[\d,]*ms on v2\.40\.7\.`, mid, "in place, and the version left alone")
	at := strings.Index(rest, "ms on")
	for n := range Frames {
		if frame := plain(d.Frame(n)); strings.Contains(frame, "ms on") {
			assert.Equal(t, at, strings.Index(frame, "ms on"), "frame %d: the line moved as it counted", n)
		}
	}

	early := plain(d.Frame(5))
	assert.NotContains(t, early, "replayAllTheSessions() {}", "the code is still typing out")
	assert.Contains(t, rest, "replayAllTheSessions() {}")
	heading, _, _ := strings.Cut(d.Frame(4), "\n")
	resting, _, _ := strings.Cut(d.Frame(Frames), "\n")
	assert.NotEqual(t, resting, heading, "the heading shimmers")
	assert.Equal(t, strings.TrimSpace(plain(resting)), strings.TrimSpace(plain(heading)))
}

// Escape parameters are digits too, so counting must never reach inside one.
func TestOutsideEscapes_LeavesEscapesAlone(t *testing.T) {
	got := outsideEscapes("\x1b[38;2;12;3m12 of 12\x1b[m", func(s string) string { return strings.ReplaceAll(s, "12", "99") })
	assert.Equal(t, "\x1b[38;2;12;3m99 of 99\x1b[m", got)
}

// A diff is drawn as /review draws one: its file named, its header gone, and what it
// added and removed on their tints, read by marker since a model miscounts the header.
func TestRender_DrawsADiffAsTheReviewDoes(t *testing.T) {
	block := "```diff\n--- a/x.go\n+++ b/x.go\n@@ -1,1 +1,1 @@\n same()\n-old()\n+new()\n```\n"
	raw := strings.Split(Render(block, theme.Current(), 60), "\n")
	out := strings.Split(plain(strings.Join(raw, "\n")), "\n")
	require.Len(t, out, 5, "%q", out)
	assert.Equal(t, "  x.go", out[0], "the file, not its --- and +++ lines")
	assert.Contains(t, out[1], "@@ -1,1 +1,1 @@")
	assert.NotContains(t, raw[2], "\x1b[48", "what the change kept is untinted")
	assert.Contains(t, raw[3], bg(theme.Current().DiffRemoved), "past the one line the header counts")
	assert.Contains(t, raw[4], bg(theme.Current().DiffAdded))
	assert.Contains(t, out[4], "+new()")

	headless := plain(Render("```diff\n-a\n+b\n```\n", theme.Current(), 40))
	assert.Equal(t, "  -a\n  +b", headless, "a diff with no header still draws")
	half := plain(Render("```diff\n+++ ui/x.go\n-a\n+b\n```\n", theme.Current(), 40))
	assert.Equal(t, "  ui/x.go\n  -a\n  +b", half, "a model often names the file with +++ alone")
	removed := plain(Render("```diff\n--- a/x.sql\n+++ b/x.sql\n@@ -1 +1 @@\n--- a comment\n+++ added\n```\n", theme.Current(), 40))
	assert.Equal(t, "  x.sql\n  @@ -1 +1 @@\n  --- a comment\n  +++ added", removed, "past a hunk's header, --- is code")

	bad := plain(Render("```diff\njust some code\n```\n", theme.Current(), 80))
	assert.Contains(t, bad, "diff block not drawn: a diff adds or removes at least one line")
}

// What a diff added is written in as it arrives, while what it removed is there from the start.
func TestLayout_ADiffWritesInWhatItAdded(t *testing.T) {
	const old, nu = "-the old line, long enough to fill it", "+the new line, long enough to fill it"
	d := Layout("```diff\n"+old+"\n"+nu+"\n```\n", theme.Current(), 40)
	early := strings.Split(plain(d.Frame(4)), "\n")
	require.Len(t, early, 2)
	assert.Equal(t, "  "+old, early[0])
	assert.NotEmpty(t, early[1], "it has started")
	assert.NotContains(t, early[1], nu, "and is still being written")
	assert.Contains(t, plain(d.Frame(Frames)), nu)
}

// A cause is a card: the error and its stack, where, why, and the fix, which lands last.
func TestRender_DrawsACauseAsACard(t *testing.T) {
	block := "```cause\nfix: bound the loop\nerror: index out of range\n  goroutine 1\nat: `store.go:142`\nwhy: one past the end\n```\n"
	out := plain(Render(block, theme.Current(), 60))
	assert.Equal(t, "  ▌ ✗ index out of range\n  ▌   goroutine 1\n  ▌   at store.go:142\n  ▌\n"+
		"  ▌ why  one past the end\n  ▌ fix  bound the loop", out, "drawn in its order, not the order written")

	// Below a long reply the reveal passes the card at once, and the card holds its fix back.
	d := Layout(strings.Repeat("A line of the reply.\n\n", 30)+block+"\nAfter.\n", theme.Current(), 60)
	held := false
	for n := range Frames {
		frame := plain(d.Frame(n))
		held = held || strings.Contains(frame, "After.") && !strings.Contains(frame, "bound the loop")
	}
	assert.True(t, held, "the reveal has passed the card while its fix has still to land")

	for name, c := range map[string]struct{ body, want string }{
		"no error or why": {"at: x.go:1\nfix: y\n", "a cause says what failed"},
		"an unknown key":  {"reason: x\n", "line 1: start each line with error, at, why or fix"},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Contains(t, plain(Render("```cause\n"+c.body+"```\n", theme.Current(), 100)), c.want)
		})
	}
}

// Options sit side by side while each box has room, every box the same width and
// height, and the pick's star lands as the view does.
func TestRender_DrawsOptionsAsBoxes(t *testing.T) {
	block := "```options\npick: sqlite\n+ pure Go\n- slower\noption: cgo\n+ fast\n```\n"
	across := strings.Split(plain(Render(block, theme.Current(), 80)), "\n")
	require.Len(t, across, 4, "a top, two rows, a bottom: the shorter box padded to the taller")
	for _, line := range across {
		assert.Equal(t, ansi.StringWidth(across[0]), ansi.StringWidth(line), "every row as wide: %q", line)
	}
	assert.Contains(t, across[0], "╭ ★ sqlite ─")
	assert.Contains(t, across[1], "+ pure Go")
	assert.Contains(t, across[2], "− slower")

	down := plain(Render(block, theme.Current(), 40))
	assert.Len(t, strings.Split(down, "\n"), 8, "too narrow to sit side by side, they stack")

	assert.Contains(t, plain(Layout(block, theme.Current(), 80).Frame(6)), "☆ sqlite", "the star has not landed")
	assert.Contains(t, Render(block, theme.Current(), 80), lipgloss.NewStyle().Foreground(theme.Current().Accent).Render("╭ "),
		"the pick is framed in the accent")
	assert.Contains(t, plain(Render("```options\n+ fast\n```\n", theme.Current(), 80)), "start with pick or option")
}

// Facts line up on their names, a line under one continuing it, and type in as they arrive.
func TestRender_DrawsFactsAligned(t *testing.T) {
	block := "```facts\ngo: 1.26\nconfig: ~/.config/detent/config.yaml\n  and ./.detent.yaml\n```\n"
	assert.Equal(t, "  go      1.26\n  config  ~/.config/detent/config.yaml\n          and ./.detent.yaml",
		plain(Render(block, theme.Current(), 60)))
	assert.NotContains(t, plain(Layout(block, theme.Current(), 60).Frame(3)), "config.yaml", "still typing")
	assert.Contains(t, plain(Render("```facts\njust text\n```\n", theme.Current(), 80)), "each line is a name")
}

// A badge's value says how it is coloured, so passing is green and failing red, and
// badges wrap to the width rather than run past it.
func TestRender_ColoursBadgesByWhatTheySay(t *testing.T) {
	th := theme.Current()
	out := Render("```badges\nci: passing\nlint: failing\ngo: 1.26\n```\n", th, 80)
	assert.Contains(t, out, pillStyle(th.Background, th.Safe).Render(" passing "))
	assert.Contains(t, out, pillStyle(th.Background, th.Danger).Render(" failing "))
	assert.Contains(t, out, pillStyle(th.Background, th.Accent).Render(" 1.26 "))
	narrow := plain(Render("```badges\nalpha: one\nbeta: two\ngamma: three\n```\n", th, 24))
	assert.Len(t, strings.Split(narrow, "\n"), 3)
}

// Steps given as durations run one after another, steps given as [start, end] overlap,
// and as the chart arrives each fills only as far as its time has passed.
func TestRender_DrawsATimelineOnOneClock(t *testing.T) {
	block := "```chart\ntype: timeline\nunit: s\ndata:\n  build: 10\n  test: 30\n  lint: [10, 20]\n```\n"
	lines := strings.Split(plain(Render(block, theme.Current(), 60)), "\n")
	require.Len(t, lines, 4)
	start := func(l string) int { return strings.IndexAny(l, "█▏▎▍▌▋▊▉") }
	assert.Less(t, start(lines[0]), start(lines[1]), "test starts as build ends")
	assert.Equal(t, start(lines[1]), start(lines[2]), "lint starts with test, at 10s")
	assert.Contains(t, lines[3], "total")
	assert.Contains(t, lines[3], "40s")

	// Below a long reply the reveal passes the chart at once, leaving its clock to show.
	d, waited := Layout(strings.Repeat("A line of the reply.\n\n", 30)+block, theme.Current(), 60), false
	for n := range Frames {
		frame := strings.Split(plain(d.Frame(n)), "\n")
		build, test := frame[len(frame)-4], frame[len(frame)-3]
		waited = waited || strings.Contains(build, "█") &&
			!strings.ContainsAny(test, "█▏▎▍▌▋▊▉") && strings.HasSuffix(test, " 0s")
	}
	assert.True(t, waited, "test waits, at 0s, while build fills")

	bad := plain(Render("```chart\ntype: timeline\ndata: {a: [5, 1]}\n```\n", theme.Current(), 80))
	assert.Contains(t, bad, "a ends before it starts")
}

// A heatmap shades from its lowest value to its highest, never the lowest's shade for
// a value above it, and fills in a wave from the top left.
func TestRender_ShadesAHeatmap(t *testing.T) {
	block := "```chart\ntype: heatmap\nseries: [Mon, Tue]\ndata:\n  a: [0, 1]\n  b: [5, 2]\n```\n"
	lines := strings.Split(plain(Render(block, theme.Current(), 60)), "\n")
	require.Len(t, lines, 4)
	assert.Contains(t, lines[0], "Mon Tue")
	assert.Equal(t, "  a ··· ░░░", lines[1], "1 is above the lowest, so it shows")
	assert.Equal(t, "  b ███ ▒▒▒", lines[2])

	d, held := Layout(strings.Repeat("A line of the reply.\n\n", 30)+block, theme.Current(), 60), false
	for n := range Frames {
		frame := strings.Split(plain(d.Frame(n)), "\n")
		a, b := frame[len(frame)-3], frame[len(frame)-2]
		held = held || strings.Contains(a, "···") && strings.HasPrefix(b, "  b") && !strings.Contains(b, "▒▒▒")
	}
	assert.True(t, held, "the top left fills while the bottom right, on screen, waits")
}

// Each still block arrives in its own way: some frame shows what only its arrival can,
// and the view at rest shows none of it.
func TestLayout_StillBlocksArriveInTheirOwnWays(t *testing.T) {
	for name, c := range map[string]struct {
		block   string
		arrives func(frame string) bool
	}{
		"a status entry lands before its glyph does": {
			"```status\n- pass: a\n- fail: b\n- skip: c\n```\n",
			func(f string) bool { return strings.Contains(f, "· fail  b") },
		},
		"a step shows before it lands": {
			"```steps\n- done: one\n- done: two\n- done: three\n```\n",
			func(f string) bool { return strings.Contains(f, "○ two") },
		},
		"a tree's row shows before its note": {
			"```tree\na/one.go  +3\na/two.go  +4\na/three.go  +5\n```\n",
			func(f string) bool { return regexp.MustCompile(`(?m)one\.go$`).MatchString(f) },
		},
		"an alert's rail slides down before its text": {
			"> [!WARNING]\n> first line\n>\n> second paragraph\n\nAfter.\n",
			func(f string) bool { return strings.Contains(f, "After.") && !strings.Contains(f, "second paragraph") },
		},
		"a flow's arrow is drawn out before the next box": {
			"```flow\nalpha -> beta -> gamma\n```\n",
			func(f string) bool { return regexp.MustCompile(`(?m)│ alpha │──? *$`).MatchString(f) },
		},
	} {
		t.Run(name, func(t *testing.T) {
			d := Layout(c.block, theme.Current(), 60)
			assert.False(t, c.arrives(plain(d.Frame(Frames))), "at rest")
			seen := false
			for n := range Frames {
				seen = seen || c.arrives(plain(d.Frame(n)))
			}
			assert.True(t, seen)
		})
	}
}

// every is a view holding one of each kind of block, for what all of them must keep.
const every = sample + "```chart\ntype: columns\ndata: {a: 3, b: 9}\n```\n" +
	"```chart\ntype: line\ndata: [1, 5, 2, 8]\n```\n```chart\ntype: sparkline\ndata: [1, 5, 2]\n```\n" +
	"```chart\ntype: gauge\ndata: {cpu: 40}\n```\n```chart\ntype: share\ndata: {a: 2, b: 1}\n```\n" +
	"```chart\ntype: compare\nseries: [old, new]\ndata: {a: [3, 1]}\n```\n" +
	"```stats\n- label: Replay\n  value: 1,240ms\n  trend: [3, 1]\n```\n" +
	"```tree\na/b.go  +1\na/c.go\n```\n```steps\n- done: one\n- todo: two\n```\n" +
	"```flow\na -> b -> c\n```\n```flow\nalpha -> beta -> gamma -> delta -> epsilon -> zeta\n```\n" +
	"> [!NOTE]\n> noted\n\n# Heading\n\nFrom **3,550ms**.\n\n| a | n |\n|---|---|\n| x | 1 |\n| y | 20 |\n\n" +
	"- [x] done\n- [ ] not\n\n1. `a.go` — 3 lines\n2. `b.go` — 9 lines\n\n- `a/b.go` new\n- `a/c.go` gone\n\n" +
	"```go\nfunc f() {}\n```\n" +
	"```diff\n--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-old()\n+new()\n```\n" +
	"```cause\nerror: boom\nat: x.go:1\nwhy: because\nfix: this\n```\n" +
	"```options\npick: a\n+ good\noption: b\n- bad\n```\n```facts\ngo: 1.26\n```\n```badges\nci: passing\n```\n" +
	"```chart\ntype: timeline\ndata: {a: 1, b: [0, 3]}\n```\n```chart\ntype: heatmap\ndata: {a: [1, 2], b: [3]}\n```\n"

// A view arrives from nothing and rests as Render draws it, and no frame between
// changes its height, which would make the pane jump as it arrives.
func TestLayout_ArrivesWithoutChangingHeight(t *testing.T) {
	d := Layout(every, theme.Current(), 70)
	rest := d.Frame(Frames)
	assert.Equal(t, Render(every, theme.Current(), 70), rest)
	assert.NotContains(t, plain(rest), "not drawn", "every block in the fixture draws")
	assert.Empty(t, strings.TrimSpace(plain(d.Frame(0))), "the first frame shows nothing yet")
	height := strings.Count(rest, "\n")
	for n := range Frames {
		frame := d.Frame(n)
		assert.Equal(t, height, strings.Count(frame, "\n"), "frame %d", n)
		for line := range strings.SplitSeq(frame, "\n") {
			require.LessOrEqual(t, ansi.StringWidth(line), 70, "frame %d: %q", n, line)
		}
	}
	mid := plain(d.Frame(Frames / 2))
	assert.Contains(t, mid, "Fixed undo", "the top has arrived halfway through")
}

// A short view shows at once and leaves its blocks the arrival to grow in, rather than
// revealing them line by line until each has all but finished.
func TestLayout_AShortViewLeavesItsBlocksTimeToGrow(t *testing.T) {
	d := Layout("```chart\ntype: bar\ndata: {a: 3, b: 5, c: 10}\n```\n", theme.Current(), 40)
	full := strings.Count(plain(d.Frame(Frames)), "█")
	for n := range Frames {
		frame := plain(d.Frame(n))
		if strings.Contains(frame, "c ") {
			assert.Less(t, strings.Count(frame, "█"), full/2, "frame %d, the first with every bar on screen", n)
			return
		}
	}
	t.Fatal("the last bar never showed")
}

// A value counts up from zero written as it is at rest, so the tile never
// changes how it reads, only how far it has got.
func TestCountUp_KeepsHowAValueIsWritten(t *testing.T) {
	for _, c := range []struct {
		value string
		p     float64
		want  string
	}{
		{"1,240ms", 0.5, "620ms"},
		{"2,000,000", 0.5, "1,000,000"},
		{"$1,024", 0.5, "$512"},
		{"80%", 0.5, "40%"},
		{"4.20s", 0.5, "2.10s"},
		{"v1.2.3", 0.5, "v1.2.3"},
		{"passing", 0.5, "passing"},
		{"412", 1, "412"},
		{"412", 0, "0"},
	} {
		assert.Equal(t, c.want, countUp(c.value, c.p), "%s at %v", c.value, c.p)
	}
}

func TestTitle(t *testing.T) {
	for text, want := range map[string]string{
		"Some intro\n\n## Fixed undo\nmore": "Fixed undo",
		"\n\nJust an answer.\nSecond line":  "Just an answer.",
		"**Inspecting Go files**\n\nI need": "Inspecting Go files",
		"":                                  "",
	} {
		assert.Equal(t, want, Title(text), text)
	}
}

// Model text is input nothing controls: whatever it holds, Render returns,
// and no line is wider than asked.
func FuzzRender(f *testing.F) {
	f.Add(sample, 40)
	f.Add("```chart\ntype: sparkline\ndata: [1, 2, x]\n```", 10)
	f.Add("~~~stats\n- {label: a, value: 1}\n", 3)
	f.Add("```chart\ntype: compare\ndata: {a: [1, 1e15], b: [0]}\n```\n```chart\ntype: line\ndata: [-1e15, 1e15]\n```", 9)
	f.Add("```chart\ntype: columns\ndata: {a: 1, b: -2, c: 0}\n```\n```chart\ntype: share\ndata: {a: 0}\n```", 4)
	f.Add("```tree\na/b/c  +1 -2\na/\n/\n```\n```flow\na -> -> b\n```\n```steps\n- done:\n  - x\n```", 2)
	f.Add("> [!NOTE]\n> ```chart\n> type: bar\n\n> [!tip] inline", 5)
	f.Add("| a |\n|---|---|\n| 1 |\n\n|x|\n|-|\n|9|\n|1e99|\n\n| \\| | **b** |\n|:-:|--:|\n| `|` | 2 |\n", 7)
	f.Add("- [x]\n- [ ] \n1. `a/b` — 1\n2. [x/](y) —\n# \n**1,2,3** **9.**", 3)
	f.Add("```diff\n--- a\n+++ /dev/null\n@@\n\\ No newline\n+\n-\n--- x\n```\n```cause\nerror:\n  \nwhy:\n```", 2)
	f.Add("```options\npick:\n+\n-\n−x\noption: \n```\n```facts\n:\n a\n```\n```badges\n:\n- \n```", 1)
	f.Add("```chart\ntype: timeline\ndata: {a: 0, b: [0, 0], c: 1e15}\n```\n```chart\ntype: heatmap\nseries: []\ndata: {a: [7], b: [7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7]}\n```", 6)
	f.Fuzz(func(t *testing.T, text string, width int) {
		width = width%200 + 1
		if width < 1 {
			width += 200
		}
		d := Layout(text, theme.Current(), width)
		for _, n := range []int{0, 1, Frames / 2, Frames - 1, Frames} {
			for line := range strings.SplitSeq(d.Frame(n), "\n") {
				if ansi.StringWidth(line) > width {
					t.Fatalf("frame %d: a line %d wide at width %d: %q", n, ansi.StringWidth(line), width, line)
				}
			}
		}
	})
}

// bg is the escape that sets c as the background, as a diff's tint is set.
func bg(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r>>8, g>>8, b>>8)
}

func plain(s string) string {
	lines := strings.Split(ansi.Strip(s), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.Join(lines, "\n")
}
