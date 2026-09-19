package gate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/capabilities"
)

func TestLayer1Allowed(t *testing.T) {
	capReg := capabilities.NewRegistry()
	require.NoError(t, capReg.Load(capabilities.Domain{
		Domain:        "unix",
		AvailableWhen: "always",
		Actions: []capabilities.Action{
			{Name: "read_file", Danger: capabilities.DangerSafe, Produces: "text", Reducer: "none"},
		},
	}))

	tests := []struct {
		name          string
		qualifiedName string
		want          bool
	}{
		{name: "registered capability", qualifiedName: "unix__read_file", want: true},
		{name: "unregistered action", qualifiedName: "unix__kill_process", want: false},
		{name: "unregistered domain", qualifiedName: "kubectl__read_file", want: false},
		{name: "adversarial: not a real capability at all", qualifiedName: "rm -rf /", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Layer1Allowed(capReg, tt.qualifiedName))
		})
	}
}
