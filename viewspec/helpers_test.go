package viewspec_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/viewspec"
)

const goTest = `ok  	github.com/x/a	0.412s
FAIL	github.com/x/b	1.203s
ok  	github.com/x/c	9.500s
ok  	github.com/x/d	10.200s
`

func linesParse() viewspec.Parse {
	return viewspec.Parse{Kind: "lines",
		Pattern: `^(?P<status>ok|FAIL)\s+(?P<pkg>\S+)\s+(?P<secs>[\d.]+)s`}
}

func colsParse() viewspec.Parse { return viewspec.Parse{Kind: "columns", Header: true} }

func cols(fields ...string) []viewspec.Column {
	out := make([]viewspec.Column, len(fields))
	for i, f := range fields {
		out[i] = viewspec.Column{Field: f}
	}
	return out
}

func twoCols(a, b string) []viewspec.Column {
	return []viewspec.Column{{Field: a}, {Field: b}}
}

func threeCols(a, b, c string) []viewspec.Column {
	return []viewspec.Column{{Field: a}, {Field: b}, {Field: c}}
}

// bind is the whole data half in one call, and needs no Painter.
func bind(t *testing.T, spec viewspec.Spec, output string) *viewspec.Bound {
	t.Helper()
	c, err := viewspec.Compile(spec)
	require.NoError(t, err)
	b, err := c.Bind(output)
	require.NoError(t, err)
	return b
}

func draw(t *testing.T, spec viewspec.Spec, output string, width int) []string {
	t.Helper()
	r, err := bind(t, spec, output).Draw(viewspec.Frame{Width: width, Paint: viewspec.Plain()})
	require.NoError(t, err)
	return r.Lines
}

func drawPainted(t *testing.T, spec viewspec.Spec, output string, width int) []string {
	t.Helper()
	r, err := bind(t, spec, output).Draw(viewspec.Frame{
		Width: width, Paint: rolePainter{viewspec.Plain()}})
	require.NoError(t, err)
	return r.Lines
}

// rolePainter makes roles visible to assertions. Plain paints nothing,
// on purpose, so golden files stay readable.
type rolePainter struct{ viewspec.Painter }

func (p rolePainter) Paint(r viewspec.Role, s string) string { return r.String() + ":" + s }
