// Package status renders run status: history-row badges and the bottom
// status bar. Inputs are plain primitives, and the judge's status and
// kind values are duplicated as literals to stay decoupled from event.
package status

import (
	"fmt"
	"strconv"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/vitzeno/detent/ui/theme"
)

// AttentionThreshold auto-expands a row at or above this attention score.
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

// RefreshStyles rebuilds status's styles from the current theme. Tool call
// it after theme.Apply.
func RefreshStyles() {
	muted = lipgloss.NewStyle().Foreground(theme.TextMuted)
	faint = lipgloss.NewStyle().Foreground(theme.TextFaint)
	safe = lipgloss.NewStyle().Foreground(theme.Safe)
	caution = lipgloss.NewStyle().Foreground(theme.Caution).Bold(true)
	danger = lipgloss.NewStyle().Foreground(theme.Danger).Bold(true)
	hint = lipgloss.NewStyle().Foreground(theme.TextFaint).Italic(true)
}

// Row is one history row's render inputs.
type Row struct {
	Running   bool
	HasResult bool
	ExitCode  int
	Summary   string // a one-line digest of the result
	LiveLines int
	Dropped   int
	Judged    bool
	Status    string // the judge's status, or ""
	Attention float64
	Err       bool // it could not run, as opposed to ran and failed
	NoVerdict bool // nothing will ever judge it
}

// Badge splits status into icon and unstyled detail: exit-code styling
// first, judged styling once the verdict lands.
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
		icon = safe.Render("✓")
		if s.ExitCode != 0 || s.Err {
			icon = danger.Render("✗")
		}
		// Nothing judges a command the human ran.
		if s.NoVerdict {
			return icon, s.Summary
		}
		return icon, s.Summary + " · judging…"
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
	default:
		return "output"
	}
}

// Notice is the one-shot flash at the end of the bar: what just
// happened and whether it worked.
type Notice struct {
	Text string
	Bad  bool
}

// Bar renders the bottom line: spinner and phase when busy, keys,
// and a one-shot notice styled so outcome reads without the wording.
func Bar(spinner, phase, keys string, n Notice, waiting bool) string {
	line := hint.Render(keys)
	if n.Text != "" {
		mark, style := "✓", safe
		if n.Bad {
			mark, style = "✗", danger
		}
		line += hint.Render(" · ") + style.Render(mark+" "+n.Text)
	}
	if waiting {
		return fmt.Sprintf("  %s %s   %s", spinner, muted.Render(phase), line)
	}
	return fmt.Sprintf("  %s   %s", muted.Render(phase), line)
}

// Dur compacts a duration for status lines: 412ms, 3.2s, 2m10s.
func Dur(d time.Duration) string {
	if d >= time.Second {
		// Rounded first, so 59.96s reads 1m0s rather than 60.0s.
		d = d.Round(100 * time.Millisecond)
	}
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
		return strconv.Itoa(n)
	case n < 1000*1000-50: // past that it would print 1000.0k
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
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
