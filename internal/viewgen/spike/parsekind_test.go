// Package spike answers one question before COMPOSE_PLAN.md is worth
// starting: can Jev choose a parse kind for real command output?
//
// Generation writes the parse today, and gets it wrong in ways the
// logs show. If a choice over eight options is not reliably better,
// nothing else in that plan matters.
//
// Needs a real key:
//
//	TYPESAFE_API_KEY=... go test ./internal/viewgen/spike/ -v
//
// The key lives in .detent.yaml as jev_api_key; these read the
// environment, so export it first.
package spike

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/viewspec"
)

// sample is one real command's output. The correct answers are not
// written down: workable computes them by running every extractor and
// keeping the ones that produce usable rows, so the ground truth is
// what the interpreter can actually do rather than an opinion.
type sample struct {
	name    string
	command string
}

var samples = []sample{
	{"df", "df -h"},
	{"ps", "ps aux | head -15"},
	{"ls-long", "ls -la"},
	{"ls-plain", "ls"},
	{"env", "env | head -20"},
	{"find", "find . -name '*.go' | head -20"},
	{"git-status", "git status --porcelain"},
	{"git-log", "git log --oneline -15"},
	{"go-test", "go test ./viewspec/ ./ui/layout/ 2>&1 | head -10"},
	{"du", "du -sh internal/* | head -12"},
	{"wc", "wc -l viewspec/*.go | head -12"},
	{"json", `printf '{"a":1,"b":"two"}\n{"a":2,"b":"three"}\n'`},
	{"tree-ish", "find internal -type d | head -15"},
	{"plain-prose", "head -12 README.md"},
	{"netstat", "netstat -an | head -15"},
	{"date-lines", "for i in 1 2 3 4 5 6 7 8; do date; done"},
}

// parseKinds are the options, in the order Registry reports them.
func parseKinds() []string { return viewspec.Standard().ParseKinds() }

// workable runs every parse kind against the output and keeps those
// that produce rows worth drawing. "none" is excluded: it always
// works and always means giving up, so counting it as correct would
// make the question meaningless.
func workable(output string) []string {
	var out []string
	for _, kind := range parseKinds() {
		if kind == "none" {
			continue
		}
		p := viewspec.Parse{Kind: kind}
		switch kind {
		case "lines":
			p.Pattern = `^(?P<line>.+)$`
		case "prefix":
			// nothing to configure
		case "columns", "fixed":
			p.Header = true
		case "pairs":
			p.Sep = "="
		case "delimited":
			p.Sep = " "
			p.Header = true
		}
		c, err := viewspec.Compile(viewspec.Spec{Parse: p,
			Blocks: []viewspec.Block{{Kind: "log"}}})
		if err != nil {
			continue
		}
		b, err := c.Bind(output)
		if err != nil {
			continue
		}
		if len(b.Fields()) > 0 {
			out = append(out, kind)
		}
	}
	sort.Strings(out)
	return out
}

func TestSpike_JevChoosesAParseKind(t *testing.T) {
	key := os.Getenv("TYPESAFE_API_KEY")
	if key == "" {
		t.Skip("set TYPESAFE_API_KEY to run the spike")
	}
	judge := classify.NewJevJudge(key)

	var hits, total int
	var slowest, sum time.Duration
	t.Logf("%-12s %-12s %-9s %s", "sample", "jev", "latency", "workable")
	for _, s := range samples {
		out, err := run(s.command)
		if err != nil && len(out) == 0 {
			t.Logf("%-12s (command produced nothing, skipped)", s.name)
			continue
		}
		output := string(out)
		ok := workable(output)
		if len(ok) == 0 {
			t.Logf("%-12s (no parse kind works, skipped)", s.name)
			continue
		}

		start := time.Now()
		choice, conf := ask(t, judge, s.command, output, headerOf(judge, s.command, output))
		took := time.Since(start)
		sum += took
		if took > slowest {
			slowest = took
		}

		total++
		mark := " "
		if contains(ok, choice) {
			hits++
			mark = "✓"
		}
		t.Logf("%s %-10s %-12s %-9s %s (conf %.2f)",
			mark, s.name, choice, took.Round(time.Millisecond), strings.Join(ok, " "), conf)
	}

	require.Positive(t, total, "no samples ran")
	t.Logf("\n%d/%d chose a parse kind that works", hits, total)
	t.Logf("latency: mean %v, worst %v", (sum / time.Duration(total)).Round(time.Millisecond),
		slowest.Round(time.Millisecond))
	t.Logf("compare: generation ran 31s median, 80s worst, in session 722911c3")
}

// ask puts the same question COMPOSE_PLAN.md step 1 would ask. header
// is what askSkip found, so the kind question is answered knowing
// where the table starts: netstat picked columns over fixed because
// the line it was reading as a header was a sentence.
func ask(t *testing.T, j *classify.JevJudge, command, output, header string) (string, float64) {
	t.Helper()
	criteria := map[string]any{
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
			"what": "every line has the same shape, no header, and needs three or more parts " +
				"named separately, so a pattern is the only way to name them",
			"not_for": "a leading token and a remainder, which prefix reads without a pattern",
		},
		"pairs": map[string]any{
			"what":    "one key and value per line, separated by = or :",
			"not_for": "more than two fields per line",
		},
		"delimited": map[string]any{
			"what":    "fields separated by a single consistent character such as a comma or a colon",
			"not_for": "fields separated by runs of spaces, which is columns",
		},
		"indent": map[string]any{
			"what":    "a hierarchy where leading whitespace is the depth",
			"not_for": "flat output where every line starts in the same place",
		},
		"json": map[string]any{
			"what":    "JSON objects, one per line or as an array",
			"not_for": "anything that is not valid JSON",
		},
		"none": map[string]any{
			"what":    "prose or a log with no record structure worth extracting",
			"not_for": "output with any repeating shape at all; prefer a real parse kind",
		},
	}
	state := map[string]any{"command": command, "output": head(output, 4096)}
	if header != "" {
		state["header_line"] = header
	}
	answers, _, ok := classify.AskOrFallback(context.Background(), j,
		classify.State(state),
		classify.Questions{
			"parse_kind": {
				Instructions: "How should this command's output be read into rows? " +
					"Choose the kind that describes the shape of the bytes, not what they mean. " +
					"Where header_line is given, it is the line naming the columns: read it to " +
					"decide whether the headings are single words or contain spaces.",
				Choice: &classify.ChoiceQuestion{Criteria: criteria},
			},
		})
	require.True(t, ok, "jev did not answer")
	return answers["parse_kind"].Choice, answers["parse_kind"].Confidence
}

func head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
