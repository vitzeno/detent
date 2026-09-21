package viewgen_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/usage"
	"github.com/vitzeno/detent/internal/viewgen"
	"github.com/vitzeno/detent/viewspec"
)

// Long enough to be worth a model call: a handful of lines is not,
// whatever shape it is.
var goTest = func() string {
	out := ""
	for i := range 10 {
		status := "ok  "
		if i == 3 {
			status = "FAIL"
		}
		out += fmt.Sprintf("%s\tgithub.com/x/p%02d\t%d.412s\n", status, i, i)
	}
	return out
}()

const goodSpec = `{"version":1,"match":"","parse":{"kind":"lines",
"pattern":"^(?P<status>ok|FAIL)\\s+(?P<pkg>\\S+)\\s+(?P<secs>[\\d.]+)s"},
"blocks":[{"kind":"meter","title":"passed","count_where":"status=ok","of":"*"},
{"kind":"table","columns":[{"field":"status"},{"field":"pkg"}]}]}`

// fakeModel replies with each canned answer in turn.
type fakeModel struct {
	replies []string
	calls   int
	schema  map[string]any
	err     error
}

func (m *fakeModel) Structured(_ context.Context, _, _ string, schema map[string]any) ([]byte, usage.Usage, error) {
	m.schema = schema
	if m.err != nil {
		return nil, usage.Usage{}, m.err
	}
	reply := m.replies[min(m.calls, len(m.replies)-1)]
	m.calls++
	return []byte(reply), usage.Usage{PromptTokens: 10, CompletionTokens: 5}, nil
}

type fakeJudge struct {
	scores []float64
	calls  int
}

func (j *fakeJudge) Ask(context.Context, classify.State, classify.Questions) (classify.Answers, classify.Usage, error) {
	s := j.scores[min(j.calls, len(j.scores)-1)]
	j.calls++
	return classify.Answers{"view_fit": {Noul: s}}, classify.Usage{}, nil
}

// An unseeded command, so the generator actually runs: anything
// detent ships a spec for is answered by Existing instead.
func request() viewgen.Request {
	return viewgen.Request{Command: "pytest -q", Output: goTest, Kind: "plain_text"}
}

func TestGenerate_AuthorsValidatesAndCaches(t *testing.T) {
	dir := t.TempDir()
	model := &fakeModel{replies: []string{goodSpec}}
	g := &viewgen.Generator{Model: model, Store: &viewgen.Store{Dir: dir},
		Candidates: 1}

	got, err := g.Generate(context.Background(), request())
	require.NoError(t, err)
	require.NotNil(t, got.Spec)
	assert.Equal(t, viewgen.SourceGenerated, got.Source)
	assert.Equal(t, "pytest", got.Spec.Match, "stamped with the command shape it serves")
	assert.Equal(t, 10, got.Usage.PromptTokens)

	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Contains(t, files[0].Name(), "pytest", "the file names itself readably")

	// A second call never reaches the model.
	again, err := g.Generate(context.Background(), request())
	require.NoError(t, err)
	assert.Equal(t, viewgen.SourceSaved, again.Source)
	assert.Zero(t, again.Usage.PromptTokens, "a spec that already exists costs nothing")
	assert.Equal(t, 1, model.calls)
}

