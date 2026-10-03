package viewgen

import (
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"unicode"

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

var (
	// ErrNoJudge says composition was asked for without a judge.
	ErrNoJudge = errors.New("viewgen: composing a view needs a judge")

	errJudgeSilent   = errors.New("the judge did not answer")
	errNothingToDraw = errors.New("no structure worth extracting")
	errHides         = errors.New("the view hides most of the output")
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

// sepOf is the candidate that most lines hold the same number of, or ""
// when none is on at least half of them.
func sepOf(output string, candidates []string) string {
	lines := nonBlank(output)
	best, most := "", 0
	for _, sep := range candidates {
		if n := mostCommon(lines, func(l string) int { return strings.Count(l, sep) }); n > most {
			best, most = sep, n
		}
	}
	if most*2 < len(lines) {
		return ""
	}
	return best
}

// mostCommon is how many lines share the commonest non-zero count.
func mostCommon(lines []string, count func(string) int) int {
	seen, top := map[int]int{}, 0
	for _, l := range lines {
		if n := count(l); n > 0 {
			seen[n]++
			top = max(top, seen[n])
		}
	}
	return top
}

func nonBlank(output string) []string {
	var out []string
	for l := range strings.SplitSeq(output, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
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

// honour treats the header answer as fact and the kind as a preference,
// since columns splits netstat's "Local Address" header and drops every row.
func honour(chosen viewspec.Parse, skip int, output string) viewspec.Parse {
	if skip < 0 {
		headerless := chosen
		// fixed has no headerless form: it read line 0 as titles anyway.
		if headerless.Kind == "fixed" {
			headerless.Kind = "columns"
		}
		headerless.Header, headerless.Fields = false, positional(headerless, output)
		return headerless
	}
	candidates := []viewspec.Parse{chosen}
	for _, kind := range []string{"fixed", "columns"} {
		p := chosen
		p.Kind = kind
		candidates = append(candidates, p)
	}
	if bordered(output) {
		candidates = append(candidates, viewspec.Parse{Kind: "box", Header: true})
	}
	if tabbed(output, skip) {
		candidates = append(candidates, viewspec.Parse{Kind: "delimited", Header: true, Sep: "\t"})
	}
	// The one reading the most rows: first to read at all, cal dropped
	// its short first and last weeks where fixed read every one.
	best, most, width := chosen, -1, 0
	best.Skip = skip
	for _, p := range candidates {
		p.Skip = skip
		b, err := bindWith(p, output)
		if err != nil || len(b.Fields()) < 2 || !named(b.Fields()) {
			continue
		}
		n, f := b.Rows(), len(b.Fields())
		if n > most || (n == most && breaksTie(p, f, width, output, skip)) {
			best, most, width = p, n, f
		}
	}
	return best
}

// breaksTie beats a parse as long: tabs, fixed losing no field, since on
// spaces systemctl's ● shifted a row, or fewer where the header agrees.
func breaksTie(p viewspec.Parse, fields, width int, output string, skip int) bool {
	switch p.Kind {
	case "delimited":
		return p.Sep == "\t"
	case "fixed":
		if fields >= width {
			return true
		}
		lines := nonBlank(output)
		return skip < len(lines) && len(twoSpaced.Split(strings.TrimSpace(lines[skip]), -1)) == fields
	}
	return false
}

var twoSpaced = regexp.MustCompile(`\s{2,}`)

// tabbed reports whether the header and most rows hold as many tabs:
// helm pads with spaces too, and split on them "APP VERSION" broke.
func tabbed(output string, skip int) bool {
	lines := nonBlank(output)
	if skip >= len(lines) {
		return false
	}
	want := strings.Count(lines[skip], "\t")
	if want == 0 {
		return false
	}
	same := 0
	for _, l := range lines[skip+1:] {
		if strings.Count(l, "\t") == want {
			same++
		}
	}
	return same*5 >= len(lines[skip+1:])*4
}

// bordered reports whether most lines carry a table's border.
func bordered(output string) bool {
	lines := nonBlank(output)
	n := 0
	for _, l := range lines {
		if strings.ContainsAny(l, "|│┃║") {
			n++
		}
	}
	return n*2 > len(lines)
}

// named reports whether every field is a word, not a border: split on
// spaces, mysql's | came out as a column called "|".
func named(fields []string) bool {
	for _, f := range fields {
		if !strings.ContainsFunc(f, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) {
			return false
		}
	}
	return true
}

// positional names a headerless table col1..colN, as wide as most lines:
// sized by the widest, one symlink in ls -la made every other line short.
func positional(p viewspec.Parse, output string) []string {
	split := strings.Fields
	if p.Kind == "delimited" {
		split = func(l string) []string { return strings.Split(l, p.Sep) }
	}
	// The width reading the most cells, rows kept times columns: the
	// commonest tied in ip -br, and a floor lost go test -bench to its banner.
	var widths []int
	for _, line := range nonBlank(output) {
		widths = append(widths, len(split(line)))
	}
	width, most := 0, 0
	for _, w := range widths {
		kept := 0
		for _, n := range widths {
			if n >= w {
				kept++
			}
		}
		if cells := kept * min(w, maxPositional); cells > most || (cells == most && w < width) {
			width, most = w, cells
		}
	}
	out := make([]string, min(width, maxPositional))
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
	b, err := bindWith(p, output)
	if err != nil {
		return nil, nil, err
	}
	if len(b.Fields()) == 0 {
		return nil, nil, errNothingToDraw
	}
	return b.Fields(), b.Sample(sampleRows), nil
}

const sampleRows = 3

// bindWith runs a parse alone, under a table that draws every field.
func bindWith(p viewspec.Parse, output string) (*viewspec.Bound, error) {
	c, err := viewspec.Compile(viewspec.Spec{Parse: p,
		Blocks: []viewspec.Block{{Kind: "table"}}})
	if err != nil {
		return nil, err
	}
	return c.Bind(output)
}

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
