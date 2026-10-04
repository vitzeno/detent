package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

// Asking for a skill by hand asks the model to load it, so it shows and
// replays as a call to the skill tool, like one the model chose.
func TestSkill_ByHandAsksTheModelToLoadIt(t *testing.T) {
	bus := event.New()
	asked, unsub := bus.Subscribe(event.Only(event.SubmitPromptKind))
	defer unsub()
	m := withSkills(t, bus)

	_, cmd := m.runSlash("/release v2.1 for the mobile app")
	runCmd(cmd)
	select {
	case rec := <-asked:
		assert.Equal(t, "Load the release skill and follow it. The request: v2.1 for the mobile app",
			rec.Event.(event.SubmitPrompt).Text)
	case <-time.After(2 * time.Second):
		t.Fatal("/release published nothing")
	}
}

func TestSkill_OffersOnlyWhatTheHumanMayAskFor(t *testing.T) {
	m := withSkills(t, event.New())
	var names []string
	for _, c := range matchSlash("/", m.skillCmds) {
		names = append(names, c.name)
	}
	assert.Contains(t, names, "/release")
	assert.NotContains(t, names, "/tidy", "a model-only skill has no command")
	assert.Equal(t, 1, strings.Count(strings.Join(names, " "), "/help "), "a skill cannot take a built-in's name")

	c, ok := lookupSlash("/help", m.skillCmds)
	require.True(t, ok)
	assert.Equal(t, "show slash commands", c.desc)
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
	_, cmd := c.run(New(t.Context(), bus, SessionInfo{}), "/release")
	runCmd(cmd)
	select {
	case rec := <-asked:
		assert.Equal(t, "Load the Release skill and follow it.", rec.Event.(event.SubmitPrompt).Text)
	case <-time.After(2 * time.Second):
		t.Fatal("/release published nothing")
	}
}

func TestSkillsPage_SaysWhereEachCameFrom(t *testing.T) {
	m := withSkills(t, event.New())
	got := stripANSI(strings.Join(m.skillLines(), "\n"))
	assert.Contains(t, got, "release  project · /release")
	assert.Contains(t, got, "tidy  personal · model only")
	assert.Contains(t, got, "Cut a release")

	empty := New(t.Context(), event.New(), SessionInfo{})
	assert.Contains(t, stripANSI(strings.Join(empty.skillLines(), "\n")), "none found")
}

// A skill can be named anywhere in a request, and the whole sentence is still
// the request. Only a skill: a built-in mid-sentence is just text.
func TestSkill_NamedMidSentenceIsLoaded(t *testing.T) {
	m := withSkills(t, event.New())
	m.skillCmds = append(m.skillCmds, skillCommands([]event.SkillSummary{
		{Name: "notes", Description: "Write notes", UserInvocable: true}})...)
	tests := []struct{ in, want string }{
		{"can you /release cut v2.1 now", "Load the release skill and follow it. The request: can you /release cut v2.1 now"},
		{"use /release, then /notes.", "Load the release and notes skills and follow them. The request: use /release, then /notes."},
		{"what is in /usr/bin", "what is in /usr/bin"},
		{"then /undo it", "then /undo it"},
	}
	for _, tt := range tests {
		bus := event.New()
		asked, unsub := bus.Subscribe(event.Only(event.SubmitPromptKind))
		m.bus = bus
		m.prompt.SetValue(tt.in)
		_, cmd := m.submit()
		runCmd(cmd)
		select {
		case rec := <-asked:
			assert.Equal(t, tt.want, rec.Event.(event.SubmitPrompt).Text, tt.in)
		case <-time.After(2 * time.Second):
			t.Fatalf("%q published nothing", tt.in)
		}
		unsub()
	}
}

// Typing a / later in a sentence offers skills, and enter completes the word
// rather than sending half a request.
func TestSkill_OfferedMidSentence(t *testing.T) {
	k := newKeyed(t)
	k.m.apply(event.SessionStarted{Skills: []event.SkillSummary{
		{Name: "release", Description: "Cut a release", UserInvocable: true}}})
	k.m.prompt.SetValue("please /re")
	k.m.prompt.rematch()
	require.True(t, k.m.prompt.Open())
	var names []string
	for _, c := range k.m.prompt.matches {
		names = append(names, c.name)
	}
	assert.Equal(t, []string{"/release"}, names, "/rename is a built-in, not offered mid-sentence")

	k.press(t, "enter")
	assert.Equal(t, "please /release ", k.m.prompt.Value(), "enter completed the word")
	select {
	case rec := <-k.seen:
		t.Fatalf("enter mid-sentence sent %s", rec.Event.Kind())
	case <-time.After(50 * time.Millisecond):
	}
}

func withSkills(t *testing.T, bus *event.Bus) Model {
	t.Helper()
	m := New(t.Context(), bus, SessionInfo{})
	m.layout.width, m.layout.height, m.layout.outputColW = 120, 40, 100
	m.apply(event.SessionStarted{Skills: []event.SkillSummary{
		{Name: "help", Description: "would shadow /help", UserInvocable: true},
		{Name: "release", Description: "Cut a release", Project: true, UserInvocable: true},
		{Name: "tidy", Description: "Tidy imports"},
	}})
	return m
}
