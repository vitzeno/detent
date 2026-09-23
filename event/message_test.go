package event_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/vitzeno/detent/event"
)

// One renderer, because the row, the confirm box and the log must all
// name the same command: a human approves what the log then records.
func TestCommand(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args map[string]any
		want string
	}{
		{"bash is its own command", "bash",
			map[string]any{"command": "rm -rf build"}, "rm -rf build"},
		{"others read as tool(k=v), sorted", "read_file",
			map[string]any{"path": "main.go", "limit": 20}, "read_file limit=20 path=main.go"},
		{"absent optionals are left out", "read_file",
			map[string]any{"path": "main.go", "limit": nil}, "read_file path=main.go"},
		{"bash without its argument still names itself", "bash",
			map[string]any{}, "bash "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, event.Command(c.tool, c.args))
		})
	}
}
