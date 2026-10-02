package model

import (
	"encoding/json"
	"fmt"

	"github.com/vitzeno/detent/event"
)

// The chat-completions wire shapes.

type wireRequest struct {
	Model       string           `json:"model"`
	Messages    []wireMessage    `json:"messages"`
	Tools       []map[string]any `json:"tools,omitempty"`
	ToolChoice  string           `json:"tool_choice,omitempty"`
	Temperature float64          `json:"temperature,omitempty"`
}

type wireMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type wireToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name string `json:"name"`
		// Arguments is a JSON string, not an object, on every endpoint.
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type wireResponse struct {
	Choices []struct {
		Message struct {
			Content   string         `json:"content"`
			ToolCalls []wireToolCall `json:"tool_calls"`
			// Reasoning models put structured output here while
			// content stays empty.
			ReasoningContent string `json:"reasoning_content"`
			Reasoning        string `json:"reasoning"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Model string `json:"model"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func encode(m event.Message) wireMessage {
	w := wireMessage{Role: string(m.Role), Content: m.Content, ToolCallID: m.CallID}
	for _, c := range m.Calls {
		wc := wireToolCall{ID: c.ID, Type: "function"}
		wc.Function.Name = c.Name
		args, err := json.Marshal(c.Args)
		if err != nil {
			args = []byte("{}")
		}
		wc.Function.Arguments = string(args)
		w.ToolCalls = append(w.ToolCalls, wc)
	}
	return w
}

func decode(r wireResponse) (Reply, error) {
	if r.Error != nil && r.Error.Message != "" {
		return Reply{}, fmt.Errorf("endpoint: %s", r.Error.Message)
	}
	if len(r.Choices) == 0 {
		return Reply{}, fmt.Errorf("no choices in response")
	}
	c := r.Choices[0]
	out := Reply{Text: firstNonEmpty(c.Message.Content, c.Message.ReasoningContent, c.Message.Reasoning),
		Stop: c.FinishReason}
	for i, wc := range c.Message.ToolCalls {
		out.Calls = append(out.Calls, decodeCall(wc, i))
	}
	return out, nil
}

// decodeCall keeps a call whose arguments would not parse. Dropping it
// would leave the assistant message naming an id nothing answers.
func decodeCall(wc wireToolCall, i int) event.ToolCall {
	c := event.ToolCall{ID: wc.ID, Name: wc.Function.Name}
	if c.ID == "" {
		c.ID = fmt.Sprintf("call_%d", i) // some endpoints omit it
	}
	raw := wc.Function.Arguments
	if raw == "" {
		c.Args = map[string]any{}
		return c
	}
	if err := json.Unmarshal([]byte(raw), &c.Args); err != nil {
		c.Args = map[string]any{}
		c.Err = fmt.Sprintf("arguments were not valid JSON: %v", err)
	}
	return c
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
