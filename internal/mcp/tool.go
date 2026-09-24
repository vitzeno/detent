package mcp

import (
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/tool"
)

// Register adds a server's tools and reports what it registered. A
// taken name is renamed, never replaced: nothing may shadow bash.
func Register(reg *tool.Registry, s *Server, tools []*sdk.Tool) []string {
	var added []string
	for _, t := range tools {
		name := free(reg, toolName(s.Name, t.Name))
		reg.Register(Tool{name: name, remote: t.Name, server: s, spec: specOf(s.Name, t)})
		added = append(added, name)
	}
	return added
}

// Tool is one MCP tool, wearing the registry's interface. It lowers to
// a description rather than a command: nothing about it runs in a shell.
type Tool struct {
	name, remote string
	server       *Server
	spec         tool.Spec
}

func (t Tool) Name() string        { return t.name }
func (t Tool) Describe() tool.Spec { return t.spec }

// Lower renders the call for a human to read before approving it.
func (t Tool) Lower(args tool.Args) (string, error) {
	return event.Command(t.name, args), nil
}

// Remote is the name on the server, which namespacing changed.
func (t Tool) Remote() string { return t.remote }

// Server is who answers this call.
func (t Tool) Server() *Server { return t.server }

func specOf(server string, t *sdk.Tool) tool.Spec {
	return tool.Spec{
		Description: describeTool(t),
		Executor:    server,
		Raw:         rawSchema(t.InputSchema),
	}
}

// describeTool prefers a title when the server gave one, since that is
// what it wanted shown.
func describeTool(t *sdk.Tool) string {
	if t.Title != "" && t.Description != "" {
		return t.Title + ": " + t.Description
	}
	if t.Description != "" {
		return t.Description
	}
	return t.Title
}

// rawSchema keeps the server's schema as it came. An unusable one
// becomes "no parameters", which is what the spec recommends anyway.
func rawSchema(in any) map[string]any {
	if m, ok := in.(map[string]any); ok && len(m) > 0 {
		return m
	}
	return map[string]any{"type": "object", "additionalProperties": false}
}

// maxToolName is what the endpoints accept. MCP allows 128 and a dot;
// neither survives a chat-completions request.
const maxToolName = 64

// toolName namespaces a server's tool, because two servers offering
// "search" is the collision the spec warns aggregators about.
func toolName(server, remote string) string {
	name := clean(server) + "__" + clean(remote)
	if len(name) <= maxToolName {
		return name
	}
	return name[:maxToolName]
}

// clean keeps what a function name may hold, so a legal MCP name that
// no endpoint accepts still arrives as something.
func clean(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "tool"
	}
	return b.String()
}

// free finds a name nothing has taken, so registering can only ever
// add. Truncation makes collisions likelier than the namespace alone.
func free(reg *tool.Registry, name string) string {
	if _, taken := reg.Lookup(name); !taken {
		return name
	}
	for n := 2; n < 1000; n++ {
		suffix := fmt.Sprintf("_%d", n)
		next := name
		if len(next)+len(suffix) > maxToolName {
			next = next[:maxToolName-len(suffix)]
		}
		if _, taken := reg.Lookup(next + suffix); !taken {
			return next + suffix
		}
	}
	return name
}
