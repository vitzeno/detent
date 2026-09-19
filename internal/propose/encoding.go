package propose

import (
	"encoding/json"
	"fmt"
	"strings"
)

type proposalWire struct {
	Command   string `json:"command"`
	Rationale string `json:"rationale"`
	Done      bool   `json:"done"`
	Summary   string `json:"summary"`
	File      string `json:"file"`
}

// EncodeAssistantTurn renders a Proposal in the JSON shape the model emits.
func EncodeAssistantTurn(p Proposal) string {
	raw, _ := json.Marshal(proposalWire{
		Command:   p.Command,
		Rationale: p.Rationale,
		Done:      p.Done,
		Summary:   p.Summary,
		File:      p.File,
	})
	return string(raw)
}

// parseProposal validates the done/command invariant: Command is empty iff Done.
func parseProposal(content string) (Proposal, error) {
	extracted := extractJSONObject(strings.TrimSpace(content))
	if extracted == "" {
		return Proposal{}, fmt.Errorf("propose: model returned no JSON object in %q", truncateStr(content, 500))
	}

	var w proposalWire
	dec := json.NewDecoder(strings.NewReader(extracted))
	if err := dec.Decode(&w); err != nil {
		return Proposal{}, fmt.Errorf("propose: decoding proposal JSON: %w", err)
	}

	w.Command = strings.TrimSpace(w.Command)
	if w.Done {
		w.Command = ""
		return Proposal{
			Rationale: strings.TrimSpace(w.Rationale),
			Done:      true,
			Summary:   strings.TrimSpace(w.Summary),
		}, nil
	}
	if w.Command == "" {
		return Proposal{}, fmt.Errorf("propose: model returned done=false with an empty command")
	}
	return Proposal{
		Command:   w.Command,
		Rationale: strings.TrimSpace(w.Rationale),
		File:      strings.TrimSpace(w.File),
	}, nil
}

// extractJSONObject slices from the first '{' to the last '}'.
func extractJSONObject(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end < 0 || end <= start {
		return ""
	}
	return s[start : end+1]
}

// Tool results go out as role user: hosted endpoints reject a bare tool role without a tool_call_id.
func toWireMessage(m Message) (wireMessage, error) {
	switch m.Role {
	case RoleUser:
		return wireMessage{Role: "user", Content: m.Content}, nil
	case RoleAssistant:
		return wireMessage{Role: "assistant", Content: m.Content}, nil
	case RoleTool:
		return wireMessage{Role: "user", Content: m.Content}, nil
	default:
		return wireMessage{}, fmt.Errorf("propose: unknown message role %q", m.Role)
	}
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
