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

const jevEndpoint = "https://api.typesafe.ai/v1/systemone"

// DefaultModel is the pinned model id; never use an alias.
const DefaultModel = "jev-1.13.0"

// JevJudge is the Judge implementation over HTTP.
type JevJudge struct {
	Model      string
	APIKey     string
	Endpoint   string
	HTTPClient *http.Client
}

func NewJevJudge(apiKey string, opts ...Option) *JevJudge {
	j := &JevJudge{
		Model:  DefaultModel,
		APIKey: apiKey,
	}
	for _, opt := range opts {
		opt(j)
	}
	return j
}

// Option overrides a NewJevJudge default.
type Option func(*JevJudge)

func WithModel(model string) Option {
	return func(j *JevJudge) { j.Model = model }
}

func WithEndpoint(endpoint string) Option {
	return func(j *JevJudge) { j.Endpoint = endpoint }
}

func WithHTTPClient(client *http.Client) Option {
	return func(j *JevJudge) { j.HTTPClient = client }
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

type wireRequest struct {
	Model     string                  `json:"model"`
	State     any                     `json:"state"`
	Questions map[string]wireQuestion `json:"questions"`
}

type wireQuestion struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
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

// Ask implements Judge.
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
		Model:        wireResp.Model,
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
