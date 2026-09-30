package viewgen_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/viewgen"
	"github.com/vitzeno/detent/ui"
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

// An unseeded command, so the generator actually runs: anything
// detent ships a spec for is answered by Existing instead.
func request() viewgen.Request {
	return viewgen.Request{Command: "pytest -q", Output: goTest, Kind: "plain_text"}
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
	req := viewgen.Request{Command: "go test ./...", Output: goTest, Kind: "plain_text"}

	got, ok := g.Existing(context.Background(), req)
	require.True(t, ok, "a seed serves before anything is generated")
	assert.Equal(t, viewgen.SourceShipped, got.Source)
	assert.Len(t, got.Spec.Blocks, 2, "the shipped go test view")

	edited := &viewspec.Spec{Version: 1, Match: "go test",
		Parse:  viewspec.Parse{Kind: "none"},
		Blocks: []viewspec.Block{{Kind: "log"}}}
	require.NoError(t, g.Store.Save(viewgen.Key(req.Command, req.Kind), edited))

	got, ok = g.Existing(context.Background(), req)
	require.True(t, ok)
	assert.Equal(t, viewgen.SourceSaved, got.Source, "and says so, since it is editable")
	assert.Len(t, got.Spec.Blocks, 1, "the human's edit wins")
}

// A spec that already exists still has to bind. "ps" and "ps aux"
// normalise to one key and print different columns, so the shipped
// spec matched, drew nothing, and blocked generation behind itself.
func TestExisting_RejectsASpecThatCannotDrawTheOutput(t *testing.T) {
	const psAux = `USER  PID %CPU %MEM    VSZ   RSS TTY STAT START TIME COMMAND
root    1  0.1  0.0   1090   640 ?   Ss   20:41 0:00 /bin/sh
root   14  0.0  0.0   7376  3200 ?   R    20:47 0:00 ps aux
`
	const plainPs = "  PID TTY          TIME CMD\n    1 ?        00:00:00 sh\n"

	g := &viewgen.Generator{}
	_, ok := g.Existing(context.Background(),
		viewgen.Request{Command: "ps", Output: plainPs})
	require.True(t, ok, "plain ps is what the seed was written for")

	_, ok = g.Existing(context.Background(),
		viewgen.Request{Command: "ps aux --sort=-%cpu | head -n 20", Output: psAux})
	assert.False(t, ok, "ps aux has command, not cmd, so the seed must stand aside")
}

// One table defines a kind's criteria and what may draw it, so a kind
// cannot be judged into existence with nothing able to render it.
func TestKinds_CriteriaAndWidgetsComeFromOneTable(t *testing.T) {
	criteria := viewgen.RenderKindCriteria()
	require.Len(t, criteria, len(viewgen.Kinds()))
	guide := ui.Registry().Schema()["properties"].(map[string]any)["widget_guide"].(map[string]any)["const"].(map[string]viewspec.Description)

	// The registry detent actually runs with, not viewspec's own:
	// ui registers markdown on top, and a kind may name it. Checking
	// against Standard made the table and the production vocabulary
	// two different things.
	known := ui.Registry().Kinds()
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
		// Every kind must offer something that draws rows, or there is
		// nothing for the body question to choose between.
		var bodies int
		for _, w := range k.Widgets {
			if d, ok := guide[w]; ok && !d.Summarises {
				bodies++
			}
		}
		assert.Positive(t, bodies, "%s offers a body widget, not only summaries", k.Name)
	}
}

type failingJudge struct{}

func (failingJudge) Ask(context.Context, classify.State, classify.Questions) (classify.Answers, classify.Usage, error) {
	return nil, classify.Usage{}, errors.New("jev unreachable")
}

// scriptedJudge answers each question by name, so a test can say what
// the composition decided without a network. Unscripted questions take
// the first criterion, which keeps a test to the decisions it is
// actually about.
type scriptedJudge struct {
	mu   sync.Mutex
	say  map[string]string
	seen []classify.State
	// asked is each batch's question names, sorted.
	asked [][]string
	err   error
}

