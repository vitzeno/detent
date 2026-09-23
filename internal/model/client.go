package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/vitzeno/detent/event"
)

const (
	DefaultBaseURL = "http://localhost:1234/v1"
	DefaultModel   = "prism-ml/bonsai-27b"
)

// Client is one OpenAI-compatible endpoint. Covers LM Studio and
// OpenRouter alike.
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
}

// Complete is one Step. tools is the registry's schemas; nil asks for
// prose alone.
func (c *Client) Complete(ctx context.Context, msgs []Message, tools []map[string]any) (Reply, event.Usage, error) {
	if len(msgs) == 0 {
		return Reply{}, event.Usage{}, fmt.Errorf("model: empty transcript")
	}
	wire := make([]wireMessage, 0, len(msgs)+1)
	wire = append(wire, wireMessage{Role: string(RoleSystem), Content: c.systemPrompt()})
	for _, m := range msgs {
		wire = append(wire, encode(m))
	}
	return c.send(ctx, wireRequest{Messages: wire, Tools: tools, Temperature: 0.2})
}

// Ping fails fast at startup instead of dying on the first Turn with a
// raw dial error.
func Ping(ctx context.Context, baseURL, apiKey string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	url := strings.TrimSuffix(baseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("model: ping: %w", err)
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return fmt.Errorf("model: ping %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("model: ping %s: HTTP %d", url, resp.StatusCode)
	}
	return nil
}

func (c *Client) send(ctx context.Context, r wireRequest) (Reply, event.Usage, error) {
	r.Model = c.model()
	body, err := json.Marshal(r)
	if err != nil {
		return Reply{}, event.Usage{}, fmt.Errorf("model: encode: %w", err)
	}
	url := c.baseURL() + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Reply{}, event.Usage{}, fmt.Errorf("model: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}

	start := time.Now()
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return Reply{}, event.Usage{}, fmt.Errorf("model: %s: %w", url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return Reply{}, event.Usage{}, fmt.Errorf("model: read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Reply{}, event.Usage{}, fmt.Errorf("model: %s: HTTP %d: %s", url, resp.StatusCode, snippet(raw))
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
	return &http.Client{Timeout: 5 * time.Minute}
}

func (c *Client) systemPrompt() string {
	if c.SystemPrompt != "" {
		return c.SystemPrompt
	}
	env := c.Env
	if env.OS == "" {
		env = LocalEnvironment()
	}
	return systemPrompt(env)
}

// snippet bounds an error body so a 2MB HTML error page cannot become
// the error message.
func snippet(b []byte) string {
	const max = 300
	if len(b) > max {
		return string(b[:max]) + "…"
	}
	return string(b)
}
