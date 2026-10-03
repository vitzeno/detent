// Package status renders run status: history-row badges and the bottom
// status bar. Inputs are plain values, the judge's verdict in event's types.
package status

import (
	"fmt"
	"strconv"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/ui/theme"
)

// AttentionThreshold auto-expands a row at or above this attention score.
const AttentionThreshold = 0.7

var muted, faint, safe, caution, danger, hint = bake(theme.Current())

// RefreshStyles rebuilds status's styles from the current theme. Call
// it after theme.Apply.
func RefreshStyles() {
	muted, faint, safe, caution, danger, hint = bake(theme.Current())
}

func bake(p theme.Theme) (muted, faint, safe, caution, danger, hint lipgloss.Style) {
	return lipgloss.NewStyle().Foreground(p.TextMuted),
		lipgloss.NewStyle().Foreground(p.TextFaint),
		lipgloss.NewStyle().Foreground(p.Safe),
		lipgloss.NewStyle().Foreground(p.Caution).Bold(true),
		lipgloss.NewStyle().Foreground(p.Danger).Bold(true),
		lipgloss.NewStyle().Foreground(p.TextFaint).Italic(true)
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
	Status    event.Status // the judge's status, or ""
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
	v, ok := verdicts[s.Status]
	if !ok {
		v = verdict{"done", "○", &muted}
	}
	if s.Attention >= AttentionThreshold {
		return caution.Render("⚠"), v.word + fmt.Sprintf(" · attention %.2f", s.Attention)
	}
	return v.style.Render(v.glyph), v.word
}

// verdict is how one judged status reads. The style is a pointer because
// RefreshStyles reassigns the styles after this table is built.
type verdict struct {
	word, glyph string
	style       *lipgloss.Style
}

var verdicts = map[event.Status]verdict{
	event.StatusClean:    {"clean", "✓", &safe},
	event.StatusWarnings: {"warnings", "⚠", &caution},
	event.StatusFailed:   {"failed", "✗", &danger},
	event.StatusEmpty:    {"no output", "○", &muted},
}

// kindLabels names the render kinds worth naming. The rest read as output.
var kindLabels = map[event.RenderKind]string{
	event.RendersTable:   "table",
	event.RendersError:   "errors",
	event.RendersDiff:    "diff",
	event.RendersJSON:    "json",
	event.RendersContent: "file",
	event.RendersFiles:   "files",
}

// KindLabel names a render kind for the viewport header.
func KindLabel(k event.RenderKind) string {
	if label, ok := kindLabels[k]; ok {
		return label
	}
	return "output"
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
