package viewgen_test

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/viewgen"
	"github.com/vitzeno/detent/viewspec"
)

func TestCorpus_ReadsWhatTheOutputHolds(t *testing.T) {
	for _, c := range corpus {
		t.Run(c.file+"/"+c.say["parse_kind"], func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", "corpus", c.file+".txt"))
			require.NoError(t, err)
			output := string(raw)
			say := maps.Clone(c.say)
			say["body"], say["summary"] = "table", "none"
			g, _ := composer(t, say)
			got, err := g.Compose(context.Background(), viewgen.Request{Command: c.file, Output: output, Kind: c.kind})
			require.NoError(t, err)

			compiled, err := viewspec.Compile(*got.Spec)
			require.NoError(t, err)
			b, err := compiled.Bind(output)
			require.NoError(t, err)
			assert.Equal(t, c.rows, b.Rows(), "rows")
			assert.Len(t, b.Fields(), c.fields, "fields %v", b.Fields())
			assert.False(t, b.Hides())
			for field, pattern := range c.each {
				for _, r := range b.Sample(b.Rows()) {
					assert.Regexp(t, pattern, r[field], "%s in %v", field, r)
				}
			}
		})
	}
}

// corpus is real output with the answers Jev gave, wrong ones included.
// Each case says what must be read, never which parse reads it.
var corpus = []struct {
	file         string
	kind         event.RenderKind
	say          map[string]string
	rows, fields int
	// each is a pattern every row's value in a field must match, which
	// is what catches a row read a column out of line.
	each map[string]string
}{
	{"df-h", "table", header("0", "columns"), 5, 9, map[string]string{"capacity": `^\d+%$`, "size": `i$`}},
	{"df-k", "table", header("0", "columns"), 5, 9, map[string]string{"capacity": `^\d+%$`, "iused": `^\d+$`}},
	{"kubectl-events", "table", header("0", "columns"), 12, 5, map[string]string{"type": `^(Normal|Warning)$`}},
	{"kubectl-wide", "table", header("0", "columns"), 15, 9, map[string]string{"nominated node": `^<none>$`}},
	{"gcloud", "table", header("0", "columns"), 9, 7, map[string]string{"status": `^(RUNNING|TERMINATED)$`}},
	{"helm-list", "table", header("0", "columns"), 8, 7, map[string]string{"status": `^(deployed|failed)$`}},
	{"psql", "table", header("0", "columns"), 12, 4, map[string]string{"id": `^\d+$`}},
	{"mysql", "table", header("1", "columns"), 12, 4, map[string]string{"id": `^\d+$`}},
	{"sqlite-box", "table", header("1", "columns"), 12, 4, map[string]string{"id": `^\d+$`}},
	{"markdown-table", "table", header("0", "columns"), 12, 4, map[string]string{"id": `^\d+$`}},
	{"pip-list", "table", header("0", "columns"), 12, 2, map[string]string{"version": `^\d`}},
	{"contacts-csv", "table", header("0", "delimited"), 10, 4, map[string]string{"email": `@example\.com$`}},
	{"top", "table", header("6", "columns"), 13, 12, map[string]string{"pid": `^\d+$`}},
	{"systemctl", "table", header("0", "columns"), 9, 6, map[string]string{"load": `^loaded$`}},
	{"free", "table", header("0", "columns"), 2, 7, map[string]string{"col1": `^(Mem|Swap):$`}},
	{"iostat", "table", header("5", "columns"), 6, 6, map[string]string{"device": `^(nvme|sd|loop|dm)`}},
	{"cal", "table", header("1", "columns"), 5, 7, map[string]string{"mo": `^\d*$`}},
	{"ip-br", "table", header("none", "columns"), 5, 3, map[string]string{"col2": `^(UP|DOWN|UNKNOWN)$`}},
	{"ip-br", "table", header("none", "fixed"), 5, 3, map[string]string{"col2": `^(UP|DOWN|UNKNOWN)$`}},
	{"ls-la", "table", header("none", "columns"), 7, 9, map[string]string{"col1": `^[-l]r`}},
	{"bench", "plain_text", header("none", "columns"), 8, 8, map[string]string{"col1": `^Benchmark`}},
	{"git-log-pipes", "table", header("none", "delimited"), 8, 4, map[string]string{"col3": `^2026-`}},
	{"redis-info", "plain_text", header("none", "pairs"), 9, 2, map[string]string{"key": `^[a-z_]+$`}},
	{"go-env", "plain_text", header("none", "pairs"), 8, 2, map[string]string{"key": `^[A-Z0-9_]+$`}},
	{"meminfo", "plain_text", header("none", "prefix"), 12, 2, map[string]string{"first": `:$`}},
}

func header(line, parse string) map[string]string {
	return map[string]string{"header_line": line, "parse_kind": parse}
}
