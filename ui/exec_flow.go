package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/vitzeno/detent/ui/editor"
)

func (m Model) onStream(msg StreamEvent) (tea.Model, tea.Cmd) {
	for _, b := range m.blocks {
		for _, r := range b.steps {
			if r.cmd.running {
				if len(r.cmd.live) < maxLiveLines {
					prefix := ""
					if msg.Stderr {
						prefix = "(stderr) "
					}
					r.cmd.live = append(r.cmd.live, prefix+msg.Line)
				} else {
					r.cmd.dropped++
				}
				// Keep waiting on the channel while a command runs.
				if m.abort != nil {
					return m, streamWaitCmd(m.streamCh, m.ctx)
				}
				return m, nil
			}
		}
	}
	return m, nil
}

func (m Model) onExecDone(msg execDoneMsg) (tea.Model, tea.Cmd) {
	m.waiting = false
	m.abort = nil
	if m.cur == nil {
		return m, nil
	}
	row := m.cur.steps[len(m.cur.steps)-1]
	row.cmd.running = false
	if msg.err != nil {
		m.cur.ended = true
		m.cur.end = m.cur.res.End
		m.cur.fatalErr = msg.err
		m.cur = nil
		m = m.backToInput()
		return m, nil
	}
	row.cmd.ec = msg.ec
	if row.editPath != "" {
		// Read from disk, not msg.ec.Result.Stdout: a heredoc or
		// redirect usually prints nothing.
		content, truncated, maxBytes, err := m.sess.ReadFile(row.editPath)
		ed := editor.New(row.editPath, content, truncated, maxBytes, err)
		row.editor = &ed
	}
	cmds := []tea.Cmd{
		judgeCmd(m.ctx, m.sess, m.cur.goal, row.command, msg.ec.Result, row),
		proposeCmd(m.ctx, m.sess, m.cur.goal),
	}
	m.waiting = true
	return m, tea.Batch(append(cmds, m.spinner.Tick)...)
}