// A spec that decodes but cannot draw this output is not a view.
func TestGenerate_RejectsWhatCannotDrawTheOutput(t *testing.T) {
	tests := []struct{ name, reply string }{
		{"not JSON at all", `sure! here is your view:`},
		{"a kind that does not exist", `{"version":1,"parse":{"kind":"none"},
			"blocks":[{"kind":"hologram"}]}`},
		{"a field the parse never produced", `{"version":1,
			"parse":{"kind":"lines","pattern":"^(?P<status>ok|FAIL)"},
			"blocks":[{"kind":"list","field":"elapsed"}]}`},
		{"a pattern that matches nothing", `{"version":1,
			"parse":{"kind":"lines","pattern":"^(?P<nope>zzzz)$"},
			"blocks":[{"kind":"list","field":"nope"}]}`},
		{"a pattern that does not compile", `{"version":1,
			"parse":{"kind":"lines","pattern":"^(?P<a>"},"blocks":[{"kind":"log"}]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := &viewgen.Generator{Model: &fakeModel{replies: []string{tc.reply}},
				Store: &viewgen.Store{Dir: t.TempDir()}, Candidates: 1}
			_, err := g.Generate(context.Background(), request())
			assert.ErrorIs(t, err, viewgen.ErrNoneFit)
		})
	}
}

func TestGenerate_JevDecidesBetweenCandidatesAndCanRejectBoth(t *testing.T) {
	plainer := `{"version":1,"parse":{"kind":"none"},"blocks":[{"kind":"log"}]}`

	g := &viewgen.Generator{
		Model: &fakeModel{replies: []string{plainer, goodSpec}},
		Judge: &fakeJudge{scores: []float64{0.2, 0.9}},
		Store: &viewgen.Store{Dir: t.TempDir()}, Candidates: 2}
	got, err := g.Generate(context.Background(), request())
	require.NoError(t, err)
	assert.Equal(t, 0.9, got.Fit)
	assert.Len(t, got.Spec.Blocks, 2, "the better-judged candidate won")

	// Both below threshold and the output stays plain text.
	g = &viewgen.Generator{
		Model: &fakeModel{replies: []string{goodSpec}},
		Judge: &fakeJudge{scores: []float64{0.1}},
		Store: &viewgen.Store{Dir: t.TempDir()}, Candidates: 1}
	_, err = g.Generate(context.Background(), request())
	assert.ErrorIs(t, err, viewgen.ErrNoneFit)
}

// A diff already draws itself; there is no view to gain from one.
func TestGenerate_SkipsShapesWithNothingToGain(t *testing.T) {
	for _, kind := range []string{"diff"} {
		model := &fakeModel{replies: []string{goodSpec}}
		g := &viewgen.Generator{Model: model, Store: &viewgen.Store{Dir: t.TempDir()}}
		req := request()
		req.Kind = kind
		_, err := g.Generate(context.Background(), req)
		assert.ErrorIs(t, err, viewgen.ErrNotWorth, kind)
		assert.Zero(t, model.calls, "and never asks")
	}
}

// The model is only shown the kinds that suit what Jev said this is.
func TestGenerate_PrunesTheVocabularyByRenderKind(t *testing.T) {
	model := &fakeModel{replies: []string{goodSpec}}
	g := &viewgen.Generator{Model: model, Store: &viewgen.Store{Dir: t.TempDir()}, Candidates: 1}
	req := request()
	req.Kind = "file_listing"
	_, _ = g.Generate(context.Background(), req)

	blocks := model.schema["properties"].(map[string]any)["blocks"].(map[string]any)
	kinds := blocks["items"].(map[string]any)["properties"].(map[string]any)["kind"].(map[string]any)["enum"].([]string)
	assert.Contains(t, kinds, "tree")
	assert.Contains(t, kinds, "list")
	assert.NotContains(t, kinds, "diff", "a file listing has no use for a diff")
	assert.NotContains(t, kinds, "code")
	assert.Less(t, len(kinds), len(viewspec.Standard().Kinds()))
}

func TestGenerate_ErrorsFromTheModelAreNotFatal(t *testing.T) {
	g := &viewgen.Generator{Model: &fakeModel{err: errors.New("endpoint down")},
		Store: &viewgen.Store{Dir: t.TempDir()}, Candidates: 2}
	_, err := g.Generate(context.Background(), request())
	assert.ErrorIs(t, err, viewgen.ErrNoneFit, "a dead endpoint costs the view, not the step")
}

func TestKey_SharesAShapeButNotAKind(t *testing.T) {
	assert.Equal(t,
		viewgen.Key("git status --porcelain=v1 --branch", "file_listing"),
		viewgen.Key("git status -sb", "file_listing"),
		"flags do not change the view")
	assert.NotEqual(t,
		viewgen.Key("ls", "file_listing"),
		viewgen.Key("ls -la", "table"),
		"but a different output shape needs a different parse")
	assert.Equal(t, "ps", viewgen.Normalise("ps -U someone"))
	assert.Equal(t, "go test", viewgen.Normalise("go test -run X ./..."))
}

func TestStore_TreatsUnreadableCacheAsAMiss(t *testing.T) {
	dir := t.TempDir()
	s := &viewgen.Store{Dir: dir}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "k.json"), []byte("{oh no"), 0o644))
	_, ok := s.Load("k")
	assert.False(t, ok, "a corrupt spec regenerates rather than failing")

	var nilStore *viewgen.Store
	_, ok = nilStore.Load("k")
	assert.False(t, ok)
	assert.NoError(t, nilStore.Save("k", &viewspec.Spec{}), "no store is not an error")
}

// A spec on disk is meant to be read and fixed by hand.
func TestStore_WritesSomethingAHumanCanEdit(t *testing.T) {
	dir := t.TempDir()
	s := &viewgen.Store{Dir: dir}
	spec := &viewspec.Spec{Version: 1, Match: "go test",
		Parse:  viewspec.Parse{Kind: "none"},
		Blocks: []viewspec.Block{{Kind: "log"}}}
	require.NoError(t, s.Save("k", spec))

	raw, err := os.ReadFile(s.Path("k"))
	require.NoError(t, err)
	assert.Contains(t, string(raw), "\n  \"match\": \"go test\"", "indented, not minified")

	var back viewspec.Spec
	require.NoError(t, json.Unmarshal(raw, &back))
	assert.Equal(t, *spec, back)
}

// Shipped specs are seeds, not defaults: an edited one on disk wins.
func TestExisting_StoreBeatsWhatWeShipped(t *testing.T) {
	dir := t.TempDir()
	g := &viewgen.Generator{Store: &viewgen.Store{Dir: dir}}
	req := viewgen.Request{Command: "go test ./...", Kind: "plain_text"}

	got, ok := g.Existing(req)
	require.True(t, ok, "a seed serves before anything is generated")
	assert.Equal(t, viewgen.SourceShipped, got.Source)
	assert.Len(t, got.Spec.Blocks, 2, "the shipped go test view")

	edited := &viewspec.Spec{Version: 1, Match: "go test",
		Parse:  viewspec.Parse{Kind: "none"},
		Blocks: []viewspec.Block{{Kind: "log"}}}
	require.NoError(t, g.Store.Save(viewgen.Key(req.Command, req.Kind), edited))

	got, ok = g.Existing(req)
	require.True(t, ok)
	assert.Equal(t, viewgen.SourceSaved, got.Source, "and says so, since it is editable")
	assert.Len(t, got.Spec.Blocks, 1, "the human's edit wins")
}

func TestSeeds_AllCompileAndFitTheirOwnShape(t *testing.T) {
	for _, command := range []string{
		"go test ./...", "git status", "docker ps", "env", "find .", "tree", "ps",
	} {
		got, ok := (&viewgen.Generator{}).Existing(viewgen.Request{Command: command})
		require.True(t, ok, command)
		_, err := viewspec.Compile(*got.Spec)
		assert.NoError(t, err, command)
	}
}

// One table defines a kind's criteria and what may draw it, so a kind
// cannot be judged into existence with nothing able to render it.
func TestKinds_CriteriaAndWidgetsComeFromOneTable(t *testing.T) {
	criteria := viewgen.RenderKindCriteria()
	require.Len(t, criteria, len(viewgen.Kinds()))

	known := viewspec.Standard().Kinds()
	for _, k := range viewgen.Kinds() {
		assert.NotEmpty(t, k.What, k.Name)
		assert.NotEmpty(t, k.NotFor, "%s names what it is confused with", k.Name)
		assert.NotEmpty(t, k.Examples, k.Name)
		assert.Contains(t, criteria, k.Name)

		require.NotEmpty(t, k.Widgets, "%s has something able to draw it", k.Name)
		for _, w := range k.Widgets {
			assert.Contains(t, known, w, "%s may draw with %s", k.Name, w)
		}
		if k.Generate {
			assert.Less(t, len(k.Widgets), len(known),
				"%s narrows the vocabulary rather than offering all of it", k.Name)
		}
	}
}

// A shipped spec is the floor, not the ceiling: a seeded command still
// gets the model's attempt, and only falls back when it comes to
// nothing. Checking seeds first made those commands ungeneratable.
func TestGenerate_AShippedSpecDoesNotShadowTheModel(t *testing.T) {
	seeded := viewgen.Request{Command: "go test ./...", Output: goTest, Kind: "plain_text"}

	model := &fakeModel{replies: []string{goodSpec}}
	g := &viewgen.Generator{Model: model, Store: &viewgen.Store{Dir: t.TempDir()}, Candidates: 1}
	got, err := g.Generate(context.Background(), seeded)
	require.NoError(t, err)
	assert.Equal(t, viewgen.SourceGenerated, got.Source, "the model was asked")
	assert.Positive(t, model.calls)

	// When nothing generated survives, the shipped spec catches it.
	g = &viewgen.Generator{Model: &fakeModel{replies: []string{`not a spec`}},
		Store: &viewgen.Store{Dir: t.TempDir()}, Candidates: 1}
	got, err = g.Generate(context.Background(), seeded)
	require.NoError(t, err)
	assert.Equal(t, viewgen.SourceShipped, got.Source, "and the floor held")
	assert.Len(t, got.Spec.Blocks, 2, "detent's own go test view")

	// With no model at all, Existing is the whole of views: saved.
	out, ok := (&viewgen.Generator{}).Existing(seeded)
	require.True(t, ok)
	assert.Equal(t, viewgen.SourceShipped, out.Source)
}

// Length is a property of the output, not of its shape, so it gates
// the model call rather than hiding inside a render kind.
func TestGenerate_ShortOutputIsNotWorthAModelCall(t *testing.T) {
	model := &fakeModel{replies: []string{goodSpec}}
	g := &viewgen.Generator{Model: model, Store: &viewgen.Store{Dir: t.TempDir()}}

	_, err := g.Generate(context.Background(), viewgen.Request{
		Command: "pytest -q", Output: "2 passed\n", Kind: "plain_text"})
	assert.ErrorIs(t, err, viewgen.ErrNotWorth)
	assert.Zero(t, model.calls, "and it never asks")
}
