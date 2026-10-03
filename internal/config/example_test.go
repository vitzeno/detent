package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const examplePath = "../../detent.example.yaml"

// yaml.v3 drops an unknown key silently, so a typo in the example is a
// setting that never applies.
func TestExample_NamesEveryKeyAndNoOthers(t *testing.T) {
	raw, err := os.ReadFile(examplePath)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &got))

	want := map[string]bool{}
	fields := reflect.TypeOf(Config{})
	for field := range fields.Fields() {
		if tag := field.Tag.Get("yaml"); tag != "" && tag != "-" {
			want[strings.Split(tag, ",")[0]] = true
		}
	}
	for key := range got {
		assert.True(t, want[key], "%s is not a config key", key)
	}
	for key := range want {
		_, ok := got[key]
		assert.True(t, ok, "%s is a config key the example never mentions", key)
	}
}

// Copying the example as-is must change nothing: every key it sets is a
// built-in default or a zero the layering skips.
func TestExample_CopiedWholesaleChangesNothing(t *testing.T) {
	raw, err := os.ReadFile(examplePath)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), ".detent.yaml")
	require.NoError(t, os.WriteFile(path, raw, 0o644))

	file, err := Load(path)
	require.NoError(t, err)

	clearEnv(t)

	withFile := Resolve(file, Config{}, -1)
	withNone := Resolve(Config{}, Config{}, -1)
	// log_bodies: false is the default spelled out, not a change.
	assert.False(t, withFile.LogsBodies())
	withFile.LogBodies = nil
	assert.Equal(t, withNone, withFile)
}
