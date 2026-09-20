package propose

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/vitzeno/detent/internal/usage"
)

// Model is required by the wire format but ignored by LM Studio.
const (
	DefaultBaseURL = "http://localhost:1234/v1"
	DefaultModel   = "prism-ml/bonsai-27b"
)

// OpenAIProposer proposes via an OpenAI-compatible /chat/completions endpoint.
// This covers LM Studio and OpenRouter (https://openrouter.ai/api/v1) alike.
type OpenAIProposer struct {
	BaseURL string
	Model   string
	APIKey  string
	// Headers are extra request headers (OpenRouter's HTTP-Referer/X-Title).
	Headers      map[string]string
	HTTPClient   *http.Client
	SystemPrompt string
	// Env describes where commands actually run; the zero value falls
	// back to this process's own machine.
	Env Environment
}

func (p *OpenAIProposer) baseURL() string {
	if p.BaseURL != "" {
		return strings.TrimSuffix(p.BaseURL, "/")
	}
	return DefaultBaseURL
}

func (p *OpenAIProposer) model() string {
	if p.Model != "" {
		return p.Model
	}
	return DefaultModel
}

func (p *OpenAIProposer) systemPrompt() string {
	if p.SystemPrompt != "" {
		return p.SystemPrompt
	}
	env := p.Env
	if env.OS == "" {
		env = LocalEnvironment()
	}
	return defaultSystemPrompt(env)
}

func (p *OpenAIProposer) httpClient() *http.Client {
	if p.HTTPClient != nil {
		return p.HTTPClient
	}
	return &http.Client{Timeout: 60 * time.Second}
}

// Ping checks the endpoint is reachable via GET /models. Call it at
// startup to fail fast with a clear message instead of dying on the
// first goal with a raw dial error.
func Ping(ctx context.Context, baseURL, apiKey string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	url := strings.TrimSuffix(baseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("propose: ping: %w", err)
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return fmt.Errorf("propose: ping %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("propose: ping %s: HTTP %d", url, resp.StatusCode)
	}
	return nil
}

type wireMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type wireRequest struct {
	Model          string         `json:"model"`
	Messages       []wireMessage  `json:"messages"`
	ResponseFormat map[string]any `json:"response_format,omitempty"`
	Temperature    float64        `json:"temperature,omitempty"`
}

type wireResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
			// Reasoning models behind LM Studio put structured output
			// here while content stays empty — parsed as fallback below.
			ReasoningContent string `json:"reasoning_content"`
			// OpenRouter thinking models use this field instead.
			Reasoning string `json:"reasoning"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Model string `json:"model"`
}

func (p *OpenAIProposer) Propose(ctx context.Context, messages []Message) (Proposal, usage.Usage, error) {
	if len(messages) == 0 {
		return Proposal{}, usage.Usage{}, fmt.Errorf("propose: empty transcript, nothing to propose for")
	}

	wireMsgs := make([]wireMessage, 0, len(messages)+1)
	wireMsgs = append(wireMsgs, wireMessage{Role: "system", Content: p.systemPrompt()})
	for _, m := range messages {
		wm, err := toWireMessage(m)
		if err != nil {
			return Proposal{}, usage.Usage{}, err
		}
		wireMsgs = append(wireMsgs, wm)
	}

	body, err := json.Marshal(wireRequest{
		Model:          p.model(),
		Messages:       wireMsgs,
		ResponseFormat: responseFormat(),
		Temperature:    0.2,
	})
	if err != nil {
		return Proposal{}, usage.Usage{}, fmt.Errorf("propose: encoding request: %w", err)
	}

	url := p.baseURL() + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Proposal{}, usage.Usage{}, fmt.Errorf("propose: building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}

	t0 := time.Now()
	resp, err := p.httpClient().Do(req)
	latency := time.Since(t0)
	if err != nil {
		return Proposal{}, usage.Usage{}, fmt.Errorf("propose: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return Proposal{}, usage.Usage{}, fmt.Errorf("propose: reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Proposal{}, usage.Usage{}, fmt.Errorf("propose: HTTP %d: %s", resp.StatusCode, truncateStr(string(respBody), 500))
	}

	var wireResp wireResponse
	if err := json.Unmarshal(respBody, &wireResp); err != nil {
		return Proposal{}, usage.Usage{}, fmt.Errorf("propose: decoding response: %w", err)
	}
	if len(wireResp.Choices) == 0 {
		return Proposal{}, usage.Usage{}, fmt.Errorf("propose: response contained no choices")
	}
	used := usage.Usage{
		PromptTokens:     wireResp.Usage.PromptTokens,
		CompletionTokens: wireResp.Usage.CompletionTokens,
		Latency:          latency,
		Model:            cmp.Or(wireResp.Model, p.model()),
	}
	proposal, err := parseProposal(cmp.Or(
		wireResp.Choices[0].Message.Content,
		wireResp.Choices[0].Message.ReasoningContent,
		wireResp.Choices[0].Message.Reasoning,
	))
	if err != nil {
		return Proposal{}, used, err
	}
	return proposal, used, nil
}
