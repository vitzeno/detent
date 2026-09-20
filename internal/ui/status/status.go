// Package status renders run status: history-row badges and the bottom
// status bar. Inputs are plain primitives, never a core-package struct.
// Badge/KindLabel/statusWord switch on the same Status*/Kind* values
// ui.PostJudgment carries, duplicated here as literals rather than
// imported, to stay decoupled from the core harness.
package status

import (
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/vitzeno/detent/internal/ui/theme"
)

// AttentionThreshold auto-expands a row at or above this Noul.
const AttentionThreshold = 0.7

var (
	muted   lipgloss.Style
	faint   lipgloss.Style
	safe    lipgloss.Style
	caution lipgloss.Style
	danger  lipgloss.Style
	hint    lipgloss.Style
)

func init() {
	RefreshStyles()
}

// RefreshStyles rebuilds status's styles from the current theme — call
// after theme.Apply.
func RefreshStyles() {
	muted = lipgloss.NewStyle().Foreground(theme.TextMuted)
	faint = lipgloss.NewStyle().Foreground(theme.TextFaint)
	safe = lipgloss.NewStyle().Foreground(theme.Safe)
	caution = lipgloss.NewStyle().Foreground(theme.Caution).Bold(true)
	danger = lipgloss.NewStyle().Foreground(theme.Danger).Bold(true)
	hint = lipgloss.NewStyle().Foreground(theme.TextFaint).Italic(true)
}

// Row is one history step's render inputs.
type Row struct {
	Running   bool
	HasResult bool
	ExitCode  int
	Summary   string // e.g. shell.Result.Summary()
	LiveLines int
	Dropped   int
	Judged    bool
	Status    string // agent Status* value, or ""
	Attention float64
}

// Badge splits status into icon and detail text: exit-code styling
// first, judged styling once the post batch lands. Detail is unstyled;
// the caller composes `icon command detail`.
func Badge(s Row, spinner string) (icon, detail string) {
	if s.Running {
		detail = fmt.Sprintf("%d live lines", s.LiveLines)
		if s.Dropped > 0 {
			detail += fmt.Sprintf(" (+%d dropped)", s.Dropped)
		}
		return spinner, detail
	}
	if !s.Judged {
		if !s.HasResult {
			return faint.Render("○"), "pending"
		}
		if s.ExitCode == 0 {
			return safe.Render("✓"), s.Summary + " · judging…"
		}
		return danger.Render("✗"), s.Summary + " · judging…"
	}
	detail = statusWord(s.Status)
	if s.Attention >= AttentionThreshold {
		return caution.Render("⚠"), detail + fmt.Sprintf(" · attention %.2f", s.Attention)
	}
	switch s.Status {
	case "clean_success":
		return safe.Render("✓"), detail
	case "success_with_warnings":
		return caution.Render("⚠"), detail
	case "failed":
		return danger.Render("✗"), detail
	default:
		return muted.Render("○"), detail
	}
}

// KindLabel names a render kind for the viewport header.
func KindLabel(k string) string {
	switch k {
	case "table":
		return "table"
	case "error_text":
		return "errors"
	case "diff":
		return "diff"
	case "structured_json":
		return "json"
	case "file_content":
		return "file"
	case "file_listing":
		return "files"
	case "scrollable_log":
		return "log"
	default:
		return "output"
	}
}

func statusWord(s string) string {
	switch s {
	case "clean_success":
		return "clean"
	case "success_with_warnings":
		return "warnings"
	case "failed":
		return "failed"
	case "empty":
		return "no output"
	default:
		return "done"
	}
}

// Bar renders the bottom status line: spinner plus phase when busy,
// contextual keys, and a one-shot notice.
func Bar(spinner, phase, keys, notice string, waiting bool) string {
	if notice != "" {
		keys += " · " + notice
	}
	if waiting {
		return fmt.Sprintf("  %s %s   %s", spinner, muted.Render(phase), hint.Render(keys))
	}
	return fmt.Sprintf("  %s   %s", muted.Render(phase), hint.Render(keys))
}

// Dur compacts a duration for status lines: 412ms, 3.2s, 2m10s.
func Dur(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	m := int(d.Minutes())
	return fmt.Sprintf("%dm%ds", m, int(d.Seconds())-60*m)
}

// Tokens compacts a token count: 847, 9.4k, 2.1M.
func Tokens(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 1000*1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
}
