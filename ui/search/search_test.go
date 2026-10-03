package search

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFuzzy_Matches(t *testing.T) {
	tests := []struct {
		name, query, text string
		ok                bool
		pos               []int
	}{
		{"an empty query matches anything", "", "ls -la", true, nil},
		{"a subsequence", "gst", "git status", true, []int{0, 4, 5}},
		{"case is ignored for a lower-case query", "readme", "cat README.md", true, []int{4, 5, 6, 7, 8, 9}},
		{"an upper-case letter makes it exact", "Readme", "cat README.md", false, nil},
		{"order matters", "tsg", "git status", false, nil},
		{"every term must match", "git zzz", "git status", false, nil},
		{"terms match anywhere, in any order", "status git", "git status", true, []int{0, 1, 2, 4, 5, 6, 7, 8, 9}},
		{"the tightest window wins", "ab", "a xxxx ab", true, []int{7, 8}},
		{"offsets are runes, not bytes", "é", "café au lait", true, []int{3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, ok := Fuzzy(tt.query, tt.text)
			require.Equal(t, tt.ok, ok)
			if ok {
				assert.Equal(t, tt.pos, h.Pos)
			}
		})
	}
}

// What a human types is a word's start or a run, so those must outrank the
// same letters scattered.
func TestFuzzy_RanksWordStartsAndRunsFirst(t *testing.T) {
	ranked := []string{
		"go test ./...",
		"git status --short | grep test",
		"golangci-lint run --timeout 5m",
	}
	var prev int
	for i, text := range ranked {
		h, ok := Fuzzy("gt", text)
		require.True(t, ok, text)
		if i > 0 {
			assert.Greater(t, prev, h.Score, "%q should rank above %q", ranked[i-1], text)
		}
		prev = h.Score
	}
	run, _ := Fuzzy("tls", "redis-cli --tls info")
	spread, _ := Fuzzy("tls", "the last step")
	assert.Greater(t, run.Score, spread.Score)
}

func TestLines_FindsTheFirstLineHoldingEveryTerm(t *testing.T) {
	text := "# Server\nredis_version:7.2.4\ntls-port:6380\nTLS replication: off"
	line, h, ok := Lines("tls port", text)
	require.True(t, ok)
	assert.Equal(t, 2, line)
	assert.Equal(t, []int{0, 1, 2, 4, 5, 6, 7}, h.Pos)

	line, _, ok = Lines("TLS", text)
	require.True(t, ok)
	assert.Equal(t, 3, line, "an upper-case query skips the lower-case line")

	_, _, ok = Lines("tls 7.2", text)
	assert.False(t, ok, "terms on different lines are not one hit")
	_, _, ok = Lines("", text)
	assert.False(t, ok, "an empty query matches no line of output")
}

// A lower-cased rune can change width, so offsets come from comparing runes,
// never from indexing a lowered copy.
func TestLines_OffsetsSurviveRunesThatLowerToAnotherWidth(t *testing.T) {
	line := "İstanbul tls"
	_, h, ok := Lines("tls", line)
	require.True(t, ok)
	runes := []rune(line)
	assert.Equal(t, "tls", string([]rune{runes[h.Pos[0]], runes[h.Pos[1]], runes[h.Pos[2]]}))
}

func FuzzMatch(f *testing.F) {
	f.Add("gst", "git status")
	f.Add("İ", "İİ\n i")
	f.Add("a b", "\xff\xfe a\nb")
	f.Fuzz(func(t *testing.T, query, text string) {
		if h, ok := Fuzzy(query, text); ok {
			checkPos(t, h.Pos, utf8.RuneCountInString(text))
		}
		if line, h, ok := Lines(query, text); ok {
			lines := strings.Split(text, "\n")
			require.Less(t, line, len(lines))
			checkPos(t, h.Pos, utf8.RuneCountInString(lines[line]))
		}
	})
}

func checkPos(t *testing.T, pos []int, n int) {
	t.Helper()
	for i, p := range pos {
		require.True(t, p >= 0 && p < n, "offset %d outside %d runes", p, n)
		if i > 0 {
			require.Greater(t, p, pos[i-1], "offsets ascend without repeats")
		}
	}
}
