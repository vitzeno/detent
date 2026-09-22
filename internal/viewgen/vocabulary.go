package viewgen

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/viewspec"
)

// The vocabulary composition chooses from. Everything here is a list
// the program owns: a judge picks an entry, never writes one.

const (
	// parseNone is the answer that means nothing is worth extracting,
	// which is how a composition declines. Prose gets no view.
	parseNone = "none"
	// choiceNone is the same answer for an optional widget.
	choiceNone = "none"
	// wholeLine is the one pattern composition writes, fixed: one
	// field per line holding the line.
	wholeLine = `^(?P<line>.+)$`
	// headerCandidates is how many opening lines are offered as the
	// header. Four covers a total, a title and a blank.
	headerCandidates = 4
)

var (
	// ErrNoJudge says composition was asked for without a judge. There
	// is no second path: the judge is how a spec is written now.
	ErrNoJudge = errors.New("viewgen: composing a view needs a judge")

	errJudgeSilent   = errors.New("the judge did not answer")
	errNothingToDraw = errors.New("no structure worth extracting")
)

// parseCriteria is how each parse kind is described to the judge. The
// wording is load-bearing: it is the literal text a choice is made
// from, not guidance around a schema.
var parseCriteria = map[string]any{
	"columns": map[string]any{
		"what":    "whitespace-aligned columns under a header row, one record per line",
		"not_for": "a bare list with no header, or headings that contain spaces",
	},
	"fixed": map[string]any{
		"what":    "aligned columns whose header contains multi-word names, sliced at the header's own offsets",
		"not_for": "columns whose headings are single words, which plain columns reads more simply",
	},
	"prefix": map[string]any{
		"what": "every line is a leading token then the rest: a hash and a subject, " +
			"a size and a path, a count and a filename, a status and a name",
		"not_for": "lines with three or more fields worth separating, which is columns",
	},
	"lines": map[string]any{
		"what":    "one value per line and nothing to divide: paths, names, timestamps",
		"not_for": "lines with a leading token and a remainder, which prefix reads",
	},
	"pairs": map[string]any{
		"what":    "one key and value per line, separated by = or :",
		"not_for": "more than two fields per line",
	},
	"delimited": map[string]any{
		"what":    "fields separated by a single consistent character such as a comma",
		"not_for": "fields separated by runs of spaces, which is columns",
	},
	"indent": map[string]any{
		"what":    "a hierarchy where leading whitespace is the depth",
		"not_for": "flat output where every line starts in the same place",
	},
	"json": map[string]any{
		"what":    "JSON objects, as a document, an array, or one per line",
		"not_for": "anything that is not valid JSON",
	},
	parseNone: map[string]any{
		"what":    "prose or a log with no record structure worth extracting",
		"not_for": "output with any repeating shape at all; prefer a real parse kind",
	},
}

// split sorts the offered kinds into the ones that draw the rows and
// the ones that draw a fact about all of them, so a single question
// never offers a meter against a table: they answer different
// questions. Each widget says which it is in its own Describe, so
// registering one is enough to be offered.
func split(guide map[string]viewspec.Description, offered []string) (body, summary []string) {
	for _, kind := range offered {
		d, ok := guide[kind]
		if !ok {
			// No description, no criteria to offer it under.
			continue
		}
		if d.Summarises {
			summary = append(summary, kind)
			continue
		}
		body = append(body, kind)
	}
	return body, summary
}

// slot is one field a widget cannot be drawn without, named so the
// question reads as a question. A table needs none: with no columns it
// draws every parsed field in the order the output printed them.
type slot struct {
	name         string
	instructions string
	column       bool
}

func slotsFor(kind string) []slot {
	switch kind {
	case "list", "tree", "flow", "badges", "histogram", "sparkline", "dots":
		return []slot{{name: "field", instructions: "Which field holds the value to draw?"}}
	case "bar", "gauge", "keyvalue":
		return []slot{
			{name: "label", instructions: "Which field labels each row?", column: true},
			{name: "value", instructions: "Which field holds the number or value for each row?", column: true},
		}
	}
	return nil
}

