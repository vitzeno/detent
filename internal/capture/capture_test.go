package capture

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func scan(t *testing.T, in string, limit int) (string, []string, bool) {
	t.Helper()
	var buf bytes.Buffer
	events := make(chan StreamEvent, 1024)
	truncated, err := ScanCapped(strings.NewReader(in), false, &buf, limit, events)
	require.NoError(t, err)
	close(events)
	var lines []string
	for e := range events {
		lines = append(lines, e.Line)
	}
	return buf.String(), lines, truncated
}

func TestScanCapped(t *testing.T) {
	long := strings.Repeat("x", maxLineBytes+10)
	cases := []struct {
		name      string
		in        string
		limit     int
		buf       string
		lines     []string
		truncated bool
	}{
		{name: "under", in: "a\nb\n", limit: 10, buf: "a\nb\n", lines: []string{"a", "b"}},
		{name: "exactly at the limit", in: "abc\n", limit: 4, buf: "abc\n", lines: []string{"abc"}},
		{name: "over", in: "abc\ndef\n", limit: 5, buf: "abc\nd", lines: []string{"abc", "def"}, truncated: true},
		{name: "no final newline", in: "a\nnonl", limit: 64, buf: "a\nnonl\n", lines: []string{"a", "nonl"}},
		{name: "crlf", in: "a\r\nb\r\n", limit: 64, buf: "a\nb\n", lines: []string{"a", "b"}},
		{name: "empty", in: "", limit: 64},
		{name: "a rune straddling the cap", in: "aé\n", limit: 2, buf: "a", lines: []string{"aé"}, truncated: true},
		{name: "longer than the read buffer", in: strings.Repeat("y", readSize*2) + "\nz\n", limit: readSize * 4,
			buf: strings.Repeat("y", readSize*2) + "\nz\n", lines: []string{strings.Repeat("y", readSize*2), "z"}},
		{name: "a line too long to carry does not end the scan", in: long + "\nafter\n", limit: 4,
			buf: "xxxx", lines: []string{long[:maxLineBytes], "after"}, truncated: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			buf, lines, truncated := scan(t, c.in, c.limit)
			assert.Equal(t, c.buf, buf)
			assert.Equal(t, c.lines, lines)
			assert.Equal(t, c.truncated, truncated)
		})
	}
}

func TestScanCapped_NilEventsStillCaptures(t *testing.T) {
	var buf bytes.Buffer
	truncated, err := ScanCapped(strings.NewReader("one\ntwo\n"), true, &buf, 64, nil)
	require.NoError(t, err)
	assert.False(t, truncated)
	assert.Equal(t, "one\ntwo\n", buf.String())
}

func TestScanCapped_MarksStderr(t *testing.T) {
	events := make(chan StreamEvent, 4)
	var buf bytes.Buffer
	_, err := ScanCapped(strings.NewReader("e\n"), true, &buf, 64, events)
	require.NoError(t, err)
	assert.Equal(t, StreamEvent{Stderr: true, Line: "e"}, <-events)
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestScanCapped_ReportsAReadError(t *testing.T) {
	var buf bytes.Buffer
	_, err := ScanCapped(io.MultiReader(strings.NewReader("ok\n"), failingReader{}), false, &buf, 64, nil)
	require.ErrorContains(t, err, "broken pipe")
	assert.Equal(t, "ok\n", buf.String())
}

func TestClip(t *testing.T) {
	assert.Equal(t, "short", Clip("short", 10))

	s := strings.Repeat("h", 100) + strings.Repeat("é", 200) + strings.Repeat("t", 100)
	got := Clip(s, 100)
	assert.True(t, utf8.ValidString(got), "a cut must land on a rune boundary")
	assert.True(t, strings.HasPrefix(got, "hhh"))
	assert.True(t, strings.HasSuffix(got, "ttt"), "the tail is where a failure usually is")
	assert.Contains(t, got, "[truncated]")
	assert.Less(t, len(got), 140)
}

func FuzzScanCapped(f *testing.F) {
	f.Add("a\nb\n", 3)
	f.Add("é\r\nx", 1)
	f.Fuzz(func(t *testing.T, in string, limit int) {
		limit = max(limit%256, 0)
		var buf bytes.Buffer
		_, err := ScanCapped(strings.NewReader(in), false, &buf, limit, nil)
		require.NoError(t, err)
		assert.LessOrEqual(t, buf.Len(), limit)
	})
}
