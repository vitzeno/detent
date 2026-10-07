package defuse

import (
	"os/exec"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A proposed command is model text, so nothing in it may drive the
// terminal on the very row a human reads to approve it.
func TestText_DefusesControls(t *testing.T) {
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
		{"bidi isolate", "a\u2066b", `a\u2066b`},
		{"right-to-left mark", "a\u200fb", `a\u200fb`},
		{"Arabic letter mark", "a\u061cb", `a\u061cb`},
		{"zero-width space", "a\u200bb", `a\u200bb`},
		{"line separator", "a\u2028b", `a\u2028b`},
		{"paragraph separator", "a\u2029b", `a\u2029b`},
		{"a lone C1 byte, not a rune", "a\x9bb", `a\x9bb`},
		{"an emoji joiner stays", "👩\u200d💻", "👩\u200d💻"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Text(tt.in))
		})
	}
}

// Command output keeps its colours, and loses anything else an escape can do.
func TestStyled_KeepsColourOnly(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"colour passes", "\x1b[31mred\x1b[0m", "\x1b[31mred\x1b[0m"},
		{"256 and true colour pass", "\x1b[38;5;208mo\x1b[38:2::1:2:3mx", "\x1b[38;5;208mo\x1b[38:2::1:2:3mx"},
		{"cursor movement is shown", "\x1b[2Jcleared", "^[[2Jcleared"},
		{"a clipboard write is shown", "\x1b]52;c;aGk=\x07", "^[]52;c;aGk=^G"},
		{"a hyperlink is shown", "\x1b]8;;https://x\x1b\\y", "^[]8;;https://x^[\\y"},
		{"an unfinished SGR is shown", "\x1b[31", "^[[31"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Styled(tt.in))
		})
	}
}

// Whatever goes in, what comes out is valid UTF-8 with nothing that acts
// on a terminal, bar a newline and, for Styled, a colour.
func FuzzText(f *testing.F) {
	for _, s := range []string{"ls", "\x1b[31mx", "\x9b", "a\u202eb", "\x1b]52;c;x\x07", "\u2028"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for _, out := range []string{Text(s), withoutSGR(Styled(s))} {
			require.True(t, utf8.ValidString(out), "%q", out)
			for _, r := range out {
				require.False(t, needsEscape(r), "%q kept %U", out, r)
			}
		}
	})
}

// withoutSGR drops the colour sequences Styled may keep.
func withoutSGR(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if n := sgrLen(s[i:]); n > 0 {
			i += n
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// headless shares it with the TUI, and needs nothing from outside the standard library.
func TestPackage_DependsOnStdlibOnly(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "github.com/vitzeno/detent/defuse").Output()
	require.NoError(t, err)
	for dep := range strings.FieldsSeq(string(out)) {
		// Stdlib paths have no dot in their first segment.
		first, _, _ := strings.Cut(dep, "/")
		if strings.Contains(first, ".") {
			require.Equal(t, "github.com/vitzeno/detent/defuse", dep, "defuse must import stdlib only")
		}
	}
}
