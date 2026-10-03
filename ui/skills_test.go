package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

// Asking for a skill by hand asks the model to load it, so it shows and
// replays as a skill Call like one the model chose.
func TestSkill_ByHandAsksTheModelToLoadIt(t *testing.T) {
	bus := event.New()
	asked, unsub := bus.Subscribe(event.Only(event.SubmitPromptKind))
	defer unsub()
	m := withSkills(bus)

	_, cmd := m.runSlash("/release v2.1 for the mobile app")
	run(cmd)
	select {
	case rec := <-asked:
		assert.Equal(t, "Load the release skill and follow it. The request: v2.1 for the mobile app",
			rec.Event.(event.SubmitPrompt).Text)
	case <-time.After(2 * time.Second):
		t.Fatal("/release published nothing")
	}
}

func TestSkill_OffersOnlyWhatTheHumanMayAskFor(t *testing.T) {
	m := withSkills(event.New())
	var names []string
	for _, c := range matchSlash("/", m.skillCmds) {
		names = append(names, c.Name)
	}
	assert.Contains(t, names, "/release")
	assert.NotContains(t, names, "/tidy", "a model-only skill has no command")
	assert.Equal(t, 1, strings.Count(strings.Join(names, " "), "/help "), "a skill cannot take a built-in's name")

	c, ok := lookupSlash("/help", m.skillCmds)
	require.True(t, ok)
	assert.Equal(t, "show slash commands", c.Desc)
}

// Typed words are matched lowercased, so a capitalised skill must still
// be reachable, while the model is asked for it by its real name.
func TestSkill_ACapitalisedNameIsReachable(t *testing.T) {
	cmds := skillCommands([]event.SkillSummary{{Name: "Release", UserInvocable: true}})
	c, ok := lookupSlash("/Release", cmds)
	require.True(t, ok)
	assert.Len(t, matchSlash("/rel", cmds), 1)

	bus := event.New()
	asked, unsub := bus.Subscribe(event.Only(event.SubmitPromptKind))
	defer unsub()
	_, cmd := c.run(New(context.Background(), bus, SessionInfo{}), "/release")
	run(cmd)
	select {
	case rec := <-asked:
		assert.Equal(t, "Load the Release skill and follow it.", rec.Event.(event.SubmitPrompt).Text)
	case <-time.After(2 * time.Second):
		t.Fatal("/release published nothing")
	}
}

func TestSkillsPage_SaysWhereEachCameFrom(t *testing.T) {
	m := withSkills(event.New())
	got := stripANSI(strings.Join(m.skillLines(), "\n"))
	assert.Contains(t, got, "release  project · /release")
	assert.Contains(t, got, "tidy  personal · model only")
	assert.Contains(t, got, "Cut a release")

	empty := New(context.Background(), event.New(), SessionInfo{})
	assert.Contains(t, stripANSI(strings.Join(empty.skillLines(), "\n")), "none found")
}

func withSkills(bus *event.Bus) Model {
	m := New(context.Background(), bus, SessionInfo{})
	m.layout.width, m.layout.height, m.layout.outputColW = 120, 40, 100
	m.apply(event.SessionStarted{Skills: []event.SkillSummary{
		{Name: "help", Description: "would shadow /help", UserInvocable: true},
		{Name: "release", Description: "Cut a release", Project: true, UserInvocable: true},
		{Name: "tidy", Description: "Tidy imports"},
	}})
	return m
}

// run executes a command and any it batches, as the runtime would.
func run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range batch {
			run(c)
		}
	}
}
