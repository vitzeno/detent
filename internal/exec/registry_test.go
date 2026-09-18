package exec

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// echoHandler is a minimal fake used to test Registry's own mechanics in
// isolation from any real filesystem behavior — that belongs to unix_test.go.
func echoHandler(_ context.Context, args Args) (Result, error) {
	msg, ok := args["msg"].(string)
	if !ok {
		return Result{}, fmt.Errorf("echo: msg is required")
	}
	return Result{Output: msg}, nil
}

func TestRegistry_Dispatch(t *testing.T) {
	r := NewRegistry()
	r.Register("test__echo", echoHandler)

	tests := []struct {
		name          string
		qualifiedName string
		args          Args
		wantOutput    string
		wantErr       bool
	}{
		{name: "known handler, valid args", qualifiedName: "test__echo", args: Args{"msg": "hi"}, wantOutput: "hi"},
		{name: "known handler, handler-level error", qualifiedName: "test__echo", args: Args{}, wantErr: true},
		{name: "unregistered handler", qualifiedName: "test__missing", args: Args{}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := r.Dispatch(context.Background(), tt.qualifiedName, tt.args)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantOutput, result.Output)
		})
	}
}

func TestRegistry_Register_PanicsOnDuplicate(t *testing.T) {
	r := NewRegistry()
	r.Register("test__echo", echoHandler)

	assert.Panics(t, func() { r.Register("test__echo", echoHandler) })
}

func TestRegistry_Names(t *testing.T) {
	r := NewRegistry()
	r.Register("test__echo", echoHandler)
	r.Register("test__other", echoHandler)

	assert.Equal(t, map[string]bool{"test__echo": true, "test__other": true}, r.Names())
}
