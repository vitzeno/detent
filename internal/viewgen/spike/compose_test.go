// Steps 3 and 4 of COMPOSE_PLAN.md: choosing widgets and their fields.
// Harder than step 1, because a parse kind is one choice over eight
// well-separated options while this is several choices over sets that
// overlap. bar, gauge and histogram are genuinely close calls.
//
//	TYPESAFE_API_KEY=... go test ./internal/viewgen/spike/ -run Compose -v
package spike

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/viewspec"
)

// composed is what the four steps produce, plus what it cost.
type composed struct {
	spec   viewspec.Spec
	fields []string
	asked  int
	took   time.Duration
}

func TestSpike_JevComposesAView(t *testing.T) {
	key := os.Getenv("TYPESAFE_API_KEY")
	if key == "" {
		t.Skip("set TYPESAFE_API_KEY to run the spike")
	}
	judge := classify.NewJevJudge(key)
	reg := viewspec.Standard()

	var drawn, total int
	var sum time.Duration
	for _, s := range samples {
		out, err := run(s.command)
		if err != nil && len(out) == 0 {
			continue
		}
		output := string(out)
		// Header first. Which line names the columns is a property of
		// the output, and knowing it is what lets the kind question
		// tell single-word headings from ones containing spaces.
		skip := askSkip(t, judge, s.command, output)
		kind, _ := ask(t, judge, s.command, output, headerLine(output, skip))
		parse, ok := parseFor(kind, output)
		if !ok {
			t.Logf("\n%s: jev chose %q, which produces nothing here", s.name, kind)
			continue
		}
		if parse.Header {
			parse = honour(parse, skip, output)
		}
		total++

		c, err := compose(t, judge, reg, s.command, output, parse)
		if err != nil {
			t.Logf("\n%s: composition failed: %v", s.name, err)
			continue
		}
		sum += c.took

		lines, err := drawSpec(reg, c.spec, output, 76)
		if err != nil {
			t.Logf("\n── %s ── %d questions, %v\n   DID NOT DRAW: %v",
				s.name, c.asked, c.took.Round(time.Millisecond), err)
			continue
		}
		drawn++
		t.Logf("\n── %s ── %s parse, %d questions, %v, fields: %s\n%s",
			s.name, parse.Kind, c.asked, c.took.Round(time.Millisecond),
			strings.Join(c.fields, " "), strings.Join(lines, "\n"))
	}

	require.Positive(t, total)
	t.Logf("\n%d/%d composed a view that binds and draws", drawn, total)
	t.Logf("mean %v per view", (sum / time.Duration(total)).Round(time.Millisecond))
}

// compose runs steps 2 to 4: the parse has already been chosen, so the
// real field names are known before any widget is picked. That is the
// whole reason a field cannot be invented here.
func compose(t *testing.T, j *classify.JevJudge, reg *viewspec.Registry,
	command, output string, parse viewspec.Parse) (composed, error) {
	t.Helper()
	start := time.Now()
	var c composed
	c.spec = viewspec.Spec{Version: viewspec.Version, Parse: parse}

	fields, rows, err := fieldsAndRows(parse, output)
	if err != nil {
		return c, err
	}
	if len(fields) == 0 {
		return c, fmt.Errorf("parse produced no fields")
	}
	c.fields = fields

	guide := widgetGuide(reg)
	state := map[string]any{
		"command": command, "output": head(output, 2048),
		"fields_found": fields, "parse_kind": parse.Kind,
	}

	// One call, both structural questions. Batched the way
	// json-render's select phase batches: independent decisions that
	// cannot conflict, rather than a sequence of follow-ups.
	answers := askAll(t, j, state, classify.Questions{
		"body": {
			Instructions: "Which widget should draw the body of this output? " +
				"Choose what a human would read this best as.",
			Choice: &classify.ChoiceQuestion{Criteria: criteriaFor(guide, bodyKinds)},
		},
		"summary": {
			Instructions: "Which widget, if any, should sit above the body as a one-line " +
				"summary? Choose none unless it genuinely adds something.",
			Choice: &classify.ChoiceQuestion{Criteria: withNone(criteriaFor(guide, summaryKinds))},
		},
	})
	c.asked++

	body := answers["body"].Choice
	summary := answers["summary"].Choice

	// Field choices are closed over what the parse actually produced,
	// which is the class of error generation keeps making.
	slots := slotsFor(body)
	qs := classify.Questions{}
	for _, slot := range slots {
		qs[slot.name] = classify.Question{
			Instructions: slot.instructions,
			Choice:       &classify.ChoiceQuestion{Criteria: fieldCriteria(fields, rows)},
		}
	}
	if summary != "none" {
		for _, slot := range slotsFor(summary) {
			qs["summary_"+slot.name] = classify.Question{
				Instructions: "For the summary widget: " + slot.instructions,
				Choice:       &classify.ChoiceQuestion{Criteria: fieldCriteria(fields, rows)},
			}
		}
	}
	if len(qs) > 0 {
		fa := askAll(t, j, state, qs)
		c.asked++
		if summary != "none" {
			c.spec.Blocks = append(c.spec.Blocks, block(summary, slotsFor(summary), fa, "summary_"))
		}
		c.spec.Blocks = append(c.spec.Blocks, block(body, slots, fa, ""))
	} else {
		if summary != "none" {
			c.spec.Blocks = append(c.spec.Blocks, block(summary, nil, nil, ""))
		}
		c.spec.Blocks = append(c.spec.Blocks, block(body, nil, nil, ""))
	}
	c.took = time.Since(start)
	return c, nil
}

