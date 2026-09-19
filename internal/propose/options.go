package propose

import "net/http"

// Option configures an OpenAIProposer. Only set what differs from the
// defaults: unset fields fall back to DefaultBaseURL/DefaultModel, no
// key, a 60s client, and the built-in system prompt.
type Option func(*OpenAIProposer)

func WithBaseURL(url string) Option {
	return func(p *OpenAIProposer) { p.BaseURL = url }
}

func WithModel(model string) Option {
	return func(p *OpenAIProposer) { p.Model = model }
}

func WithAPIKey(key string) Option {
	return func(p *OpenAIProposer) { p.APIKey = key }
}

func WithHeaders(headers map[string]string) Option {
	return func(p *OpenAIProposer) { p.Headers = headers }
}

func WithHTTPClient(client *http.Client) Option {
	return func(p *OpenAIProposer) { p.HTTPClient = client }
}

func WithSystemPrompt(prompt string) Option {
	return func(p *OpenAIProposer) { p.SystemPrompt = prompt }
}

// New builds a proposer from options; New() alone yields the defaults.
func New(opts ...Option) *OpenAIProposer {
	p := &OpenAIProposer{}
	for _, opt := range opts {
		opt(p)
	}
	return p
}
