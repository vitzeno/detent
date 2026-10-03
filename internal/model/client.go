package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vitzeno/detent/event"
)

// Hosted by default: it needs a key, but its window is large enough
// that the transcript budget stays a ceiling.
const (
	DefaultBaseURL = "https://openrouter.ai/api/v1"
	DefaultModel   = "openai/gpt-6-luna"
)

// retryWaits is the pause before each retry of a transient failure, a
// var so a test need not wait.
var retryWaits = []time.Duration{2 * time.Second, 8 * time.Second}

// maxRetryWait caps a Retry-After, so an endpoint cannot park a Turn.
const maxRetryWait = 30 * time.Second

// maxResponseBytes bounds a reply. A Step's answer is kilobytes.
const maxResponseBytes = 32 << 20

// instructionsSep sits between the built-in prompt and the project's own.
const instructionsSep = "\n\n"

// defaultHTTPClient bounds a Step that never answers.
var defaultHTTPClient = &http.Client{Timeout: 5 * time.Minute}

// Client is one OpenAI-compatible endpoint. Covers OpenRouter and a
// local LM Studio alike.
type Client struct {
	BaseURL string
	Model   string
	APIKey  string
	// Headers are extras some endpoints want (OpenRouter's HTTP-Referer).
	Headers      map[string]string
	HTTPClient   *http.Client
	SystemPrompt string
	// Env is what the prompt says about where commands run.
	Env Environment
	// Instructions are the project's own, appended to the built-in prompt,
	// and InstructionFiles name where they came from.
	Instructions     string
	InstructionFiles []string
}

// Complete is one Step. tools is the registry's schemas, and nil asks
// for prose alone.
func (c *Client) Complete(ctx context.Context, msgs []event.Message, tools []map[string]any) (Reply, event.Usage, error) {
	if len(msgs) == 0 {
		return Reply{}, event.Usage{}, errors.New("model: empty transcript")
	}
	wire := make([]wireMessage, 0, len(msgs)+1)
	wire = append(wire, wireMessage{Role: string(event.RoleSystem), Content: c.systemPrompt()})
	for _, m := range msgs {
		wire = append(wire, encode(m))
	}
	return c.send(ctx, wireRequest{Messages: wire, Tools: tools, Temperature: 0.2})
}

// Ping fails fast at startup instead of dying on the first Turn with a
// raw dial error. It authenticates exactly as Complete does.
func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	url := c.baseURL() + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("model: ping: %w", err)
	}
	c.authorize(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("model: ping %s: %w", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck // reading it was what mattered
	// Drained, within a bound, so the first Step can reuse the connection.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("model: ping %s: HTTP %d", url, resp.StatusCode)
	}
	return nil
}

// PromptPart is one named piece of the system prompt, for /context.
type PromptPart struct {
	Name, Detail string
	Bytes        int
}

// PromptParts names each piece of the system prompt and its size. A new
// piece belongs here, so /context labels it without being told.
func (c *Client) PromptParts() []PromptPart {
	if c.SystemPrompt != "" {
		return []PromptPart{{Name: "system prompt", Detail: "set by the caller", Bytes: len(c.SystemPrompt)}}
	}
	parts := []PromptPart{{Name: "detent", Detail: "environment and rules", Bytes: len(systemPrompt(c.env()))}}
	if c.Instructions != "" {
		parts = append(parts, PromptPart{Name: "instructions", Detail: strings.Join(c.InstructionFiles, ", "),
			Bytes: len(c.Instructions) + len(instructionsSep)})
	}
	return parts
}

// StatusError is a reply other than 200, typed so a transient failure
// can be told from a bad request without reading the message.
type StatusError struct {
	URL        string
	Code       int
	RetryAfter time.Duration
	Body       string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("model: %s: HTTP %d: %s", e.URL, e.Code, e.Body)
}

// Temporary reports whether asking again later could succeed.
func (e *StatusError) Temporary() bool {
	return e.Code == http.StatusTooManyRequests || e.Code >= http.StatusInternalServerError
}

// send posts one request, retrying a 429 or 5xx a bounded number of
// times so one transient failure does not end a Turn.
func (c *Client) send(ctx context.Context, r wireRequest) (Reply, event.Usage, error) {
	r.Model = c.model()
	body, err := json.Marshal(r)
	if err != nil {
		return Reply{}, event.Usage{}, fmt.Errorf("model: encode: %w", err)
	}
	for attempt := 0; ; attempt++ {
		reply, used, err := c.post(ctx, body)
		var se *StatusError
		if !errors.As(err, &se) || !se.Temporary() || attempt >= len(retryWaits) {
			return reply, used, err
		}
		wait := retryWaits[attempt]
		if se.RetryAfter > 0 {
			wait = min(se.RetryAfter, maxRetryWait)
		}
		select {
		case <-ctx.Done():
			return Reply{}, event.Usage{}, err
		case <-time.After(wait):
		}
	}
}

func (c *Client) post(ctx context.Context, body []byte) (Reply, event.Usage, error) {
	url := c.baseURL() + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Reply{}, event.Usage{}, fmt.Errorf("model: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.authorize(req)

	start := time.Now()
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return Reply{}, event.Usage{}, fmt.Errorf("model: %s: %w", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck // reading it was what mattered
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return Reply{}, event.Usage{}, fmt.Errorf("model: read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Reply{}, event.Usage{}, &StatusError{URL: url, Code: resp.StatusCode,
			RetryAfter: retryAfter(resp.Header.Get("Retry-After")), Body: snippet(raw)}
	}
	if len(raw) > maxResponseBytes {
		return Reply{}, event.Usage{}, fmt.Errorf("model: %s: reply over %d bytes", url, maxResponseBytes)
	}

	var wr wireResponse
	if err := json.Unmarshal(raw, &wr); err != nil {
		return Reply{}, event.Usage{}, fmt.Errorf("model: decode: %w: %s", err, snippet(raw))
	}
	used := event.Usage{
		PromptTokens:     wr.Usage.PromptTokens,
		CompletionTokens: wr.Usage.CompletionTokens,
		Latency:          time.Since(start),
		Model:            wr.Model,
	}
	reply, err := decode(wr)
	if err != nil {
		return Reply{}, used, fmt.Errorf("model: %w", err)
	}
	return reply, used, nil
}

// authorize sets the key and the configured headers, the same on every request.
func (c *Client) authorize(req *http.Request) {
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}
}

// retryAfter reads a Retry-After in seconds. The HTTP-date form is rare
// from an API and counts as absent.
func retryAfter(v string) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}

func (c *Client) systemPrompt() string {
	if c.SystemPrompt != "" {
		return c.SystemPrompt
	}
	if c.Instructions == "" {
		return systemPrompt(c.env())
	}
	return systemPrompt(c.env()) + instructionsSep + c.Instructions
}

// env is where commands run, this machine when the harness said nothing.
func (c *Client) env() Environment {
	if c.Env.OS == "" {
		return LocalEnvironment()
	}
	return c.Env
}

func (c *Client) baseURL() string {
	if c.BaseURL != "" {
		return strings.TrimSuffix(c.BaseURL, "/")
	}
	return DefaultBaseURL
}

func (c *Client) model() string {
	if c.Model != "" {
		return c.Model
	}
	return DefaultModel
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return defaultHTTPClient
}

// snippet bounds an error body so a 2MB HTML error page cannot become
// the error message.
func snippet(b []byte) string {
	const limit = 300
	if len(b) > limit {
		return strings.ToValidUTF8(string(b[:limit]), "") + "…"
	}
	return string(b)
}
