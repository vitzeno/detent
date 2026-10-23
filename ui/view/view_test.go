package view

import (
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

// A tile's trend is a sparkline under its label.
func TestRender_DrawsATilesTrend(t *testing.T) {
	out := plain(Render("```stats\n- label: Replay\n  value: 1.2s\n  trend: [4, 3, 1]\n```\n", theme.Current(), 60))
	assert.Equal(t, "  1.2s\n  Replay\n  █▅▁", out)
}

// every is a view holding one of each kind of block, for what all of them must keep.
const every = sample + "```chart\ntype: columns\ndata: {a: 3, b: 9}\n```\n" +
	"```chart\ntype: line\ndata: [1, 5, 2, 8]\n```\n```chart\ntype: sparkline\ndata: [1, 5, 2]\n```\n" +
	"```chart\ntype: gauge\ndata: {cpu: 40}\n```\n```chart\ntype: share\ndata: {a: 2, b: 1}\n```\n" +
	"```chart\ntype: compare\nseries: [old, new]\ndata: {a: [3, 1]}\n```\n" +
	"```stats\n- label: Replay\n  value: 1,240ms\n  trend: [3, 1]\n```\n" +
	"```tree\na/b.go  +1\na/c.go\n```\n```steps\n- done: one\n- todo: two\n```\n" +
	"```flow\na -> b -> c\n```\n```flow\nalpha -> beta -> gamma -> delta -> epsilon -> zeta\n```\n" +
	"> [!NOTE]\n> noted\n"

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

func plain(s string) string {
	lines := strings.Split(ansi.Strip(s), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.Join(lines, "\n")
}
