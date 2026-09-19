package classify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// jevEndpoint and DefaultModel are pinned, verified constants, not
// defaults chosen for convenience — see the adapter-level rules below.
const jevEndpoint = "https://api.typesafe.ai/v1/systemone"

// DefaultModel is the pinned model id validated by phase 0/0b's spikes.
// Adapter-level rule (§6.1): pin the model id, never an alias like
// "jev-latest" — an alias moving mid-run invalidates every threshold
// tuned against it. JevJudge.Model defaults to this but can be overridden
// (still expected to be a pinned id, never an alias).
const DefaultModel = "jev-1.13.0"

// JevJudge is the reference Judge implementation (§6.1) — TypeSafe's Jev
// model over HTTP. This function is the entire TypeSafe dependency in the
// codebase; nothing outside this file may import net/http for this purpose.
type JevJudge struct {
	Model      string // pinned, e.g. "jev-1.13.0" — never an alias
	APIKey     string
	Endpoint   string       // optional; defaults to jevEndpoint. Overridable for tests.
	HTTPClient *http.Client // optional; defaults to a client with a 60s timeout
}

// NewJevJudge builds a JevJudge with the pinned default model and a sane
// request timeout, matching phase 0's spike harness.
func NewJevJudge(apiKey string) *JevJudge {
	return &JevJudge{
		Model:  DefaultModel,
		APIKey: apiKey,
	}
}

func (j *JevJudge) endpoint() string {
	if j.Endpoint != "" {
		return j.Endpoint
	}
	return jevEndpoint
}

func (j *JevJudge) httpClient() *http.Client {
	if j.HTTPClient != nil {
		return j.HTTPClient
	}
	return &http.Client{Timeout: 60 * time.Second}
}

// wire* types are TypeSafe's actual JSON shape (confirmed against
// docs.typesafe.ai/api.md and /primitives/score.md while building this
// adapter) — kept private and separate from Questions/Answers so a
// protocol quirk here never leaks into the Judge interface everything
// else depends on.
type wireRequest struct {
	Model     string                  `json:"model"`
	State     any                     `json:"state"`
	Questions map[string]wireQuestion `json:"questions"`
}

type wireQuestion struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"` // map[string]string (choice) | []string (score)
}

type wireResponse struct {
	Model   string                `json:"model"`
	Answers map[string]wireAnswer `json:"answers"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type wireAnswer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice"`
	Noul          *float64           `json:"noul"`
	Score         *float64           `json:"score"`
	Confidence    *float64           `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
	Legend        map[string]string  `json:"legend"`
}

// Ask implements Judge. Builds one TypeSafe request, translates
// Choice/Noul/Score questions into TypeSafe's criteria shape, translates
// the response back into Answers + Usage.
func (j *JevJudge) Ask(ctx context.Context, state State, qs Questions) (Answers, Usage, error) {
	if j.Model == "" {
		return nil, Usage{}, fmt.Errorf("classify: JevJudge.Model is empty")
	}

	wireQs := make(map[string]wireQuestion, len(qs))
	for id, q := range qs {
		wq, err := toWireQuestion(q)
		if err != nil {
			return nil, Usage{}, fmt.Errorf("classify: question %q: %w", id, err)
		}
		wireQs[id] = wq
	}

	body, err := json.Marshal(wireRequest{Model: j.Model, State: state, Questions: wireQs})
	if err != nil {
		return nil, Usage{}, fmt.Errorf("classify: encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, j.endpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, Usage{}, fmt.Errorf("classify: building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+j.APIKey)
	req.Header.Set("Content-Type", "application/json")

	t0 := time.Now()
	resp, err := j.httpClient().Do(req)
	latency := time.Since(t0)
	if err != nil {
		return nil, Usage{}, fmt.Errorf("classify: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, Usage{}, fmt.Errorf("classify: reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, Usage{}, fmt.Errorf("classify: HTTP %d: %s", resp.StatusCode, truncate(respBody, 500))
	}

	var wireResp wireResponse
	if err := json.Unmarshal(respBody, &wireResp); err != nil {
		return nil, Usage{}, fmt.Errorf("classify: decoding response: %w", err)
	}

	answers := make(Answers, len(wireResp.Answers))
	for id, wa := range wireResp.Answers {
		answers[id] = toAnswer(wa)
	}

	usage := Usage{
		InputTokens:  wireResp.Usage.InputTokens,
		OutputTokens: wireResp.Usage.OutputTokens,
		LatencyMS:    float64(latency.Microseconds()) / 1000.0,
		Model:        wireResp.Model, // resolved id, reported even if it drifts from what we requested
	}
	return answers, usage, nil
}

func toWireQuestion(q Question) (wireQuestion, error) {
	switch {
	case q.Choice != nil:
		return wireQuestion{Type: "choice", Instructions: q.Instructions, Criteria: q.Choice.Criteria}, nil
	case q.Noul != nil:
		return wireQuestion{Type: "noul", Instructions: q.Instructions}, nil
	case q.Score != nil:
		return wireQuestion{Type: "score", Instructions: q.Instructions, Criteria: q.Score.Levels}, nil
	default:
		return wireQuestion{}, fmt.Errorf("has none of Choice/Noul/Score set")
	}
}

func toAnswer(wa wireAnswer) Answer {
	a := Answer{Choice: wa.Choice, Probabilities: wa.Probabilities, Legend: wa.Legend}
	if wa.Confidence != nil {
		a.Confidence = *wa.Confidence
	}
	if wa.Noul != nil {
		a.Noul = *wa.Noul
	}
	if wa.Score != nil {
		a.Score = *wa.Score
	}
	return a
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
