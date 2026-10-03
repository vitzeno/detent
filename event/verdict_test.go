package event

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Other packages check their tables against these lists, so a constant
// missing from one would pass there unchecked.
func TestVerdict_ListsHoldEveryConstant(t *testing.T) {
	declared := declaredConsts(t)
	var kinds, statuses []string
	for _, k := range RenderKinds() {
		kinds = append(kinds, string(k))
	}
	for _, s := range Statuses() {
		statuses = append(statuses, string(s))
	}
	assert.ElementsMatch(t, declared["RenderKind"], kinds)
	assert.ElementsMatch(t, declared["Status"], statuses)
}

// declaredConsts reads the source for every constant's value, by its type.
func declaredConsts(t *testing.T) map[string][]string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	out := map[string][]string{}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, err)
		for _, d := range file.Decls {
			gen, ok := d.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				id, ok := vs.Type.(*ast.Ident)
				if !ok || len(vs.Values) == 0 {
					continue
				}
				lit, ok := vs.Values[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				v, err := strconv.Unquote(lit.Value)
				require.NoError(t, err)
				out[id.Name] = append(out[id.Name], v)
			}
		}
	}
	return out
}