func (j *scriptedJudge) Ask(_ context.Context, state classify.State,
	qs classify.Questions) (classify.Answers, classify.Usage, error) {
	if j.err != nil {
		return nil, classify.Usage{}, j.err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.seen = append(j.seen, state)
	j.asked = append(j.asked, slices.Sorted(maps.Keys(qs)))
	out := classify.Answers{}
	for name, q := range qs {
		choice, ok := j.say[name]
		if !ok {
			choice = firstCriterion(q)
		}
		if _, valid := q.Choice.Criteria[choice]; !valid {
			return nil, classify.Usage{}, fmt.Errorf("scripted %q for %q, which was not offered (have %v)",
				choice, name, slices.Sorted(maps.Keys(q.Choice.Criteria)))
		}
		out[name] = classify.Answer{Choice: choice, Confidence: 0.9}
	}
	return out, classify.Usage{InputTokens: 10}, nil
}

func firstCriterion(q classify.Question) string {
	return slices.Sorted(maps.Keys(q.Choice.Criteria))[0]
}

// composer builds a Generator wired to answer as scripted.
func composer(t *testing.T, say map[string]string) (*viewgen.Generator, *scriptedJudge) {
	t.Helper()
	j := &scriptedJudge{say: say}
	return &viewgen.Generator{Judge: j, Store: &viewgen.Store{Dir: t.TempDir()}}, j
}

// A composed spec is assembled from answers, so a wrong answer is a
// worse view rather than no view. That is the whole reason the
// generative path went: it could name a widget, a role or a field that
// did not exist, and each of those threw the view away.
func TestCompose_AssemblesFromChoicesAndCaches(t *testing.T) {
	g, judge := composer(t, map[string]string{
		"header_line": "none",
		"parse_kind":  "prefix",
		"body":        "table",
		"summary":     "none",
	})
	got, err := g.Compose(context.Background(), request())
	require.NoError(t, err)

	assert.Equal(t, viewgen.SourceGenerated, got.Source)
	assert.Equal(t, "prefix", got.Spec.Parse.Kind)
	require.Len(t, got.Spec.Blocks, 1)
	assert.Equal(t, "table", got.Spec.Blocks[0].Kind)
	assert.Equal(t, "pytest", got.Spec.Match, "filed under the normalised command")
	assert.Positive(t, got.Usage.PromptTokens, "the cost is reported")

	// Cached, and served from disk without asking again.
	before := len(judge.seen)
	again, ok := g.Existing(context.Background(), request())
	require.True(t, ok)
	assert.Equal(t, viewgen.SourceSaved, again.Source)
	assert.Len(t, judge.seen, before, "a saved spec asks nothing")
}

// Choosing columns for df reads one row of nine, all of it shifted a
// column over. The header answer stands and the kind gives way.
func TestCompose_AMisreadHeaderGivesWayToFixed(t *testing.T) {
	g, _ := composer(t, map[string]string{
		"header_line": "0",
		"parse_kind":  "columns",
		"body":        "table",
		"summary":     "none",
	})
	req := request()
	req.Kind, req.Output = "table", dfH
	got, err := g.Compose(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "fixed", got.Spec.Parse.Kind)
}

const dfH = "Filesystem      Size  Used Avail Use% Mounted on\n" +
	"/dev/disk3s1s1  926G   10G  560G   2% /\n" +
	"devfs           205K  205K    0B 100% /dev\n" +
	"/dev/disk3s6    926G  7.0G  560G   2% /System/Volumes/VM\n" +
	"/dev/disk3s2    926G  7.6G  560G   2% /System/Volumes/Preboot\n" +
	"/dev/disk3s4    926G  3.1M  560G   1% /System/Volumes/Update\n" +
	"/dev/disk1s2    500M  6.0M  483M   2% /System/Volumes/xarts\n" +
	"/dev/disk3s5    926G  345G  560G  39% /System/Volumes/Data\n" +
	"map auto_home     0B    0B    0B 100% /System/Volumes/Data/home\n"

// Every field the composition picks comes from a list the program
// built out of what the parse actually produced.
func TestCompose_OffersOnlyFieldsTheParseProduced(t *testing.T) {
	g, judge := composer(t, map[string]string{
		"header_line": "0",
		"parse_kind":  "columns",
		"body":        "bar",
		"summary":     "none",
		"label":       "pkg",
		"value":       "secs",
	})
	req := request()
	// bar is offered for a table, not for plain text: the vocabulary
	// is still pruned by render kind, as it was for generation.
	req.Kind = "table"
	req.Output = "pkg secs status\na 1.2 ok\nb 3.4 ok\nc 0.5 FAIL\nd 9.9 ok\ne 1.1 ok\nf 2.2 ok\ng 3.3 ok\nh 4.4 ok\n"
	got, err := g.Compose(context.Background(), req)
	require.NoError(t, err)

	require.Len(t, got.Spec.Blocks, 1)
	assert.Equal(t, []viewspec.Column{{Field: "pkg"}, {Field: "secs"}}, got.Spec.Blocks[0].Columns)

	// The question offered the parsed fields and nothing else.
	var offered []string
	for _, state := range judge.seen {
		if f, ok := state.(map[string]any)["fields_found"]; ok {
			offered = f.([]string)
		}
	}
	assert.Equal(t, []string{"pkg", "secs", "status"}, offered)
}

// "none" is how a composition declines, and prose is what it declines.
func TestCompose_DeclinesOutputWithNothingToExtract(t *testing.T) {
	g, _ := composer(t, map[string]string{"header_line": "none", "parse_kind": "none"})
	_, err := g.Compose(context.Background(), request())
	assert.ErrorIs(t, err, viewgen.ErrNoneFit)
}

// The judge writes the spec now, so without one there is nothing to
// compose with. Said plainly rather than quietly doing nothing.
func TestCompose_NeedsAJudge(t *testing.T) {
	g := &viewgen.Generator{Store: &viewgen.Store{Dir: t.TempDir()}}
	_, err := g.Compose(context.Background(), request())
	assert.ErrorIs(t, err, viewgen.ErrNoJudge)
}

func TestCompose_AJudgeThatFailsComposesNothing(t *testing.T) {
	g := &viewgen.Generator{Judge: failingJudge{}, Store: &viewgen.Store{Dir: t.TempDir()}}
	_, err := g.Compose(context.Background(), request())
	assert.ErrorIs(t, err, viewgen.ErrNoneFit)
}

// A judge deciding shape needs a sample, not the whole thing. Sending
// the output whole is what made every view_fit call fail silently.
func TestCompose_TheJudgeSeesABoundedSample(t *testing.T) {
	g, judge := composer(t, map[string]string{"header_line": "none", "parse_kind": "prefix"})
	req := request()
	req.Output = strings.Repeat(goTest, 200)
	require.Greater(t, len(req.Output), viewgen.MaxJudgeBytes*4)

	_, err := g.Compose(context.Background(), req)
	require.NoError(t, err)
	require.NotEmpty(t, judge.seen)
	shown := judge.seen[0].(map[string]any)["output"].(string)
	assert.LessOrEqual(t, len(shown), viewgen.MaxJudgeBytes+len("\n…[truncated]"))
}

// Short output is not worth asking about, whatever shape it is.
func TestCompose_ShortOutputIsNotWorthAsking(t *testing.T) {
	g, judge := composer(t, nil)
	req := request()
	req.Output = "two\nlines\n"
	_, err := g.Compose(context.Background(), req)
	assert.ErrorIs(t, err, viewgen.ErrNotWorth)
	assert.Empty(t, judge.seen, "and it never asked")
}

// A shipped spec is the floor: composition is tried first, and the
// seed catches what composition declines.
func TestCompose_FallsBackToAShippedSpec(t *testing.T) {
	g := &viewgen.Generator{Judge: failingJudge{}, Store: &viewgen.Store{Dir: t.TempDir()}}
	got, err := g.Compose(context.Background(),
		viewgen.Request{Command: "go test ./...", Output: goTest, Kind: "plain_text"})
	require.NoError(t, err)
	assert.Equal(t, viewgen.SourceShipped, got.Source)
}

// A kind nobody recognises gets the whole vocabulary rather than none.
// Subsetting on an unknown kind's empty widget list offers nothing,
// and a composition with nothing to choose from cannot start.
func TestCompose_AnUnknownKindStillHasAVocabulary(t *testing.T) {
	g, _ := composer(t, map[string]string{
		"header_line": "none", "parse_kind": "prefix", "body": "table", "summary": "none",
	})
	req := request()
	req.Kind = "something_jev_invented"

	got, err := g.Compose(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "table", got.Spec.Blocks[0].Kind)
}

// Every widget a render kind offers has to be composable: its needs
// must be fields the composer knows how to ask for. Offering one whose
// needs cannot be filled composes a block that fails to validate, and
// that throws away the whole view, not just the block.
//
// kinds.go and slotsFor are two lists that have to agree, and they had
// drifted: eleven widgets were offered whose columns nobody asked for.
func TestCompose_EveryOfferedWidgetCanBeComposed(t *testing.T) {
	const columnar = "pkg secs status\na 1.2 ok\nb 3.4 ok\nc 0.5 FAIL\nd 9.9 ok\n" +
		"e 1.1 ok\nf 2.2 ok\ng 3.3 ok\nh 4.4 ok\n"
	guide := ui.Registry().Schema()["properties"].(map[string]any)["widget_guide"].(map[string]any)["const"].(map[string]viewspec.Description)

	for _, k := range viewgen.Kinds() {
		if !k.Generate {
			continue
		}
		for _, w := range k.Widgets {
			if w == viewspec.RowKind || w == viewspec.PanelKind {
				continue // containers hold blocks; nothing composes one yet
			}
			role, other := "body", "none"
			if guide[w].Summarises {
				// A summary needs a body under it, and it has to be
				// one this kind actually offers.
				role, other = "summary", firstBody(guide, k.Widgets)
			}
			t.Run(k.Name+"/"+w, func(t *testing.T) {
				g, _ := composer(t, map[string]string{
					"header_line": "0", "parse_kind": "columns",
					"body": other, "summary": "none", role: w,
				})
				g.Registry = ui.Registry()
				req := request()
				req.Kind, req.Output = k.Name, columnar
				_, err := g.Compose(context.Background(), req)
				assert.NoError(t, err, "%s is offered for %s but cannot be composed", w, k.Name)
			})
		}
	}
}

// firstBody is any widget from the list that draws rows, for a test
// that needs a body it is not itself about.
func firstBody(guide map[string]viewspec.Description, kinds []string) string {
	for _, k := range kinds {
		if d, ok := guide[k]; ok && !d.Summarises && k != viewspec.RowKind && k != viewspec.PanelKind {
			return k
		}
	}
	return "log"
}
