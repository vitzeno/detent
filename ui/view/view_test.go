package view

import (
	"strings"
	"testing"

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
	f.Fuzz(func(t *testing.T, text string, width int) {
		width = width%200 + 1
		if width < 1 {
			width += 200
		}
		for line := range strings.SplitSeq(Render(text, theme.Current(), width), "\n") {
			if ansi.StringWidth(line) > width {
				t.Fatalf("a line %d wide at width %d: %q", ansi.StringWidth(line), width, line)
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
