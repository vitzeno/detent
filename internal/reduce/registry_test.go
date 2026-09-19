package reduce

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRegistry_Reduce(t *testing.T) {
	r := NewRegistry()
	r.Register("upper", func(output string) Result {
		return Result{Facts: map[string]any{"len": len(output)}}
	})

	tests := []struct {
		name       string
		reducer    string
		output     string
		wantErr    bool
		wantResult Result
	}{
		{
			name:       "registered reducer",
			reducer:    "upper",
			output:     "hello",
			wantResult: Result{Facts: map[string]any{"len": 5}},
		},
		{name: "unregistered reducer", reducer: "missing", output: "hello", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := r.Reduce(tt.reducer, tt.output)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func TestRegistry_Register_PanicsOnDuplicate(t *testing.T) {
	r := NewRegistry()
	noop := func(string) Result { return Result{} }
	r.Register("dup", noop)

	assert.Panics(t, func() { r.Register("dup", noop) })
}

func TestRegisterUnix_WiresAllReducers(t *testing.T) {
	r := NewRegistry()
	RegisterUnix(r)

	assert.Equal(t, map[string]bool{
		"path_lines": true, "text_summary": true, "process_lines": true,
		"port_listener_lines": true, "disk_usage_lines": true,
		"git_status_lines": true, "git_log_lines": true,
	}, r.Names())
}
