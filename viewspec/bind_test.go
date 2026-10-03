package viewspec_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/viewspec"
)

// A header is read during Bind, and once lived on the Compiled, so two
// outputs bound at once swapped each other's columns.
func TestBind_IsSafeOnOneCompiledAcrossGoroutines(t *testing.T) {
	aligned := [2]string{"ZEBRA  ALPHA\n1      2\n", "MIDDLE  OTHER\n3       4\n"}
	tests := []struct {
		parse   viewspec.Parse
		outputs [2]string
	}{
		{viewspec.Parse{Kind: "columns", Header: true}, aligned},
		{viewspec.Parse{Kind: "columns", Header: true, Skip: 1},
			[2]string{"banner\n" + aligned[0], "banner\n" + aligned[1]}},
		{viewspec.Parse{Kind: "delimited", Sep: ",", Header: true},
			[2]string{"ZEBRA,ALPHA\n1,2\n", "MIDDLE,OTHER\n3,4\n"}},
		{viewspec.Parse{Kind: "fixed"}, aligned},
		{viewspec.Parse{Kind: "box"}, [2]string{"| ZEBRA | ALPHA |\n| 1 | 2 |\n", "| MIDDLE | OTHER |\n| 3 | 4 |\n"}},
	}
	for _, tc := range tests {
		t.Run(tc.parse.Kind, func(t *testing.T) {
			c, err := viewspec.Compile(viewspec.Spec{Parse: tc.parse, Blocks: []viewspec.Block{{Kind: "table"}}})
			require.NoError(t, err)
			want := [2]string{"ZEBRA ALPHA", "MIDDLE OTHER"}
			var wg sync.WaitGroup
			for i := range 64 {
				wg.Go(func() {
					b, err := c.Bind(tc.outputs[i%2])
					if !assert.NoError(t, err) {
						return
					}
					r, err := b.Draw(viewspec.Frame{Width: 40, Paint: viewspec.Plain()})
					if assert.NoError(t, err) && assert.NotEmpty(t, r.Lines) {
						assert.Equal(t, want[i%2], strings.Join(strings.Fields(r.Lines[0]), " "))
					}
				})
			}
			wg.Wait()
		})
	}
}
