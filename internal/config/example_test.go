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

// The example file spells every key out so an editor reads it as YAML
// rather than as one long comment. That only helps if the keys are
// real: yaml.v3 drops an unknown one without a word, so a typo there
// is a setting that silently never applies.
func TestExample_NamesEveryKeyAndNoOthers(t *testing.T) {
	raw, err := os.ReadFile(examplePath)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &got))

	want := map[string]bool{}
	fields := reflect.TypeOf(Config{})
	for i := range fields.NumField() {
		if tag := fields.Field(i).Tag.Get("yaml"); tag != "" && tag != "-" {
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

// It also claims copying it as-is changes nothing, which is only true
// while every key it sets is either a built-in default or a zero the
// layering skips.
func TestExample_CopiedWholesaleChangesNothing(t *testing.T) {
	raw, err := os.ReadFile(examplePath)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), ".detent.yaml")
	require.NoError(t, os.WriteFile(path, raw, 0o644))

	file, err := Load(path)
	require.NoError(t, err)

	t.Setenv("DETENT_BASE_URL", "")
	t.Setenv("DETENT_MODEL", "")
	t.Setenv("DETENT_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("TYPESAFE_API_KEY", "")
	t.Setenv("DETENT_THEME", "")
	t.Setenv("DETENT_CONTEXT_TOKENS", "")

	withFile := Resolve(file, Config{}, -1)
	withNone := Resolve(Config{}, Config{}, -1)
	assert.Equal(t, withNone, withFile)
}
