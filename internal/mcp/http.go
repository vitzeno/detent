package mcp

import (
	"net/http"
	"net/url"
	"strings"
	"time"

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

// client carries the configured headers to this server alone. It has no
// Timeout: the event stream lasts the session, and each tool call has its own deadline.
func (h HTTP) client() *http.Client {
	if len(h.Headers) == 0 {
		return &http.Client{}
	}
	return &http.Client{Transport: headers{origin: origin(h.URL), set: h.Headers}}
}

// authTimeout bounds each request a sign-in makes: discovery, registration
// and the token exchange, none of which waits on a human.
const authTimeout = 30 * time.Second

// authClient is what signing in uses. It carries no configured header,
// since its requests go to hosts the server's 401 names.
func authClient() *http.Client { return &http.Client{Timeout: authTimeout} }

// headers adds the configured headers to requests for one origin, so a
// redirect or a discovery request elsewhere never carries them.
type headers struct {
	origin string
	set    map[string]string
}

func (h headers) RoundTrip(r *http.Request) (*http.Response, error) {
	if h.origin == "" || origin(r.URL.String()) != h.origin {
		return http.DefaultTransport.RoundTrip(r)
	}
	// Cloned: a RoundTripper may not modify the request it is handed.
	r = r.Clone(r.Context())
	for k, v := range h.set {
		// A signed-in token is the server's own answer, newer than the config.
		if http.CanonicalHeaderKey(k) == "Authorization" && r.Header.Get("Authorization") != "" {
			continue
		}
		r.Header.Set(k, v)
	}
	return http.DefaultTransport.RoundTrip(r)
}

// origin is a URL's scheme and host, or "" when it has none.
func origin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
}
