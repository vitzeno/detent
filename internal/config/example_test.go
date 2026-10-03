package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const examplePath = "detent.example.yaml"

// yaml.v3 drops an unknown key silently, so a typo in the example is a
// setting that never applies.
func TestExample_NamesEveryKeyAndNoOthers(t *testing.T) {
	raw, err := os.ReadFile(examplePath)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &got))

	want := map[string]bool{}
	fields := reflect.TypeFor[Config]()
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

	file, err := Load(path, nil)
	require.NoError(t, err)

	clearEnv(t)

	withFile := Resolve(file, Config{}, -1)
	withNone := Resolve(Config{}, Config{}, -1)
	// log_bodies: false is the default spelled out, not a change.
	assert.False(t, withFile.LogsBodies())
	withFile.LogBodies = nil
	assert.Equal(t, withNone, withFile)
}

// -init writes the shipped example, owner-only, into a directory it makes.
func TestWriteExample_WritesTheShippedFileOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new", "config.yaml")
	require.NoError(t, WriteExample(path))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	shipped, err := os.ReadFile(examplePath)
	require.NoError(t, err)
	assert.Equal(t, shipped, got, "the embedded copy is the file in the repository")
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "it will hold an API key")
	}
}

// A config the human already has is never replaced, whichever spelling it uses.
func TestWriteExample_LeavesAnExistingConfigAlone(t *testing.T) {
	for _, existing := range []string{"config.yaml", "config.yml"} {
		t.Run(existing, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, existing), []byte("model: mine\n"), 0o600))

			err := WriteExample(filepath.Join(dir, "config.yaml"))
			require.ErrorContains(t, err, "already exists")
			got, err := os.ReadFile(filepath.Join(dir, existing))
			require.NoError(t, err)
			assert.Equal(t, "model: mine\n", string(got))
		})
	}
}

func TestUserExists_SeesEitherSpelling(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // where Windows looks for home
	assert.False(t, UserExists())

	path, err := UserPath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".config", "detent", "config.yaml"), path)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(path), "config.yml"), nil, 0o600))
	assert.True(t, UserExists())
}
