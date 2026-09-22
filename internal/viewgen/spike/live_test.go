package spike

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/viewgen"
	"github.com/vitzeno/detent/ui"
	"github.com/vitzeno/detent/viewspec"
)

// The production composer against a real judge and real commands. The
// spikes proved the idea; this proves what shipped does it.
//
//	TYPESAFE_API_KEY=... go test ./internal/viewgen/spike/ -run Live -v
func TestLive_ComposeDrawsRealCommands(t *testing.T) {
	key := os.Getenv("TYPESAFE_API_KEY")
	if key == "" {
		t.Skip("set TYPESAFE_API_KEY to run against a real judge")
	}
	g := &viewgen.Generator{
		Judge:    classify.NewJevJudge(key),
		Registry: ui.Registry(),
		Store:    &viewgen.Store{Dir: t.TempDir()},
	}

	var drew, total int
	var sum time.Duration
	for _, s := range samples {
		out, err := run(s.command)
		if err != nil && len(out) == 0 {
			continue
		}
		total++
		start := time.Now()
		got, err := g.Compose(context.Background(), viewgen.Request{
			Command: s.command, Output: string(out), Kind: kindFor(string(out)),
		})
		took := time.Since(start)
		sum += took
		if err != nil {
			t.Logf("\n── %s ── %v: %v", s.name, took.Round(time.Millisecond), err)
			continue
		}
		lines, err := drawSpec(ui.Registry(), *got.Spec, string(out), 76)
		require.NoError(t, err, "%s composed a spec that does not draw", s.name)
		drew++
		t.Logf("\n── %s ── %s, %s parse, %v\n%s", s.name, got.Source,
			got.Spec.Parse.Kind, took.Round(time.Millisecond), strings.Join(lines, "\n"))
	}
	t.Logf("\n%d/%d drew, mean %v", drew, total, (sum / time.Duration(total)).Round(time.Millisecond))
}

// kindFor stands in for Jev's render_kind, which agent supplies in the
// real flow.
func kindFor(output string) string {
	first, _, _ := strings.Cut(output, "\n")
	if strings.Count(strings.TrimSpace(first), "  ") > 1 {
		return "table"
	}
	return "plain_text"
}

var _ = exec.Command
var _ viewspec.Spec
