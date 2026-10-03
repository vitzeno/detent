package viewgen

import (
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
	// header: top prints five of summary and a blank before its own.
	headerCandidates = 8
)

// tabular is the parse kinds reading a line as a row of columns.
var tabular = map[string]bool{"columns": true, "fixed": true, "delimited": true}

// Separators a parse may split on, most specific first. Read off the
// output, since no single one fits both CSV and "key: value".
var (
	delimiters = []string{"\t", "|", ",", ";", ":"}
	pairSeps   = []string{"=", ":"}
)

// parseCriteria is how each parse kind is described to the judge. The
// wording is load-bearing: it is the literal text a choice is made from.
var parseCriteria = map[string]any{
	"columns": map[string]any{
		"what":    "whitespace-aligned columns under a header row, one record per line",
		"not_for": "a bare list with no header, headings that contain spaces, or a table drawn with | or │ borders",
	},
	"box": map[string]any{
		"what":    "a table drawn with borders: | or │ between cells, rules of - or ─, as mysql, psql, sqlite -box and markdown print",
		"not_for": "columns aligned by spaces alone, with no border characters",
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

// logger is what a composer records through.
type logger = *slog.Logger

// describe reads what each registered widget says about itself.
func describe(reg *viewspec.Registry) map[string]viewspec.Description {
	out := map[string]viewspec.Description{}
	for _, kind := range reg.Kinds() {
		if d, ok := reg.Describe(kind); ok {
			out[kind] = d
		}
	}
	return out
}

// split sorts the offered kinds into those drawing the rows and those
// summarising them, so one question never offers a meter against a table.
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

// fieldQuestion offers the fields the parse produced, each with a value
// it holds. A name alone is thin for %iused and meaningless for col3.
func fieldQuestion(s viewspec.Slot, fields []string, rows []viewspec.Row) classify.Question {
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
		Instructions: "Which field " + s.What + "?",
		Choice:       &classify.ChoiceQuestion{Criteria: criteria},
	}
}

// block assembles one widget from the fields chosen for its slots. One
// slot fills Field, more fill Columns in order, as viewspec.Slot states.
func block(kind string, needs []viewspec.Slot, answers classify.Answers, prefix string) viewspec.Block {
	b := viewspec.Block{Kind: kind}
	for _, s := range needs {
		got := answers[prefix+s.Name].Choice
		if got == "" {
			continue
		}
		if len(needs) == 1 {
			b.Field = got
			continue
		}
		b.Columns = append(b.Columns, viewspec.Column{Field: got})
	}
	return b
}

// twice reports whether any field is read by more than one slot.
func twice(fields []string) bool {
	seen := map[string]bool{}
	for _, f := range fields {
		if seen[f] {
			return true
		}
		seen[f] = true
	}
	return false
}

// read is every field a block draws from.
func read(b viewspec.Block) []string {
	var out []string
	for _, f := range []string{b.Field, b.Depth} {
		if f != "" {
			out = append(out, f)
		}
	}
	for _, c := range b.Columns {
		out = append(out, c.Field)
	}
	return out
}
