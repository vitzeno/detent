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
		tool event.ToolName
		args map[string]any
		want string
	}{
		{"bash is its own command", "bash",
			map[string]any{"command": "rm -rf build"}, "rm -rf build"},
		{"others read as tool(k=v), sorted", "read_file",
			map[string]any{"path": "main.go", "limit": 20}, "read_file limit=20 path=main.go"},
		{"absent optionals are left out", "read_file",
			map[string]any{"path": "main.go", "limit": nil}, "read_file path=main.go"},
		{"powershell is its own command too", "powershell",
			map[string]any{"command": "Remove-Item -Recurse build"}, "Remove-Item -Recurse build"},
		{"bash without its argument still names itself", "bash",
			map[string]any{}, "bash"},
		{"a value with a space cannot pass for a second argument", "srv__send",
			map[string]any{"to": "me other=x"}, `srv__send to="me other=x"`},
		{"quotes and escapes are escaped", "srv__send",
			map[string]any{"body": "say \"hi\"\x1b[2J"}, `srv__send body="say \"hi\"\x1b[2J"`},
		{"an empty string is visible", "srv__send",
			map[string]any{"to": ""}, `srv__send to=""`},
		{"structured values are JSON", "srv__send",
			map[string]any{"tags": []any{"a b", "c"}, "n": 2.5, "ok": true},
			`srv__send n=2.5 ok=true tags=["a b","c"]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, event.Command(c.tool, c.args))
		})
	}
}
