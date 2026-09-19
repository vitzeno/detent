// Package status renders run status: history-row badges and the bottom
// status bar. It takes plain data (never step or session types) and
// returns fully styled strings, so Jev's verdicts become color in one
// place. Callers map kinds and judgments onto Row/Bar inputs.
package status

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"

	"github.com/vitzeno/detent/internal/agentloop"
	"github.com/vitzeno/detent/internal/ui/theme"
)

// AttentionThreshold auto-expands a row at or above this Noul.
const AttentionThreshold = 0.7

var (
	muted   = lipgloss.NewStyle().Foreground(theme.TextMuted)
	faint   = lipgloss.NewStyle().Foreground(theme.TextFaint)
	safe    = lipgloss.NewStyle().Foreground(theme.Safe)
	caution = lipgloss.NewStyle().Foreground(theme.Caution).Bold(true)
	danger  = lipgloss.NewStyle().Foreground(theme.Danger).Bold(true)
	hint    = lipgloss.NewStyle().Foreground(theme.TextFaint).Italic(true)
)

// Row is one history step's render inputs.
type Row struct {
	Running   bool
	HasResult bool
	ExitCode  int
	Summary   string // e.g. shell.Result.Summary()
	LiveLines int
	Dropped   int
	Judged    bool
	Status    string // agentloop Status* value, or ""
	Attention float64
}

// Badge splits a row's status into icon and detail text: provisional
// exit-code styling first, judged styling once the post batch lands.
// The caller composes `icon command detail`; detail arrives unstyled.
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
	case agentloop.StatusClean:
		return safe.Render("✓"), detail
	case agentloop.StatusWarnings:
		return caution.Render("⚠"), detail
	case agentloop.StatusFailed:
		return danger.Render("✗"), detail
	default:
		return muted.Render("○"), detail
	}
}

func statusWord(s string) string {
	switch s {
	case agentloop.StatusClean:
		return "clean"
	case agentloop.StatusWarnings:
		return "warnings"
	case agentloop.StatusFailed:
		return "failed"
	case agentloop.StatusEmpty:
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
