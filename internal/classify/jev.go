package classify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
	"unicode/utf8"
)

// DefaultModel is the pinned model id, never an alias.
const DefaultModel = "jev-1.13.0"

var (
	// ErrNoModel is a JevJudge with no model to ask.
	ErrNoModel = errors.New("classify: JevJudge has no model")
	// ErrNoKind is a Question with none of Choice, Noul or Score set.
	ErrNoKind = errors.New("has none of Choice/Noul/Score set")
)

const (
	jevEndpoint = "https://api.typesafe.ai/v1/systemone"
	// maxResponseBytes bounds a reply. A real one is a few KB.
	maxResponseBytes = 1 << 20
)

// defaultClient is what a JevJudge with no client of its own uses.
var defaultClient = &http.Client{Timeout: 60 * time.Second}

// JevJudge asks TypeSafe's Jev over HTTP.
type JevJudge struct {
	model    string
	apiKey   string
	endpoint string
	client   *http.Client
}

// Option overrides a NewJevJudge default.
type Option func(*JevJudge)

// NewJevJudge returns a judge on DefaultModel and the public endpoint.
func NewJevJudge(apiKey string, opts ...Option) *JevJudge {
	j := &JevJudge{
		model:  DefaultModel,
		apiKey: apiKey,
	}
	for _, opt := range opts {
		opt(j)
	}
	return j
}

// WithModel pins a model id other than DefaultModel.
func WithModel(model string) Option {
	return func(j *JevJudge) { j.model = model }
}

// WithEndpoint points the judge somewhere other than the public API.
func WithEndpoint(endpoint string) Option {
	return func(j *JevJudge) { j.endpoint = endpoint }
}

// WithHTTPClient replaces the default client and its 60s timeout.
func WithHTTPClient(client *http.Client) Option {
	return func(j *JevJudge) { j.client = client }
}

// Ask sends every question in one request. An answer missing its value,
// or of another kind than asked, is left out rather than read as zero.
func (j *JevJudge) Ask(ctx context.Context, state State, qs Questions) (Answers, Usage, error) {
	if j == nil {
		return nil, Usage{}, errors.New("classify: nil JevJudge")
	}
	if j.model == "" {
		return nil, Usage{}, ErrNoModel
	}

	wireQs := make(map[string]wireQuestion, len(qs))
	for id, q := range qs {
		wq, err := toWireQuestion(q)
		if err != nil {
			return nil, Usage{}, fmt.Errorf("classify: question %q: %w", id, err)
		}
		wireQs[id] = wq
	}

	body, err := json.Marshal(wireRequest{Model: j.model, State: state, Questions: wireQs})
	if err != nil {
		return nil, Usage{}, fmt.Errorf("classify: encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, j.url(), bytes.NewReader(body))
	if err != nil {
		return nil, Usage{}, fmt.Errorf("classify: building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+j.apiKey)
	req.Header.Set("Content-Type", "application/json")

	t0 := time.Now()
	resp, err := j.httpClient().Do(req)
	if err != nil {
		return nil, Usage{}, fmt.Errorf("classify: request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // reading it was what mattered

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	latency := time.Since(t0)
	if err != nil {
		return nil, Usage{}, fmt.Errorf("classify: reading response: %w", err)
	}
	if len(respBody) > maxResponseBytes {
		return nil, Usage{}, fmt.Errorf("classify: response is over %d bytes", maxResponseBytes)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, Usage{}, &HTTPError{Status: resp.StatusCode, Body: truncate(respBody, 500)}
	}

	var wireResp wireResponse
	if err := json.Unmarshal(respBody, &wireResp); err != nil {
		return nil, Usage{}, fmt.Errorf("classify: decoding response: %w", err)
	}

	answers := make(Answers, len(wireResp.Answers))
	for id, wa := range wireResp.Answers {
		if q, asked := wireQs[id]; asked && answered(q.Type, wa) {
			answers[id] = toAnswer(wa)
		}
	}

	usage := Usage{
		InputTokens:  wireResp.Usage.InputTokens,
		OutputTokens: wireResp.Usage.OutputTokens,
		LatencyMS:    float64(latency.Microseconds()) / 1000.0,
		Model:        wireResp.Model,
	}
	return answers, usage, nil
}

// HTTPError is a reply that was not 200, so a caller can tell a bad key
// from an outage without reading the message.
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("classify: HTTP %d: %s", e.Status, e.Body) }

func (j *JevJudge) url() string {
	if j.endpoint != "" {
		return j.endpoint
	}
	return jevEndpoint
}

func (j *JevJudge) httpClient() *http.Client {
	if j.client != nil {
		return j.client
	}
	return defaultClient
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

func toWireQuestion(q Question) (wireQuestion, error) {
	switch {
	case q.Choice != nil:
		return wireQuestion{Type: "choice", Instructions: q.Instructions, Criteria: q.Choice.Criteria}, nil
	case q.Noul != nil:
		return wireQuestion{Type: "noul", Instructions: q.Instructions}, nil
	case q.Score != nil:
		return wireQuestion{Type: "score", Instructions: q.Instructions, Criteria: q.Score.Levels}, nil
	default:
		return wireQuestion{}, ErrNoKind
	}
}

// answered reports whether wa carries the value a question of kind asked for.
func answered(kind string, wa wireAnswer) bool {
	if wa.Type != "" && wa.Type != kind {
		return false
	}
	switch kind {
	case "choice":
		return wa.Choice != ""
	case "noul":
		return wa.Noul != nil
	case "score":
		return wa.Score != nil
	}
	return false
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

// truncate cuts b to at most n bytes without splitting a rune.
func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	for n > 0 && !utf8.RuneStart(b[n]) {
		n--
	}
	return string(b[:n]) + "…"
}
