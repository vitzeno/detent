package classify

import "net/http"

// Option overrides a NewJevJudge default.
type Option func(*JevJudge)

// NewJevJudge returns a judge on DefaultModel and the public endpoint.
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

// WithModel pins a model id other than DefaultModel.
func WithModel(model string) Option {
	return func(j *JevJudge) { j.Model = model }
}

// WithEndpoint points the judge somewhere other than the public API.
func WithEndpoint(endpoint string) Option {
	return func(j *JevJudge) { j.Endpoint = endpoint }
}

// WithHTTPClient replaces the default client and its 60s timeout.
func WithHTTPClient(client *http.Client) Option {
	return func(j *JevJudge) { j.HTTPClient = client }
}
