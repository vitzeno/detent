package ui

import (
	"fmt"

	"github.com/vitzeno/detent/ui/status"
)

// The frame around the panes, in the order it appears on screen: the
// session bar across the top, each pane's own header, and the status
// line above the input bar.

func (m Model) sessionBar() string {
	jev := styleFaint.Render("jev ○ off")
	if m.info.Judge != "" {
		jev = styleSafe.Render("jev ● " + m.info.Judge)
	}
	goals := 0
	for _, b := range m.blocks {
		if b.ended && b.tool == "" {
			goals++
		}
	}
	if m.cur != nil {
		goals++
	}
	snap := m.sess.UsageSnapshot()
	usage := styleFaint.Render(fmt.Sprintf("⏱ %s · %stok",
		status.Dur(snap.MachineTime()), status.Tokens(snap.ProposerTokens+snap.JudgeTokens)))
	return fmt.Sprintf("%s %s · %d goal(s) · %d cmd(s)  %s  %s  %s",
		styleBrand.Render("◆ detent v2"), styleFaint.Render(m.info.Proposer),
		goals, m.totalCmds, usage, runModeBadge(m.info.RunMode), jev)
}

// viewportHeader names what the output pane is currently showing: a
// file being edited, a tool, or a command's output by judged kind.
func (m Model) viewportHeader() string {
	active := m.nav.focus == focusOutput
	r := m.focused()
	if r == nil {
		return fmt.Sprintf("%s %s", paneMark(active), paneLabel("detent", active))
	}
	if r.editor != nil && r.editor.Err() == nil {
		label := "file"
		if m.save.editing {
			label = "editing — ctrl+s save"
		}
		dirty := ""
		if r.editor.Dirty() {
			dirty = styleCaution.Render(" ●")
		}
		return fmt.Sprintf("%s %s — %s%s", paneMark(active), paneLabel(label, active),
			styleGoal.Render(truncateWidth(r.editor.Path, m.layout.outputColW-28)), dirty)
	}
	if r.toolKind != "" {
		return fmt.Sprintf("%s %s — %s", paneMark(active), paneLabel(r.toolKind, active),
			styleGoal.Render(truncateWidth(r.command, m.layout.outputColW-24)))
	}
	label := "output"
	if k := rowKind(r); k != "" {
		label = status.KindLabel(string(k))
	}
	return fmt.Sprintf("%s %s — %s", paneMark(active), paneLabel(label, active),
		styleGoal.Render(truncateWidth(r.command, m.layout.outputColW-24)))
}

func (m Model) historyHeader() string {
	return fmt.Sprintf("%s %s", paneMark(m.nav.focus == focusHistory), paneLabel("history", m.nav.focus == focusHistory))
}

func (m Model) statusLine() string {
	phase := "idle"
	if m.waiting {
		phase = "thinking…"
		for _, b := range m.blocks {
			for _, r := range b.steps {
				if r.cmd.running {
					phase = "running…"
				}
			}
		}
	}
	return status.Bar(m.spinner.View(), phase, m.statusHint(),
		status.Notice{Text: m.notice.text, Bad: m.notice.bad}, m.waiting)
}

// statusHint mirrors handleKey's owner() so the hint never falls out of
// sync with what actually routes the keystroke.
func (m Model) statusHint() string {
	if m.mode == modeSaveConfirm {
		return "[y/enter] save · [n] keep editing"
	}
	if m.save.editing {
		return "[ctrl+s] save · [esc] done editing"
	}
	switch m.owner() {
	case ownerConfirm:
		return "[y/enter] run · [n] stop goal"
	case ownerOutput:
		// Must match onEscape's actual behavior (keys.go): it aborts a
		// running command here instead of stepping back to history.
		esc := "[esc] history"
		if m.abort != nil {
			esc = "[esc] abort"
		}
		return "[tab] input · [↑/↓] inside · " + esc + " · [q] quit"
	case ownerHistory:
		// While a goal is in flight the input is blurred and focus sits
		// here, so this is the only hint a waiting human sees — esc is
		// the one thing that stops it, and omitting it read as "you
		// can't abort a thinking model".
		if m.abort != nil {
			return "[esc] abort · [tab] output · [↑/↓] move · [space] expand · [q] quit"
		}
		return "[tab] output · [↑/↓] move · [space] expand · [enter] expand · [q] quit"
	case ownerBusy:
		if m.prompt.Open() {
			return "[↑/↓] pick · [tab] complete · [enter] run · [esc] close"
		}
		return "[tab] history · type / + enter for commands · [esc] abort"
	default: // ownerInput
		if m.prompt.Open() {
			return "[↑/↓] pick · [tab] complete · [enter] run · [esc] close"
		}
		return "[tab] history · [enter] run · [" + m.prompt.NewlineKey() + "] newline · type / for cmds"
	}
}

// runModeBadge says where commands actually run. Host is called out
// rather than left implicit: it's the unsandboxed case.
func runModeBadge(mode string) string {
	if mode == "sandbox" {
		return styleSafe.Render("sandbox ●")
	}
	return styleCaution.Render("host ⚠ unsandboxed")
}

func paneMark(active bool) string {
	if active {
		return styleBrand.Render("●")
	}
	return styleFaint.Render("○")
}

func paneLabel(name string, active bool) string {
	if active {
		return styleBrand.Render(name)
	}
	return styleFaint.Render(name)
}
