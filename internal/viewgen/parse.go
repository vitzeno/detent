package viewgen

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/vitzeno/detent/viewspec"
)

const maxPositional = 12

const sampleRows = 3

var twoSpaced = regexp.MustCompile(`\s{2,}`)

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

// bindWith runs a parse alone, under a table that draws every field.
func bindWith(p viewspec.Parse, output string) (*viewspec.Bound, error) {
	c, err := viewspec.Compile(viewspec.Spec{Parse: p,
		Blocks: []viewspec.Block{{Kind: "table"}}})
	if err != nil {
		return nil, err
	}
	return c.Bind(output)
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
