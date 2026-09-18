package capabilities

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testDomain() Domain {
	return Domain{
		Domain:        "unix",
		AvailableWhen: "always",
		Actions: []Action{
			{Name: "list_files", Danger: DangerSafe, Produces: "path[]", Reducer: "none"},
			{Name: "read_file", Danger: DangerSafe, Produces: "text", Reducer: "none"},
		},
	}
}

func TestRegistry_Get(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.Load(testDomain()))

	tests := []struct {
		name          string
		qualifiedName string
		wantFound     bool
		wantAction    string
	}{
		{name: "loaded action", qualifiedName: "unix__read_file", wantFound: true, wantAction: "read_file"},
		{name: "unloaded action", qualifiedName: "unix__delete_everything", wantFound: false},
		{name: "unloaded domain", qualifiedName: "kubectl__read_file", wantFound: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg, ok := r.Get(tt.qualifiedName)
			assert.Equal(t, tt.wantFound, ok)
			if tt.wantFound {
				assert.Equal(t, "unix", reg.Domain)
				assert.Equal(t, tt.wantAction, reg.Action.Name)
			}
		})
	}
}

func TestRegistry_Load_RejectsDuplicateAcrossCalls(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.Load(testDomain()))

	err := r.Load(testDomain())
	assert.Error(t, err)
}

func TestRegistry_All_SortedByQualifiedName(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.Load(testDomain()))

	all := r.All()
	require.Len(t, all, 2)
	assert.Equal(t, "unix__list_files", all[0].QualifiedName)
	assert.Equal(t, "unix__read_file", all[1].QualifiedName)
}

// domainWithReducers mirrors testDomain but gives each action a real
// reducer name instead of "none", so ValidateReducers has something
// meaningful to check against — testDomain's "none" pair would trivially
// pass regardless of reducerNames.
func domainWithReducers() Domain {
	return Domain{
		Domain:        "unix",
		AvailableWhen: "always",
		Actions: []Action{
			{Name: "list_files", Danger: DangerSafe, Produces: "path[]", Reducer: "path_lines"},
			{Name: "read_file", Danger: DangerSafe, Produces: "text", Reducer: "text_summary"},
			{Name: "kill_process", Danger: DangerDestructive, Produces: "none", Reducer: "none"},
		},
	}
}

func TestRegistry_ValidateReducers(t *testing.T) {
	tests := []struct {
		name     string
		reducers map[string]bool
		wantErr  bool
	}{
		{name: "no reducers registered", reducers: map[string]bool{}, wantErr: true},
		{name: "one of two registered", reducers: map[string]bool{"path_lines": true}, wantErr: true},
		{
			name:     "both real reducers registered, none sentinel skipped",
			reducers: map[string]bool{"path_lines": true, "text_summary": true},
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRegistry()
			require.NoError(t, r.Load(domainWithReducers()))

			err := r.ValidateReducers(tt.reducers)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestRegistry_Validate(t *testing.T) {
	tests := []struct {
		name     string
		handlers map[string]bool
		wantErr  bool
	}{
		{name: "no handlers registered", handlers: map[string]bool{}, wantErr: true},
		{name: "one of two registered", handlers: map[string]bool{"unix__read_file": true}, wantErr: true},
		{
			name:     "every action has a handler",
			handlers: map[string]bool{"unix__read_file": true, "unix__list_files": true},
			wantErr:  false,
		},
		{
			name: "extra unrelated handlers don't matter",
			handlers: map[string]bool{
				"unix__read_file": true, "unix__list_files": true, "kubectl__get_pods": true,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRegistry()
			require.NoError(t, r.Load(testDomain()))

			err := r.Validate(tt.handlers)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