// bodyKinds and summaryKinds split the vocabulary by the job, so one
// question never offers a meter against a table.
var bodyKinds = []string{"table", "list", "tree", "flow", "keyvalue", "bar",
	"gauge", "log", "code", "errors", "json", "dots", "histogram"}

var summaryKinds = []string{"meter", "badges", "stat", "sparkline", "histogram"}

type slot struct {
	name         string
	instructions string
	target       string // "field", "label", "value"
}

// slotsFor is what a widget cannot be drawn without, from its own
// Describe().Needs. A table with no columns draws every parsed field
// in order, so it needs nothing chosen.
func slotsFor(kind string) []slot {
	switch kind {
	case "list", "tree", "flow", "badges", "histogram", "sparkline", "dots":
		return []slot{{"field", "Which field holds the value this widget should draw?", "field"}}
	case "bar", "gauge", "keyvalue":
		return []slot{
			{"label", "Which field labels each row?", "label"},
			{"value", "Which field holds the number, or the value, for each row?", "value"},
		}
	}
	return nil
}

func block(kind string, slots []slot, answers classify.Answers, prefix string) viewspec.Block {
	b := viewspec.Block{Kind: kind}
	for _, s := range slots {
		got := answers[prefix+s.name].Choice
		switch s.target {
		case "field":
			b.Field = got
		case "label":
			b.Columns = append(b.Columns, viewspec.Column{Field: got})
		case "value":
			b.Columns = append(b.Columns, viewspec.Column{Field: got})
		}
	}
	// dots colours by the field it draws unless something better is
	// chosen, since it cannot bind without an accent.
	if kind == "dots" && b.Field != "" {
		b.Accent = &viewspec.Accent{Field: b.Field, Map: map[string]viewspec.Role{}}
	}
	return b
}

func askAll(t *testing.T, j *classify.JevJudge, state map[string]any, qs classify.Questions) classify.Answers {
	t.Helper()
	answers, _, ok := classify.AskOrFallback(context.Background(), j, classify.State(state), qs)
	require.True(t, ok, "jev did not answer")
	return answers
}

// widgetGuide reads the descriptions the registry already publishes,
// so the spike needs no production change to get at them.
func widgetGuide(reg *viewspec.Registry) map[string]viewspec.Description {
	props := reg.Schema()["properties"].(map[string]any)
	return props["widget_guide"].(map[string]any)["const"].(map[string]viewspec.Description)
}

func criteriaFor(guide map[string]viewspec.Description, kinds []string) map[string]any {
	out := map[string]any{}
	for _, k := range kinds {
		d, ok := guide[k]
		if !ok {
			continue
		}
		out[k] = map[string]any{"what": d.What, "not_for": d.NotFor}
	}
	return out
}

func withNone(in map[string]any) map[string]any {
	in["none"] = map[string]any{"what": "no summary; the body speaks for itself"}
	return in
}

// fieldCriteria shows each field with a value it actually holds. A
// name alone is thin for %iused and meaningless for col3.
func fieldCriteria(fields []string, rows []viewspec.Row) map[string]any {
	out := map[string]any{}
	for _, f := range fields {
		var samples []string
		for _, r := range rows {
			if v := strings.TrimSpace(r[f]); v != "" && len(samples) < 3 {
				samples = append(samples, head(v, 40))
			}
		}
		if len(samples) == 0 {
			out[f] = f
			continue
		}
		out[f] = fmt.Sprintf("%s, holding values like: %s", f, strings.Join(samples, ", "))
	}
	return out
}

// parseFor builds the parse step 1 chose. A lines pattern is the one
// thing still generated, and the spike stands in a naive one so the
// widget questions are exercised rather than the regexp.
func parseFor(kind, output string) (viewspec.Parse, bool) {
	p := viewspec.Parse{Kind: kind}
	switch kind {
	case "columns", "fixed", "delimited":
		p.Header = true
		if kind == "delimited" {
			p.Sep = ","
		}
	case "pairs":
		p.Sep = "="
	case "lines":
		p.Pattern = `^(?P<line>.+)$`
	}
	if _, err := fieldsOfRaw(p, output); err != nil {
		return p, false
	}
	return p, true
}

