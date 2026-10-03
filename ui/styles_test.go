package ui

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/ui/status"
	"github.com/vitzeno/detent/ui/theme"
	"github.com/vitzeno/detent/ui/welcome"
)

// Three packages bake styles from the palette, and only RefreshStyles
// reaches the other two. A theme they miss draws in the old colours.
func TestRefreshStyles_ReachesEverySubpackage(t *testing.T) {
	was := theme.Current()
	t.Cleanup(func() { theme.Apply(was); RefreshStyles() })
	light := theme.Themes["light"]
	theme.Apply(light)
	RefreshStyles()

	sgr := func(c color.Color) string {
		r, g, b, _ := c.RGBA()
		return fmt.Sprintf("38;2;%d;%d;%d", r>>8, g>>8, b>>8)
	}
	icon, _ := status.Badge(status.Row{HasResult: true, NoVerdict: true}, "")
	assert.Contains(t, icon, sgr(light.Safe), "status")
	assert.Contains(t, styleGoal.Render("x"), sgr(light.TextPrimary), "ui")
	assert.Equal(t, light.Background, palette.Background, "ui")
	pane := strings.Join(welcome.Lines(welcome.Facts{Version: "v"}, 80, 40, 0), "\n")
	assert.Contains(t, pane, sgr(light.Accent), "welcome")
}

// A subpackage that bakes a package-level var from the theme keeps the
// old colours after Apply unless RefreshStyles calls its own.
func TestRefreshStyles_CallsEverySubpackageThatBakes(t *testing.T) {
	fset := token.NewFileSet()
	src, err := parser.ParseFile(fset, "styles.go", nil, 0)
	require.NoError(t, err)
	called := map[string]bool{}
	for _, d := range src.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "RefreshStyles" {
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "RefreshStyles" {
					if id, ok := sel.X.(*ast.Ident); ok {
						called[id.Name] = true
					}
				}
				return true
			})
		}
	}

	dirs, err := os.ReadDir(".")
	require.NoError(t, err)
	var bakers []string
	for _, d := range dirs {
		if !d.IsDir() || d.Name() == "theme" {
			continue
		}
		if bakes(t, fset, d.Name()) {
			bakers = append(bakers, d.Name())
			assert.True(t, called[d.Name()], "ui.RefreshStyles never calls %s.RefreshStyles", d.Name())
		}
	}
	assert.ElementsMatch(t, []string{"status", "welcome"}, bakers, "the scan still finds the ones it knows of")
}

// bakes reports whether a package-level var in dir reads the theme or holds a style.
func bakes(t *testing.T, fset *token.FileSet, dir string) bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	require.NoError(t, err)
	found := false
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, err)
		for _, d := range f.Decls {
			gen, ok := d.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			ast.Inspect(gen, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if id, ok := sel.X.(*ast.Ident); ok && (id.Name == "theme" || id.Name == "lipgloss" && sel.Sel.Name == "Style") {
					found = true
				}
				return true
			})
		}
	}
	return found
}

// The declaration and RefreshStyles list the styles separately, so a
// refresh under the same theme proves they list them in the same order.
func TestRefreshStyles_KeepsEachStyleInItsPlace(t *testing.T) {
	all := func() []lipgloss.Style {
		return []lipgloss.Style{styleBrand, styleGoal, styleMuted, styleFaint, styleSafe, styleCaution, styleDanger, styleRowCursor}
	}
	was := all()
	RefreshStyles()
	assert.Equal(t, was, all())
}
