package viewspec

import (
	"math"
	"strconv"
	"testing"
)

// Every extractor reads bytes a command chose, so none may panic on any
// of them, and whatever binds must draw inside the frame.

func FuzzExtractLines(f *testing.F) {
	fuzzParse(f, Parse{Kind: "lines", Pattern: `^(?P<status>ok|FAIL)\s+(?P<pkg>\S+)\s+(?P<secs>[\d.]+)s`},
		"ok  github.com/x/a 0.412s\nFAIL github.com/x/b 1.2s\n", "FAIL\n", "")
}

func FuzzExtractColumns(f *testing.F) {
	fuzzParse(f, Parse{Kind: "columns", Header: true},
		"NAME   STATUS\napi    Up 3 hours\ndb     Exited (0)\n",
		"PID TTY TIME CMD\n1 ?? 0:01 /sbin/launchd\n", "A\n\n\nB C\n", "-----\n")
}

func FuzzExtractJSON(f *testing.F) {
	fuzzParse(f, Parse{Kind: "json"},
		`[{"Name":"api","Port":8080},{"Name":"db","Port":5432}]`, `{"a":{"b":[1,2]}}`,
		"{\"a\":1}\n{\"a\":2}\n", `[1,2]`, `{"n":1e400}`, "null")
}

func FuzzExtractFixed(f *testing.F) {
	fuzzParse(f, Parse{Kind: "fixed"},
		"CONTAINER ID   IMAGE   STATUS\nabc123         nginx   Up 2 hours\n",
		"              total        used\nMem:            15Gi       9.1Gi\n", "日本 語\nあ い\n")
}

func FuzzExtractPairs(f *testing.F) {
	fuzzParse(f, Parse{Kind: "pairs", Sep: "="}, "HOME=/root\nPATH=/bin:/usr/bin\n", "=\n==\n")
}

func FuzzExtractDelimited(f *testing.F) {
	fuzzParse(f, Parse{Kind: "delimited", Sep: ",", Header: true},
		"name,company\n\"Smith, John\",Acme\n", "a\n1,2,3\n", "\"\n")
}

func FuzzExtractIndent(f *testing.F) {
	fuzzParse(f, Parse{Kind: "indent"}, "root\n  a\n    a1\n\tb\n", " \t \n")
}

func FuzzExtractPrefix(f *testing.F) {
	fuzzParse(f, Parse{Kind: "prefix"}, "abc123 Fix the thing\n  12 ./dir\n", "\t\n")
}

func FuzzExtractBox(f *testing.F) {
	fuzzParse(f, Parse{Kind: "box"},
		"+----+------+\n| id | name |\n+----+------+\n| 1  | ada  |\n+----+------+\n",
		" id | name\n----+------\n  1 | ada\n(1 row)\n", "│\n|\n")
}

func FuzzExtractNone(f *testing.F) {
	fuzzParse(f, Parse{Kind: "none"}, "anything\n")
}

func FuzzNumber(f *testing.F) {
	for _, s := range []string{"45%", "1,024", "1.2G", "926Gi", "12ms", "-3.5", "1e5", "1E", "2.5e-3K",
		"1000E", "", "-", ".", "+1,5", "1e400", "nan", "inf"} {
		f.Add(s, 1.5)
	}
	f.Add("", math.MaxFloat64)
	f.Add("", -1e-300)
	f.Fuzz(func(t *testing.T, s string, x float64) {
		if v := number(s); math.IsInf(v, 0) || math.IsNaN(v) {
			t.Fatalf("number(%q) = %v", s, v)
		}
		if math.IsInf(x, 0) || math.IsNaN(x) {
			return
		}
		printed := strconv.FormatFloat(x, 'f', -1, 64)
		if got := number(printed); got != x {
			t.Fatalf("number(%q) = %v, want %v", printed, got, x)
		}
	})
}

// fuzzParse binds output through p and draws every standard widget that
// accepts what it parsed, at a few widths.
func fuzzParse(f *testing.F, p Parse, seeds ...string) {
	f.Helper()
	for _, s := range seeds {
		f.Add(s, 40)
	}
	reg := Standard()
	f.Fuzz(func(t *testing.T, output string, width int) {
		width = 1 + int(uint(width)%160)
		// The capture cap upstream is 8KB, and longer only slows each run.
		output = output[:min(len(output), 8<<10)]
		ext, err := reg.extractor(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ext.Extract(output); err != nil {
			return
		}
		probe, err := Compile(Spec{Parse: p, Blocks: []Block{{Kind: "table"}}})
		if err != nil {
			t.Fatal(err)
		}
		bound, err := probe.Bind(output)
		if err != nil {
			return
		}
		fields := bound.Fields()
		for _, kind := range reg.Kinds() {
			if reg.isContainer(kind) {
				continue
			}
			drawAny(t, Spec{Parse: p, Blocks: []Block{blockFor(reg, kind, fields)}}, output, width)
		}
	})
}

// blockFor fills a block from whatever fields there are, so every kind is
// drawn from the fuzzed rows rather than from rows a test chose.
func blockFor(reg *Registry, kind string, fields []string) Block {
	b := Block{Kind: kind, Title: "t"}
	if len(fields) == 0 {
		return b
	}
	pick := func(i int) string { return fields[i%len(fields)] }
	b.Field, b.Depth = pick(0), pick(1)
	b.CountWhere, b.Of = pick(0)+"=1", pick(1)+"=1"
	d, _ := reg.Describe(kind)
	for i := range max(len(d.Needs), 3) {
		b.Columns = append(b.Columns, Column{Field: pick(i)})
	}
	switch kind {
	case "list", "tree", "sparkline", "histogram", "badges":
		b.Columns = nil
	case "bar", "stack", "scatter", "series", "boxplot", "timeline", "gauge":
		b.Columns = b.Columns[:2]
	}
	return b
}

func drawAny(t *testing.T, spec Spec, output string, width int) {
	t.Helper()
	c, err := Compile(spec)
	if err != nil {
		return
	}
	b, err := c.Bind(output)
	if err != nil {
		return
	}
	for _, focused := range []bool{false, true} {
		r, err := b.Draw(Frame{Width: width, Focused: focused, Cursor: 1, Paint: Plain()})
		if err != nil {
			continue
		}
		for _, l := range r.Lines {
			if Plain().Width(l) > width {
				t.Fatalf("%s: line %q is wider than %d", spec.Blocks[0].Kind, l, width)
			}
		}
		if r.CursorLine >= len(r.Lines) {
			t.Fatalf("%s: cursor line %d past %d lines", spec.Blocks[0].Kind, r.CursorLine, len(r.Lines))
		}
	}
}
