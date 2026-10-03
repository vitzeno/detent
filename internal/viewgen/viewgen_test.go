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

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/viewgen"
	"github.com/vitzeno/detent/ui"
	"github.com/vitzeno/detent/views"
	"github.com/vitzeno/detent/viewspec"
)

// A composed spec is assembled from answers, so a wrong answer is a
// worse view rather than no view.
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
	// bar is offered for a table, not for plain text: the vocabulary is
	// pruned by render kind.
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
func TestCompose_DrawsOutputWithNothingToExtractAsItCame(t *testing.T) {
	g, _ := composer(t, map[string]string{"header_line": "none", "parse_kind": "none", "body": "markdown"})
	g.Registry = ui.Registry()
	req := request()
	req.Kind, req.Output = event.RendersContent, "# Notes\n\n- one\n- two\n\n## More\n\nText.\n"
	got, err := g.Compose(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "markdown", got.Spec.Blocks[0].Kind)

	// With one such kind offered, the fallback already draws it.
	g, _ = composer(t, map[string]string{"header_line": "none", "parse_kind": "none"})
	req.Kind, req.Output = event.RendersJSON, strings.Repeat("{not json}\n", 9)
	_, err = g.Compose(context.Background(), req)
	assert.ErrorIs(t, err, viewgen.ErrNoneFit)
}

// Without a judge there is nothing to compose with, and it says so.
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

// A judge that did not answer the first question is not asked a second.
func TestCompose_ASilentJudgeIsAskedOnce(t *testing.T) {
	g, judge := composer(t, nil)
	judge.err = errors.New("jev unreachable")
	_, err := g.Compose(context.Background(), request())
	require.ErrorIs(t, err, viewgen.ErrNoneFit)
	assert.Len(t, judge.asked, 1)
}

// The cache is an optimisation: a view composed and checked still draws
// when it cannot be written down.
func TestCompose_AViewThatCannotBeSavedStillDraws(t *testing.T) {
	g, _ := composer(t, map[string]string{
		"header_line": "none", "parse_kind": "prefix", "body": "table", "summary": "none",
	})
	file := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	g.Store = &viewgen.Store{Dir: filepath.Join(file, "views")}

	got, err := g.Compose(context.Background(), request())
	require.NoError(t, err)
	assert.Equal(t, viewgen.SourceGenerated, got.Source)
	require.NotNil(t, got.Spec)
}

// A judge deciding shape needs a sample, not the whole thing.
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
	require.ErrorIs(t, err, viewgen.ErrNotWorth)
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

// A kind nobody recognises gets the whole vocabulary rather than none,
// which would leave a composition nothing to choose from.
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

// Every widget a render kind offers has to be composable, or its block
// fails to validate and throws away the whole view.
func TestCompose_EveryOfferedWidgetCanBeComposed(t *testing.T) {
	const columnar = "pkg secs status\na 1.2 ok\nb 3.4 ok\nc 0.5 FAIL\nd 9.9 ok\n" +
		"e 1.1 ok\nf 2.2 ok\ng 3.3 ok\nh 4.4 ok\n"
	guide := described(ui.Registry())

	for _, k := range viewgen.Kinds() {
		if !k.Generate {
			continue
		}
		for _, w := range k.Widgets {
			if w == viewspec.RowKind || w == viewspec.PanelKind {
				continue // containers hold blocks, and nothing composes one
			}
			role, other := "body", "none"
			if guide[w].Summarises {
				// A summary needs a body under it, and it has to be
				// one this kind actually offers.
				role, other = "summary", firstBody(guide, k.Widgets)
			}
			t.Run(string(k.Name)+"/"+w, func(t *testing.T) {
				say := map[string]string{
					"header_line": "0", "parse_kind": "columns",
					"body": other, "summary": "none", role: w,
				}
				output := columnar
				// A list of one column from three hides the other two,
				// so a listing is composed from a listing.
				if w == "list" || w == "tree" || w == "flow" || k.Name == event.RendersFiles {
					say["header_line"], say["parse_kind"] = "none", "lines"
					output = strings.Repeat("cmd/detent/main.go\nui/view.go\n", 5)
				}
				g, _ := composer(t, say)
				g.Registry = ui.Registry()
				req := request()
				req.Kind, req.Output = k.Name, output
				_, err := g.Compose(context.Background(), req)
				assert.NoError(t, err, "%s is offered for %s but cannot be composed", w, k.Name)
			})
		}
	}
}

// A header of "##" read every line as one field: a table of one column.
func TestCompose_OneColumnIsNoTable(t *testing.T) {
	g, _ := composer(t, map[string]string{
		"header_line": "0", "parse_kind": "columns", "body": "table", "summary": "none"})
	req := request()
	req.Kind = "table"
	req.Output = "##\n" + strings.Repeat("# User Database\n", 8)
	_, err := g.Compose(context.Background(), req)
	assert.ErrorIs(t, err, viewgen.ErrNoneFit)
}

