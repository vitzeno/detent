package mcp

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// HTTP is a server detent reaches rather than launches, over
// Streamable HTTP. Headers is where a bearer token goes.
type HTTP struct {
	URL     string
	Headers map[string]string
	// Auth signs in when the server answers 401. Nil is headers only.
	Auth auth.OAuthHandler
}

// Transport builds what Connect needs. The SDK takes no headers, so
// they ride on a client of our own.
func (h HTTP) Transport() sdk.Transport {
	return &sdk.StreamableClientTransport{Endpoint: h.URL, HTTPClient: h.client(), OAuthHandler: h.Auth}
}

// client is the one every request to this server goes out on, signing
// in included, so configured headers ride along there too.
func (h HTTP) client() *http.Client {
	if len(h.Headers) > 0 {
		return &http.Client{Transport: headers(h.Headers)}
	}
	return http.DefaultClient
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
