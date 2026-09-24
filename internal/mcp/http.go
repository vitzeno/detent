package mcp

import (
	"net/http"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// HTTP is a server detent reaches rather than launches, over
// Streamable HTTP. Headers is where a bearer token goes.
type HTTP struct {
	URL     string
	Headers map[string]string
}

// Transport builds what Connect needs. The SDK takes no headers, so
// they ride on a client of our own.
func (h HTTP) Transport() sdk.Transport {
	client := http.DefaultClient
	if len(h.Headers) > 0 {
		client = &http.Client{Transport: headers(h.Headers)}
	}
	return &sdk.StreamableClientTransport{Endpoint: h.URL, HTTPClient: client}
}

// headers adds the configured headers to every request.
type headers map[string]string

func (h headers) RoundTrip(r *http.Request) (*http.Response, error) {
	// Cloned: a RoundTripper may not modify the request it is handed.
	r = r.Clone(r.Context())
	for k, v := range h {
		r.Header.Set(k, v)
	}
	return http.DefaultTransport.RoundTrip(r)
}