// askSkip finds the header by asking which line names the columns,
// rather than how many lines to drop. "ls -la" opens with "total 232"
// and netstat with a sentence, and both became the header: the parse
// kind was right and nobody asked where the table starts.
//
// Naming the candidate lines is what makes this answerable. A count is
// arithmetic the judge has no reason to be good at.
func askSkip(t *testing.T, j *classify.JevJudge, command, output string) int {
	// -1 means there is no header at all.
	t.Helper()
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(lines) < 2 {
		return 0
	}
	answers, _, ok := classify.AskOrFallback(context.Background(), j,
		classify.State(map[string]any{"command": command, "output": head(output, 2048)}),
		skipQuestion(output))
	if !ok {
		return 0
	}
	if answers["header_line"].Choice == "none" {
		return -1
	}
	skip, err := strconv.Atoi(answers["header_line"].Choice)
	if err != nil || skip < 0 {
		return 0
	}
	return skip
}

// skipQuestion quotes the candidate lines, which is what makes this
// answerable where "how many lines to skip" is arithmetic.
func skipQuestion(output string) classify.Questions {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	criteria := map[string]any{
		"none": "None of these is a header. Every line is data, as in ls -la.",
	}
	for i := range min(len(lines), 4) {
		criteria[strconv.Itoa(i)] = fmt.Sprintf("line %d: %s", i+1, head(lines[i], 120))
	}
	return classify.Questions{
		"header_line": {
			Instructions: "Which of these lines names the columns of the table below it? " +
				"Choose the first line if the output starts with its header, and a later " +
				"one when a total, a title or a blank line comes first.",
			Choice: &classify.ChoiceQuestion{Criteria: criteria},
		},
	}
}

// honour treats the header answer as established and the kind as a
// preference. netstat is why: Jev located the header at 0.99, and the
// kind it chose could not read it, columns splitting "Local Address"
// into two names and dropping every row. fixed reads the same header
// correctly. So the skip stands and the kind gives way, tried against
// the interpreter rather than argued about.
func honour(chosen viewspec.Parse, skip int, output string) viewspec.Parse {
	if skip < 0 {
		headerless := chosen
		headerless.Header, headerless.Fields = false, positional(output)
		if _, err := fieldsOfRaw(headerless, output); err == nil {
			return headerless
		}
		return chosen
	}
	// The chosen kind first, then the siblings that read a header.
	for _, kind := range append([]string{chosen.Kind}, "fixed", "columns") {
		p := chosen
		p.Kind, p.Skip = kind, skip
		if fields, err := fieldsOfRaw(p, output); err == nil && len(fields) > 1 {
			return p
		}
	}
	return chosen
}

// headerLine is the line askSkip identified, or "" when it found none.
func headerLine(output string, skip int) string {
	if skip < 0 {
		return ""
	}
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if skip >= len(lines) {
		return ""
	}
	return lines[skip]
}

// headerOf is headerLine for the parse-kind spike, which asks the two
// questions in the same order.
func headerOf(j *classify.JevJudge, command, output string) string {
	answers, _, ok := classify.AskOrFallback(context.Background(), j,
		classify.State(map[string]any{"command": command, "output": head(output, 2048)}),
		skipQuestion(output))
	if !ok || answers["header_line"].Choice == "none" {
		return ""
	}
	i, err := strconv.Atoi(answers["header_line"].Choice)
	if err != nil {
		return ""
	}
	return headerLine(output, i)
}

// positional names a headerless table col1..colN. The names are
// meaningless on their own, which is why the field questions carry a
// sample value: "col3: e.g. mohamed" is answerable where "col3" is not.
func positional(output string) []string {
	widest := 0
	for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		if n := len(strings.Fields(line)); n > widest {
			widest = n
		}
	}
	out := make([]string, min(widest, 12))
	for i := range out {
		out[i] = fmt.Sprintf("col%d", i+1)
	}
	return out
}

// run executes from the repo root, not the package directory, so a
// sample naming a real path finds it.
func run(command string) ([]byte, error) {
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = "../../.."
	return cmd.CombinedOutput()
}

func fieldsOfRaw(p viewspec.Parse, output string) ([]string, error) {
	f, _, err := fieldsAndRows(p, output)
	return f, err
}

// fieldsAndRows is what step 2 produces: the real field names, and
// enough rows to show the judge what each one holds.
func fieldsAndRows(p viewspec.Parse, output string) ([]string, []viewspec.Row, error) {
	c, err := viewspec.Compile(viewspec.Spec{Parse: p,
		Blocks: []viewspec.Block{{Kind: "table"}}})
	if err != nil {
		return nil, nil, err
	}
	b, err := c.Bind(output)
	if err != nil {
		return nil, nil, err
	}
	if len(b.Fields()) == 0 {
		return nil, nil, fmt.Errorf("no fields")
	}
	return b.Fields(), b.Sample(3), nil
}

func drawSpec(reg *viewspec.Registry, spec viewspec.Spec, output string, width int) ([]string, error) {
	c, err := viewspec.Compile(spec, viewspec.WithRegistry(reg))
	if err != nil {
		return nil, err
	}
	b, err := c.Bind(output)
	if err != nil {
		return nil, err
	}
	r, err := b.Draw(viewspec.Frame{Width: width, Paint: viewspec.Plain()})
	if err != nil {
		return nil, err
	}
	if len(r.Lines) > 10 {
		return append(r.Lines[:10], fmt.Sprintf("   …%d more lines", len(r.Lines)-10)), nil
	}
	return r.Lines, nil
}
