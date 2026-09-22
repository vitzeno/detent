package ui

import (
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/version"
)

// /status answers "what am I running and what has it done", which the
// welcome pane answers before anything has run and nothing answered
// after.
func TestStatus_ReportsTheBuildAndTheSession(t *testing.T) {
	m, _ := usageModel()
	nm, _ := m.runSlash("/status")
	m = nm.(Model)

	body := strings.Join(m.panelLines(), "\n")
	assert.Contains(t, body, version.Number, "which build this is")
	assert.Contains(t, body, "test-model", "what it proposes with")
	assert.Contains(t, body, "unsandboxed", "where commands run")
	assert.Contains(t, body, "commands", "and what the session has done")
	assert.Contains(t, body, "views")
}

// A panel replaces a panel rather than stacking, so /usage after
// /status leaves one thing open and esc closes it.
func TestPanel_OneAtATime(t *testing.T) {
	m, _ := usageModel()
	nm, _ := m.runSlash("/status")
	nm, _ = nm.(Model).runSlash("/usage")
	m = nm.(Model)

	assert.Equal(t, panelUsage, m.panel.kind)
	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.False(t, nm.(Model).panel.open())
}

// A panel is a page, so it takes the whole pane and says how to leave.
func TestPanel_HeaderSaysHowToClose(t *testing.T) {
	m, _ := usageModel()
	nm, _ := m.runSlash("/status")
	m = nm.(Model)

	require.Contains(t, m.viewportHeader(), "status")
	assert.Contains(t, m.viewportHeader(), "esc")
}

// The counts are the point of /status, so they have to move when
// something happens rather than only being rendered.
func TestStatus_CountsWhatHappened(t *testing.T) {
	m := testUIModel()
	m.counts.failed = 2
	m.countView(ViewGenerated)
	m.countView(ViewGenerated)
	m.countView(ViewSaved)
	m.countView(ViewDeclined)
	m.countView(ViewBuiltin) // detent's own, counted as neither

	assert.Equal(t, 2, m.counts.composed)
	assert.Equal(t, 1, m.counts.saved)
	assert.Equal(t, 1, m.counts.viewDeclined)
	assert.Equal(t, 0, m.counts.shipped)

	nm, _ := m.runSlash("/status")
	body := strings.Join(nm.(Model).panelLines(), "\n")
	assert.Contains(t, body, "1 saved · 0 shipped")
}

// A width verb counts the escape bytes in a rendered string, so the
// labels have to be padded before they are styled. They were not, and
// every value started at a different column.
func TestStatus_LabelsLineUp(t *testing.T) {
	m, _ := usageModel()
	m.info.Judge = "jev-1.13.0"
	nm, _ := m.runSlash("/status")

	// Two known rows with labels of very different lengths: unpadded,
	// their values would start eight columns apart.
	var proposer, machine string
	for _, line := range nm.(Model).panelLines() {
		plain := ansi.Strip(line)
		switch {
		case strings.Contains(plain, "test-model"):
			proposer = plain
		case strings.Contains(plain, "cpu"):
			machine = plain
		}
	}
	require.NotEmpty(t, proposer)
	require.NotEmpty(t, machine)
	assert.Equal(t,
		strings.Index(proposer, "test-model"),
		strings.Index(machine, runtime.GOOS),
		"both values start at the same column")
}
