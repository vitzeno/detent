package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/views"
	"github.com/vitzeno/detent/viewspec"
)

// compileAll drops a spec that will not compile rather than taking the
// whole map with it, so a typo here is a render kind that quietly
// loses its rendering. Nothing else would notice.
func TestFallbacks_EveryKindStillCompiles(t *testing.T) {
	require.Len(t, compiledFallback, len(views.Kinds()))
	for kind := range byKind() {
		assert.Contains(t, compiledFallback, kind, "%s lost its built-in spec", kind)
	}
	assert.NotNil(t, compiledPlain, "the floor must exist or a row can have no view")
	assert.NotNil(t, compiledMarkdown)
}

// The chain has to end in something that always draws. A row with no
// view is a pane showing nothing, and the output that breaks a spec is
// exactly the output nobody thought to try.
func TestBoundView_AlwaysYieldsSomethingDrawable(t *testing.T) {
	outputs := map[string]string{
		"empty":            "",
		"blank lines":      "\n\n\n",
		"spaces":           "   \n  \n",
		"one word":         "hi",
		"no final newline": "a\tb",
		"header only":      "NAME  STATUS\n",
		"ragged columns":   "a b c\n1\n2 3 4 5\n",
		"tabs":             "a\tb\tc\n1\t2\t3\n",
		"control bytes":    "\x00\x01\x02\n",
		"one long line":    string(make([]byte, 4096)),
	}
	kinds := []RenderKind{"", KindText, KindTable, KindFiles, KindContent,
		KindError, KindDiff, KindJSON, "something_jev_invented"}

	for _, kind := range kinds {
		for name, output := range outputs {
			t.Run(string(kind)+"/"+name, func(t *testing.T) {
				r := &stepRow{command: "cmd"}
				r.cmd.ec = &ExecutedCommand{
					Result: Result{Stdout: output},
					Post:   &PostJudgment{RenderKind: kind},
				}
				b, ok := boundView(r)
				require.True(t, ok, "no view at all")
				require.NotNil(t, b)

				// Binding is half of it: a view that draws an error is
				// the same blank pane to the human.
				for _, width := range []int{1, 8, 40, 200} {
					_, err := b.Draw(viewspec.Frame{Width: width, Paint: viewspec.Plain()})
					assert.NoError(t, err, "width %d", width)
				}
			})
		}
	}
}

// cmd/detent hands the composer ui's own registry, so a spec it
// composed always binds here too. The nil case is safe the same way:
// viewspec.Standard is a subset of this one, since ui only adds to it.
func TestRegistry_ComposerCannotPickWhatUiCannotDraw(t *testing.T) {
	ours := Registry().Kinds()
	for _, kind := range viewspec.Standard().Kinds() {
		assert.Contains(t, ours, kind,
			"%s is offered to the judge but ui cannot draw it", kind)
	}
	assert.Contains(t, ours, "markdown", "and ui adds one of its own")
}