// brew list --versions drew a list of names and lost every version.
func TestCompose_AListOfOneFieldOfSeveralIsRefused(t *testing.T) {
	g, _ := composer(t, map[string]string{
		"header_line": "none", "parse_kind": "prefix", "body": "list", "summary": "none", "field": "first"})
	req := request()
	req.Kind = event.RendersFiles
	req.Output = strings.Repeat("abseil 20250127.1\naom 3.12.1\n", 5)
	_, err := g.Compose(context.Background(), req)
	assert.ErrorIs(t, err, viewgen.ErrNoneFit)
}

// A tree over an indented outline reads its depth, not the text as a path.
func TestCompose_ATreeOverAnOutlineReadsItsDepth(t *testing.T) {
	g, _ := composer(t, map[string]string{
		"header_line": "none", "parse_kind": "indent", "body": "tree", "summary": "none", "field": "text"})
	req := request()
	req.Kind = event.RendersFiles
	req.Output = strings.Repeat("cmd\n  detent\n    main.go\nui\n  view.go\n", 2)
	got, err := g.Compose(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "depth", got.Spec.Blocks[0].Depth)
}

// One field picked for two slots draws each key against itself, so the
// second slot is asked again without it.
func TestCompose_OneFieldForTwoSlotsIsAskedAgain(t *testing.T) {
	g, judge := composer(t, map[string]string{"header_line": "0", "parse_kind": "columns",
		"body": "keyvalue", "summary": "none", "label": "pkg", "value": "pkg"})
	judge.again = map[string]string{"value": "secs"}
	req := request()
	req.Kind = "table"
	req.Output = "pkg secs\n" + strings.Repeat("a 1.2\nb 3.4\n", 4)
	got, err := g.Compose(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, []viewspec.Column{{Field: "pkg"}, {Field: "secs"}}, got.Spec.Blocks[0].Columns)
}

// A badge per distinct value of a field unique on every row is the
// rows over again.
func TestCompose_ASummaryCountsOnlyValuesThatRepeat(t *testing.T) {
	for field, want := range map[string]int{"status": 2, "pkg": 1} {
		g, _ := composer(t, map[string]string{"header_line": "0", "parse_kind": "columns",
			"body": "table", "summary": "badges", "summary_field": field})
		req := request()
		req.Kind = "table"
		req.Output = "pkg status\n" + strings.Repeat("a ok\nb ok\nc FAIL\nd ok\n", 1) + "e ok\nf FAIL\n"
		got, err := g.Compose(context.Background(), req)
		require.NoError(t, err)
		assert.Len(t, got.Spec.Blocks, want, "a summary of %s", field)
	}
}

