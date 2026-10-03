package layout

import (
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
)

func TestSplit(t *testing.T) {
	tests := []struct {
		name    string
		total   int
		weights []int
		min     int
		want    []int
	}{
		{"even split, no remainder", 100, []int{1, 1}, 0, []int{50, 50}},
		{"3:2 weighting, no remainder", 100, []int{3, 2}, 0, []int{60, 40}},
		{"remainder goes to earliest shares", 101, []int{3, 2}, 0, []int{61, 40}},
		{"three-way remainder distributes one each", 10, []int{1, 1, 1}, 0, []int{4, 3, 3}},
		{"min floors a share below its weighted size", 20, []int{1, 9}, 5, []int{5, 15}},
		{"min can make the sum exceed total", 5, []int{1, 1}, 10, []int{10, 10}},
		{"zero weights fall back to an even split", 10, []int{0, 0}, 0, []int{5, 5}},
		{"single weight takes everything", 42, []int{1}, 0, []int{42}},
		{"no weights returns nil", 42, nil, 0, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Split(tt.total, tt.weights, tt.min)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSplit_SumsToTotalWhenUnconstrained(t *testing.T) {
	for _, total := range []int{0, 1, 7, 13, 100, 137} {
		for _, weights := range [][]int{{1, 1}, {3, 2}, {1, 1, 1}, {5, 3, 2}} {
			got := Split(total, weights, 0)
			sum := 0
			for _, v := range got {
				sum += v
			}
			assert.Equal(t, total, sum, "total=%d weights=%v", total, weights)
		}
	}
}

func TestRow_JoinsLeftToRight(t *testing.T) {
	// Same visual row: "a"/"c" and "b"/"d" must each share a line, not stack.
	assert.Equal(t, "ac\nbd", Row("a\nb", "c\nd"))
}

// Every caller places this inside a frame, so a newline would put the
// tail outside it.
func TestTruncate_ReturnsOneLine(t *testing.T) {
	const heredoc = "python3 - <<'EOF'\nimport re\np = 'ui/goal_flow.go'\nEOF"

	assert.Equal(t, "python3 - <<'EOF' import re p = 'ui/goal_flow.go' EOF",
		Truncate(heredoc, 200), "short enough to keep, still one line")
	assert.Equal(t, "python3 - <<'EOF' import…", Truncate(heredoc, 25))

	for _, w := range []int{4, 10, 25, 200} {
		assert.NotContains(t, Truncate(heredoc, w), "\n", "width %d", w)
	}
}

// Runs of whitespace collapse rather than each becoming a space, so an
// indented script does not turn into a line of gaps.
func TestTruncate_CollapsesIndentation(t *testing.T) {
	assert.Equal(t, "if x: return", Truncate("if x:\n    return", 40))
	assert.Equal(t, "a b", Truncate("\n\na\t\tb\n", 40), "and the edges go")
	assert.Equal(t, "grep -n  two  spaces", Truncate("grep -n  two  spaces", 40),
		"but one line is left exactly as the command was written")
}

// Runes, not bytes: cutting mid-character draws a replacement glyph,
// and measuring in bytes cuts a wide string far too short.
func TestTruncate_CutsRunesNotBytes(t *testing.T) {
	const s = "ßßßßßßßßßß" // ten runes, twenty bytes
	assert.Equal(t, s, Truncate(s, 10), "ten runes fit in ten columns")
	assert.Equal(t, "ßßßß…", Truncate(s, 5))
	assert.True(t, utf8.ValidString(Truncate(s, 7)))
}

// Cells, not runes: a wide rune takes two, and the frame budgets cells.
func TestTruncate_FitsCells(t *testing.T) {
	for _, s := range []string{"日本語日本語日本語", "👍👍👍👍👍👍👍👍", "👍🏽👍🏽👍🏽👍🏽👍🏽", "ééééééééé"} {
		for _, w := range []int{4, 6, 9} {
			got := Truncate(s, w)
			assert.LessOrEqual(t, ansi.StringWidth(got), w, "%q at %d", s, w)
			assert.True(t, utf8.ValidString(got))
		}
	}
	assert.Equal(t, "日本…", Truncate("日本語日本語", 6))
}

// A proposed command is model text, so nothing in it may drive the
// terminal on the very row a human reads to approve it.
func TestPrintable_DefusesControls(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"plain text is untouched", "ls -la", "ls -la"},
		{"newlines stay", "a\nb", "a\nb"},
		{"tabs become spaces", "a\tb", "a    b"},
		{"escape is shown, not sent", "\x1b[31mred\x1b[0m", "^[[31mred^[[0m"},
		{"carriage return cannot overwrite", "rm -rf ~\rls", "rm -rf ~^Mls"},
		{"delete", "a\x7fb", "a^?b"},
		{"C1 introducer", "a\u009bb", `a\u009bb`},
		{"bidi override", "a\u202eb", `a\u202eb`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Printable(tt.in))
		})
	}
}

func TestTruncate_NeverPassesAnEscapeThrough(t *testing.T) {
	got := Truncate("\x1b[31mredredredred\x1b[0m", 6)
	assert.NotContains(t, got, "\x1b")
	assert.Equal(t, "^[[31…", got)
	assert.Equal(t, "a    b", Truncate("a\tb", 40))
}
