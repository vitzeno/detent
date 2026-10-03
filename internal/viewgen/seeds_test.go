package viewgen

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/views"
	"github.com/vitzeno/detent/viewspec"
)

func TestSeeds_EveryShippedSpecHasASampleOfItsOwnOutput(t *testing.T) {
	for _, command := range views.Commands() {
		_, ok := seedOutput[command]
		assert.True(t, ok, "%s ships a spec with no sample output to bind it against", command)
	}
	for command := range seedOutput {
		_, ok := views.ForCommand(command)
		assert.True(t, ok, "%s has a sample but no shipped spec", command)
	}
}

// The key is what Normalise produces, so a realistic invocation has to
// reach it.
func TestSeeds_RealInvocationsReachTheirSeed(t *testing.T) {
	for command, want := range map[string]string{
		"go test ./... -race":            "go test",
		"git status --porcelain":         "git status",
		"docker ps -a":                   "docker ps",
		"env | sort":                     "env",
		"find . -name '*.go'":            "find",
		"tree -L 2":                      "tree",
		"ps aux --sort=-%cpu | head -20": "ps",
	} {
		assert.Equal(t, want, Normalise(command), command)
		_, ok := views.ForCommand(Normalise(command))
		assert.True(t, ok, "%s should reach a shipped spec", command)
	}
}

func TestSeeds_EachDrawsItsOwnOutput(t *testing.T) {
	reg := viewspec.Standard()
	for command, output := range seedOutput {
		t.Run(command, func(t *testing.T) {
			spec, ok := seed(command)
			require.True(t, ok)
			assert.Equal(t, Normalise(command), spec.Match,
				"match should be the key it is filed under")

			require.NoError(t, draws(spec, reg, output))

			compiled, err := viewspec.Compile(*spec, viewspec.WithRegistry(reg))
			require.NoError(t, err)
			b, err := compiled.Bind(output)
			require.NoError(t, err)
			for _, width := range []int{20, 60, 120} {
				r, err := b.Draw(viewspec.Frame{Width: width, Paint: viewspec.Plain()})
				require.NoError(t, err, "width %d", width)
				assert.NotEmpty(t, r.Lines, "width %d drew nothing", width)
			}
		})
	}
}

// Pruning decides what the judge may pick, not what an already written
// spec may use, so trimming a kind's list takes nothing from saved views.
func TestPrune_NarrowsGenerationWithoutNarrowingWhatDraws(t *testing.T) {
	const xy = "x y\n1 10\n2 40\n3 20\n4 90\n"
	saved := &viewspec.Spec{Version: viewspec.Version, Match: "plot",
		Parse: viewspec.Parse{Kind: "columns", Header: true},
		Blocks: []viewspec.Block{{Kind: "scatter",
			Columns: []viewspec.Column{{Field: "x"}, {Field: "y"}}}}}

	full := viewspec.Standard()
	narrowed := prune(full, KindTable).Subset("table", "text")

	assert.NotContains(t, narrowed.Kinds(), "scatter", "the model is no longer offered it")
	require.NoError(t, draws(saved, full, xy), "and the spec on disk still draws")

	// Against the pruned set it is refused: nothing may name what it was
	// not offered.
	assert.Error(t, draws(saved, narrowed, xy))
}

// Trimming a kind's list is a real saving and a modest one.
func TestPrune_SchemaShrinksButMostOfItIsFixed(t *testing.T) {
	size := func(reg *viewspec.Registry) int {
		b, err := json.Marshal(reg.Schema())
		require.NoError(t, err)
		return len(b)
	}
	full := viewspec.Standard()
	table := size(prune(full, KindTable))
	floor := size(full.Subset("text"))

	assert.Greater(t, table, floor)
	assert.Greater(t, floor, table/2,
		"over half the schema is the block and parse shape, not the widget guide, "+
			"so cutting widgets moves less than it looks like it should")
}

// A parse kind is a bare string here and an extractor name in viewspec,
// so a rename there would compile and then never bind.
func TestParseCriteria_NamesOnlyKindsViewspecReads(t *testing.T) {
	known := viewspec.Standard().ParseKinds()
	for kind := range parseCriteria {
		assert.Contains(t, known, kind)
	}
	for kind := range tabular {
		assert.Contains(t, parseCriteria, kind)
	}
}

// One sample of each shipped command's real output. A seed without a
// sample here is a seed nobody has ever bound.
var seedOutput = map[string]string{
	"go test": "ok  \tgithub.com/x/a\t0.412s\n" +
		"FAIL\tgithub.com/x/b\t1.203s\n" +
		"ok  \tgithub.com/x/c\t9.500s\n",
	"git status": " M internal/agent/loop.go\n?? notes.txt\nA  ui/paste_test.go\n",
	"docker ps": "CONTAINER ID   IMAGE     STATUS         NAMES\n" +
		"9f2a1c3d4e5f   nginx     Up 2 hours     web\n" +
		"1a2b3c4d5e6f   redis     Up 20 minutes  cache\n",
	"env":  "PATH=/usr/bin\nHOME=/root\nSHELL=/bin/sh\n",
	"find": "./main.go\n./internal/agent/loop.go\n./ui/keys.go\n",
	"tree": ".\n  internal\n    agent\n      loop.go\n  ui\n    keys.go\n",
	"ps": "  PID TTY          TIME CMD\n" +
		"    1 ?        00:00:00 sh\n" +
		"   14 ?        00:00:00 ps\n",
}
