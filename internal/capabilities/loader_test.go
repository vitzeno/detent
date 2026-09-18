package capabilities

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadFile_Unix(t *testing.T) {
	d, err := LoadFile("../../capabilities/unix.yaml")
	require.NoError(t, err)

	assert.Equal(t, "unix", d.Domain)
	assert.Equal(t, "always", d.AvailableWhen)
	require.Len(t, d.Actions, 10)

	byName := map[string]Action{}
	for _, a := range d.Actions {
		byName[a.Name] = a
	}

	rf, ok := byName["read_file"]
	require.True(t, ok, "read_file not loaded")
	assert.Equal(t, DangerSafe, rf.Danger)
	require.Len(t, rf.Args, 1)
	assert.Equal(t, Arg{Name: "path", Type: ArgPath, Required: true}, rf.Args[0])

	lf, ok := byName["list_files"]
	require.True(t, ok, "list_files not loaded")
	assert.Equal(t, "path[]", lf.Produces)
	require.Len(t, lf.Args, 1)
	assert.Equal(t, Arg{Name: "path", Type: ArgPath, Required: false}, lf.Args[0])

	pl, ok := byName["process_list"]
	require.True(t, ok, "process_list not loaded")
	assert.Equal(t, DangerSafe, pl.Danger)
	assert.Equal(t, "pid[]", pl.Produces)
	assert.Empty(t, pl.Args)

	kp, ok := byName["kill_process"]
	require.True(t, ok, "kill_process not loaded")
	assert.Equal(t, DangerDestructive, kp.Danger)
	require.Len(t, kp.Args, 1)
	assert.Equal(t, Arg{Name: "pid", Type: ArgPID, Required: true}, kp.Args[0])
}

func TestLoadFile_UnrecognizedExtension(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "domain.txt")
	require.NoError(t, os.WriteFile(path, []byte("domain: x\n"), 0o644))

	_, err := LoadFile(path)
	assert.Error(t, err)
}

func TestLoadFile_FailsLoudlyOnBadSchema(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{
			name: "missing domain",
			yaml: `
description: "no domain name"
available_when: always
actions: []
`,
		},
		{
			name: "missing available_when",
			yaml: `
domain: x
actions: []
`,
		},
		{
			name: "duplicate action",
			yaml: `
domain: x
available_when: always
actions:
  - {name: a, description: d, danger: safe, view: table, produces: none, reducer: none}
  - {name: a, description: d, danger: safe, view: table, produces: none, reducer: none}
`,
		},
		{
			name: "missing danger",
			yaml: `
domain: x
available_when: always
actions:
  - {name: a, description: d, view: table, produces: none, reducer: none}
`,
		},
		{
			name: "missing produces",
			yaml: `
domain: x
available_when: always
actions:
  - {name: a, description: d, danger: safe, view: table, reducer: none}
`,
		},
		{
			name: "missing reducer",
			yaml: `
domain: x
available_when: always
actions:
  - {name: a, description: d, danger: safe, view: table, produces: none}
`,
		},
		{
			name: "arg with no type",
			yaml: `
domain: x
available_when: always
actions:
  - name: a
    description: d
    danger: safe
    view: table
    produces: none
    reducer: none
    args:
      - {name: missing_type}
`,
		},
	}

	dir := t.TempDir()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, tt.name+".yaml")
			require.NoError(t, os.WriteFile(path, []byte(tt.yaml), 0o644))

			_, err := LoadFile(path)
			assert.Error(t, err)
		})
	}
}