// block assembles one widget from the fields chosen for its slots.
func block(kind string, answers classify.Answers, prefix string) viewspec.Block {
	b := viewspec.Block{Kind: kind}
	for _, s := range slotsFor(kind) {
		got := answers[prefix+s.name].Choice
		if got == "" {
			continue
		}
		if s.column {
			b.Columns = append(b.Columns, viewspec.Column{Field: got})
			continue
		}
		b.Field = got
	}
	// dots colours by what it draws unless told otherwise, since it
	// cannot bind without an accent.
	if kind == "dots" && b.Field != "" {
		b.Accent = &viewspec.Accent{Field: b.Field, Map: map[string]viewspec.Role{}}
	}
	return b
}

// fieldQuestion offers the fields the parse produced, each shown with
// a value it holds. A name alone is thin for %iused and meaningless
// for col3.
func fieldQuestion(s slot, fields []string, rows []viewspec.Row) classify.Question {
	criteria := map[string]any{}
	for _, f := range fields {
		var samples []string
		for _, r := range rows {
			if v := strings.TrimSpace(r[f]); v != "" && len(samples) < 3 {
				samples = append(samples, head(v, 40))
			}
		}
		if len(samples) == 0 {
			criteria[f] = f
			continue
		}
		criteria[f] = fmt.Sprintf("%s, holding values like: %s", f, strings.Join(samples, ", "))
	}
	return classify.Question{
		Instructions: s.instructions,
		Choice:       &classify.ChoiceQuestion{Criteria: criteria},
	}
}

// honour treats the header answer as fact and the kind as a
// preference. netstat is why: the header is located at 0.99 and
// columns could not read it, splitting "Local Address" into two names
// and dropping every row. So the kind gives way, tried against the
// interpreter rather than argued about.
func honour(chosen viewspec.Parse, skip int, output string) viewspec.Parse {
	if skip < 0 {
		headerless := chosen
		headerless.Header, headerless.Fields = false, positional(output)
		if _, _, err := readWith(headerless, output); err == nil {
			return headerless
		}
		return chosen
	}
	for _, kind := range append([]string{chosen.Kind}, "fixed", "columns") {
		p := chosen
		p.Kind, p.Skip = kind, skip
		if fields, _, err := readWith(p, output); err == nil && len(fields) > 1 {
			return p
		}
	}
	return chosen
}

// positional names a headerless table col1..colN. The names mean
// nothing alone, which is why the field questions carry samples.
func positional(output string) []string {
	widest := 0
	for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		widest = max(widest, len(strings.Fields(line)))
	}
	out := make([]string, min(widest, maxPositional))
	for i := range out {
		out[i] = fmt.Sprintf("col%d", i+1)
	}
	return out
}

const maxPositional = 12

// headerLine is the line the header question identified, or "" when it
// found none.
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

// readWith runs a parse and reports what it produced: the field names,
// and enough rows to show what each field holds.
func readWith(p viewspec.Parse, output string) ([]string, []viewspec.Row, error) {
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
		return nil, nil, errNothingToDraw
	}
	return b.Fields(), b.Sample(sampleRows), nil
}

const sampleRows = 3

// describe reads the descriptions the registry publishes, which are
// the criteria a widget choice is made from.
func describe(reg *viewspec.Registry) map[string]viewspec.Description {
	props := reg.Schema()["properties"].(map[string]any)
	return props["widget_guide"].(map[string]any)["const"].(map[string]viewspec.Description)
}

func criteriaFor(guide map[string]viewspec.Description, kinds []string) map[string]any {
	out := map[string]any{}
	for _, k := range kinds {
		if d, ok := guide[k]; ok {
			out[k] = map[string]any{"what": d.What, "not_for": d.NotFor}
		}
	}
	return out
}

func withNone(in map[string]any) map[string]any {
	in[choiceNone] = map[string]any{"what": "no summary; the body speaks for itself"}
	return in
}

// logger is what a composer records through.
type logger = *slog.Logger
