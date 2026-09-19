package status

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBadge(t *testing.T) {
	icon, detail := Badge(Row{Running: true, LiveLines: 7, Dropped: 2}, "…")
	assert.Equal(t, "…", icon)
	assert.Contains(t, detail, "7 live lines")

	icon, detail = Badge(Row{}, "…")
	assert.Contains(t, icon, "○")
	assert.Equal(t, "pending", detail)

	icon, detail = Badge(Row{HasResult: true, ExitCode: 0, Summary: "exit 0, 3 lines"}, "…")
	assert.Contains(t, icon, "✓")
	assert.Contains(t, detail, "judging…")

	icon, detail = Badge(Row{HasResult: true, ExitCode: 1, Summary: "exit 1, 0 lines"}, "…")
	assert.Contains(t, icon, "✗")

	icon, detail = Badge(Row{Judged: true, Status: "clean_success"}, "…")
	assert.Contains(t, icon, "✓")
	assert.Equal(t, "clean", detail)

	icon, detail = Badge(Row{Judged: true, Status: "failed", Attention: 0.9}, "…")
	assert.Contains(t, icon, "⚠")
	assert.Contains(t, detail, "0.90")
}

func TestBar(t *testing.T) {
	out := Bar("…", "running…", "[q] quit", "", true)
	assert.Contains(t, out, "running…")
	assert.Contains(t, out, "[q] quit")

	out = Bar("", "idle", "[tab] history", "abort sent", false)
	assert.Contains(t, out, "abort sent")
}

func TestDurTokens(t *testing.T) {
	assert.Equal(t, "412ms", Dur(412*time.Millisecond))
	assert.Equal(t, "3.2s", Dur(3200*time.Millisecond))
	assert.Equal(t, "2m10s", Dur(130*time.Second))
	assert.Equal(t, "847", Tokens(847))
	assert.Equal(t, "9.4k", Tokens(9400))
	assert.Equal(t, "2.1M", Tokens(2100000))
}
