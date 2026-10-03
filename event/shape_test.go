package event

import (
	"encoding"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "rewrite testdata/shapes.golden from the current types")

const goldenShapes = "testdata/shapes.golden"

// What a fact stores is a contract with every session already on disk, so
// changing it fails here until the golden file and a store migration agree.
func TestShapes_StoredFactsKeepTheirShape(t *testing.T) {
	var lines []string
	for _, k := range Kinds() {
		if k.IsIntent() {
			continue // never stored
		}
		e, err := Decode(k, []byte(`{}`))
		require.NoError(t, err)
		for _, f := range shape(t, reflect.TypeOf(e), "") {
			lines = append(lines, string(k)+" "+f)
		}
	}
	got := strings.Join(lines, "\n") + "\n"
	if *update {
		require.NoError(t, os.WriteFile(goldenShapes, []byte(got), 0o600))
		return
	}
	want, err := os.ReadFile(goldenShapes)
	require.NoError(t, err)
	assert.Equal(t, string(want), got,
		"what a stored fact writes changed. If that was meant, add a store migration that rewrites old records, then run: go test ./event -run TestShapes -update")
}

var (
	jsonMarshaler = reflect.TypeFor[json.Marshaler]()
	textMarshaler = reflect.TypeFor[encoding.TextMarshaler]()
	eventPkg      = reflect.TypeFor[Message]().PkgPath()
)

// shape lists every stored key path with its type, and fails on a field of
// this package with no json tag, since its Go name would become the key.
func shape(t *testing.T, ty reflect.Type, path string) []string {
	t.Helper()
	for ty.Kind() == reflect.Pointer {
		ty = ty.Elem()
	}
	switch {
	case ty.Implements(jsonMarshaler) || ty.Implements(textMarshaler):
		return []string{leaf(path, ty.String())}
	case ty.Kind() == reflect.Struct && ty.PkgPath() != eventPkg:
		// Another package's struct carries its own version, as viewspec.Spec does.
		return []string{leaf(path, ty.String())}
	}
	switch ty.Kind() {
	case reflect.Struct:
		var out []string
		for f := range ty.Fields() {
			if !f.IsExported() {
				continue
			}
			tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if !assert.NotEmpty(t, tag, "%s.%s has no json tag, so its Go name would be what is stored", ty.Name(), f.Name) {
				continue
			}
			if tag == "-" {
				continue
			}
			out = append(out, shape(t, f.Type, join(path, tag))...)
		}
		slices.Sort(out)
		return out
	case reflect.Slice, reflect.Array:
		return shape(t, ty.Elem(), path+"[]")
	case reflect.Map:
		return shape(t, ty.Elem(), path+"{}")
	default:
		return []string{leaf(path, ty.String())}
	}
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func leaf(path, ty string) string { return fmt.Sprintf("%s %s", path, ty) }