// Records earn a view sooner than text: a short table, or one line of
// minified JSON, is worth one.
func TestCompose_RecordsAreWorthAViewSoonerThanText(t *testing.T) {
	g, _ := composer(t, map[string]string{
		"header_line": "0", "parse_kind": "columns", "body": "table", "summary": "none"})
	req := request()
	req.Kind, req.Output = "table", "NAME STATUS\nnode-1 Ready\nnode-2 NotReady\n"
	_, err := g.Compose(context.Background(), req)
	require.NoError(t, err, "three lines of a table")

	req.Kind = "plain_text"
	_, err = g.Compose(context.Background(), req)
	require.ErrorIs(t, err, viewgen.ErrNotWorth, "three lines of text")

	g, _ = composer(t, map[string]string{
		"header_line": "none", "parse_kind": "json", "body": "table", "summary": "none"})
	req.Kind, req.Output = "structured_json", `[{"id":1,"sku":"a"},{"id":2,"sku":"b"}]`
	_, err = g.Compose(context.Background(), req)
	assert.NoError(t, err, "one line of JSON")
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

// A spec that already exists still has to bind: "ps" and "ps aux"
// normalise to one key and print different columns.
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

// go test -v is mostly its tests' own lines, which the shipped view of
// the summary alone would hide.
func TestExisting_AShippedViewThatHidesTheOutputIsNotUsed(t *testing.T) {
	g, _ := composer(t, nil)
	verbose := strings.Repeat("=== RUN   TestX\n--- PASS: TestX (0.00s)\n", 5) +
		"PASS\nok  \tgithub.com/x/p\t0.2s\n"
	_, ok := g.Existing(context.Background(), viewgen.Request{Command: "go test -v ./...", Output: verbose})
	assert.False(t, ok)
	_, ok = g.Existing(context.Background(), viewgen.Request{Command: "go test ./...", Output: goTest})
	assert.True(t, ok, "the summary alone still draws with it")
}

func TestKinds_CriteriaAndWidgetsComeFromOneTable(t *testing.T) {
	criteria := viewgen.RenderKindCriteria()
	require.Len(t, criteria, len(viewgen.Kinds()))
	guide := described(ui.Registry())

	// The registry detent actually runs with, not viewspec's own: ui
	// registers markdown on top, and a kind may name it.
	known := ui.Registry().Kinds()
	for _, k := range viewgen.Kinds() {
		assert.NotEmpty(t, k.What, k.Name)
		assert.NotEmpty(t, k.NotFor, "%s names what it is confused with", k.Name)
		assert.NotEmpty(t, k.Examples, k.Name)
		assert.Contains(t, criteria, string(k.Name))

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

// One table defines a kind's criteria and what may draw it, so a kind
// cannot be judged into existence with nothing able to render it.
// event owns the render kinds, so a shape added here or in views fails
// until it exists there, and one added there fails until it is judged.
func TestKinds_AgreeWithEvent(t *testing.T) {
	var judged []event.RenderKind
	for _, k := range viewgen.Kinds() {
		judged = append(judged, k.Name)
		assert.Contains(t, event.RenderKinds(), k.Name)
	}
	for _, k := range event.RenderKinds() {
		if k != event.RendersMarkdown { // only ever declared by a tool
			assert.Contains(t, judged, k, "nothing can judge output to be %s", k)
		}
	}
	for _, k := range views.Kinds() {
		assert.Contains(t, judged, k, "views ships a spec for %s, which nothing judges", k)
	}
}

// Text offers a tree for kubectl describe, and file content a table for
// a cat of /etc/hosts.
func TestKinds_OfferWhatTheirOutputsHold(t *testing.T) {
	offered := func(kind event.RenderKind) []string {
		for _, k := range viewgen.Kinds() {
			if k.Name == kind {
				return k.Widgets
			}
		}
		return nil
	}
	assert.Subset(t, offered(event.RendersText), []string{"tree", "keyvalue", "list"})
	assert.Subset(t, offered(event.RendersContent), []string{"table", "keyvalue", "list"})
}

func TestKinds_IsACopy(t *testing.T) {
	viewgen.Kinds()[0].Name = "changed"
	assert.NotEqual(t, event.RenderKind("changed"), viewgen.Kinds()[0].Name)
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

// Long enough to be worth a model call: a handful of lines is not,
// whatever shape it is.
var goTest = func() string {
	var out strings.Builder
	for i := range 10 {
		status := "ok  "
		if i == 3 {
			status = "FAIL"
		}
		fmt.Fprintf(&out, "%s\tgithub.com/x/p%02d\t%d.412s\n", status, i, i)
	}
	return out.String()
}()

// An unseeded command, so the generator actually runs: anything
// detent ships a spec for is answered by Existing instead.
func request() viewgen.Request {
	return viewgen.Request{Command: "pytest -q", Output: goTest, Kind: "plain_text"}
}

type failingJudge struct{}

func (failingJudge) Ask(context.Context, classify.State, classify.Questions) (classify.Answers, classify.Usage, error) {
	return nil, classify.Usage{}, errors.New("jev unreachable")
}

// scriptedJudge answers each question by name. Unscripted questions take
// the first criterion not already given in the batch, as two slots would.
type scriptedJudge struct {
	mu  sync.Mutex
	say map[string]string
	// again answers a question asked a second time, as a re-asked slot is.
	again map[string]string
	asks  map[string]int
	seen  []classify.State
	// asked is each batch's question names, sorted.
	asked [][]string
	// err fails every batch after recording it.
	err error
}

func (j *scriptedJudge) Ask(_ context.Context, state classify.State,
	qs classify.Questions) (classify.Answers, classify.Usage, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.seen = append(j.seen, state)
	j.asked = append(j.asked, slices.Sorted(maps.Keys(qs)))
	if j.err != nil {
		return nil, classify.Usage{}, j.err
	}
	out := classify.Answers{}
	given := map[string]bool{}
	for _, name := range slices.Sorted(maps.Keys(qs)) {
		q := qs[name]
		if j.asks == nil {
			j.asks = map[string]int{}
		}
		j.asks[name]++
		choice, ok := j.say[name]
		if again, has := j.again[name]; has && j.asks[name] > 1 {
			choice = again
		}
		if !ok {
			choice = firstCriterion(q)
			for _, c := range slices.Sorted(maps.Keys(q.Choice.Criteria)) {
				if !given[c] {
					choice = c
					break
				}
			}
		}
		given[choice] = true
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

// firstBody is any widget from the list that draws rows, for a test
// that needs a body it is not itself about.
// described is every widget that says what it is for, keyed by kind, which
// is what the judge is offered.
func described(reg *viewspec.Registry) map[string]viewspec.Description {
	out := map[string]viewspec.Description{}
	for _, k := range reg.Kinds() {
		if d, ok := reg.Describe(k); ok {
			out[k] = d
		}
	}
	return out
}

func firstBody(guide map[string]viewspec.Description, kinds []string) string {
	for _, k := range kinds {
		if d, ok := guide[k]; ok && !d.Summarises && k != viewspec.RowKind && k != viewspec.PanelKind {
			return k
		}
	}
	return "log"
}
