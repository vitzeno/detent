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
		kind, _ := ask(t, judge, s.command, output)
		parse, ok := parseFor(kind, output)
		if !ok {
			t.Logf("\n%s: jev chose %q, which produces nothing here", s.name, kind)
			continue
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

	fields, err := fieldsOf(reg, parse, output)
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
			Choice:       &classify.ChoiceQuestion{Criteria: fieldCriteria(fields)},
		}
	}
	if summary != "none" {
		for _, slot := range slotsFor(summary) {
			qs["summary_"+slot.name] = classify.Question{
				Instructions: "For the summary widget: " + slot.instructions,
				Choice:       &classify.ChoiceQuestion{Criteria: fieldCriteria(fields)},
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

func fieldCriteria(fields []string) map[string]any {
	out := map[string]any{}
	for _, f := range fields {
		out[f] = "the field named " + f
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

// run executes from the repo root, not the package directory, so a
// sample naming a real path finds it.
func run(command string) ([]byte, error) {
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = "../../.."
	return cmd.CombinedOutput()
}

func fieldsOf(reg *viewspec.Registry, p viewspec.Parse, output string) ([]string, error) {
	return fieldsOfRaw(p, output)
}

func fieldsOfRaw(p viewspec.Parse, output string) ([]string, error) {
	c, err := viewspec.Compile(viewspec.Spec{Parse: p,
		Blocks: []viewspec.Block{{Kind: "log"}}})
	if err != nil {
		return nil, err
	}
	b, err := c.Bind(output)
	if err != nil {
		return nil, err
	}
	if len(b.Fields()) == 0 {
		return nil, fmt.Errorf("no fields")
	}
	return b.Fields(), nil
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
