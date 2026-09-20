package classify

import "net/http"

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
