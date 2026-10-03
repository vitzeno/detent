package logging

import (
	"go/build"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

// A fact describe has no case for is logged with no fields at all, so a
// new one must be given a case rather than fall through unnoticed.
func TestDescribe_EveryLoggedFactHasFields(t *testing.T) {
	for _, k := range event.Kinds() {
		e, err := event.Decode(k, []byte(`{}`))
		require.NoError(t, err)
		if !worthKeeping(e) || !carriesData(e) {
			continue
		}
		_, fields := describe(e)
		assert.NotEmpty(t, fields, "%s is logged with no fields", k)
	}
}

// carriesData says whether an event has any exported field to log.
func carriesData(e event.Event) bool {
	t := reflect.TypeOf(e)
	for f := range t.Fields() {
		if f.IsExported() {
			return true
		}
	}
	return false
}

// Anything logging imports, everything that logs imports too, and event
// is the one module package it may take. event logging would be a cycle.
func TestPackage_ImportsOnlyStdlibAndEvent(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	require.NoError(t, err)
	allowed := map[string]bool{
		"github.com/vitzeno/detent/event": true,
		"github.com/google/uuid":          true,
	}
	for _, imp := range pkg.Imports {
		first, _, _ := strings.Cut(imp, "/")
		if strings.Contains(first, ".") {
			assert.True(t, allowed[imp], "logging imports %s", imp)
		}
	}
}
