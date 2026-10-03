package mcp

import (
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"

	"github.com/vitzeno/detent/internal/capture"
)

func TestToResult_NonTextBlocksBecomeALineNotAPayload(t *testing.T) {
	res := toResult(&sdk.CallToolResult{Content: []sdk.Content{
		&sdk.TextContent{Text: "here is the chart"},
		&sdk.ImageContent{MIMEType: "image/png", Data: make([]byte, 12_700)},
		&sdk.AudioContent{MIMEType: "audio/wav", Data: make([]byte, 400)},
		&sdk.ResourceLink{URI: "file:///src/main.go"},
	}})

	assert.Equal(t, strings.Join([]string{
		"here is the chart",
		"[image image/png, 12.4KB]",
		"[audio audio/wav, 400B]",
		"[resource file:///src/main.go]",
	}, "\n"), res.Stdout)
}

// A server that inlines a file meant the model to read it.
func TestToResult_EmbeddedResourceKeepsItsText(t *testing.T) {
	res := toResult(&sdk.CallToolResult{Content: []sdk.Content{
		&sdk.EmbeddedResource{Resource: &sdk.ResourceContents{
			URI: "file:///main.go", Text: "package main",
		}},
	}})
	assert.Contains(t, res.Stdout, "package main")
	assert.Contains(t, res.Stdout, "file:///main.go")
}

// For the servers that do not duplicate it into a text block.
func TestToResult_FallsBackToStructuredContent(t *testing.T) {
	res := toResult(&sdk.CallToolResult{
		StructuredContent: map[string]any{"temperature": 22.5},
	})
	assert.Contains(t, res.Stdout, "22.5")
}

// Losing the tail silently is how a model reasons off a lie.
func TestToResult_SaysWhenItTruncated(t *testing.T) {
	res := toResult(&sdk.CallToolResult{Content: []sdk.Content{
		&sdk.TextContent{Text: strings.Repeat("x", capture.MaxOutputBytes*2)},
	}})
	assert.Len(t, res.Stdout, capture.MaxOutputBytes)
	assert.True(t, res.Truncated, "the tail went without saying so")
}
