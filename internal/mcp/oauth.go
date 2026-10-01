package mcp

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

// oauthHandler signs in to one server. The SDK does the protocol; this
// supplies the browser leg, through signins, and the token file.
func oauthHandler(server string, a *Auth, client *http.Client, signins *SignIns) (*serial, error) {
	port, err := freePort()
	if err != nil {
		return nil, fmt.Errorf("mcp: %s: a port for the sign-in: %w", server, err)
	}
	redirect := fmt.Sprintf("http://127.0.0.1:%d/callback", port)
	cfg := &auth.AuthorizationCodeHandlerConfig{
		RedirectURL:              redirect,
		AuthorizationCodeFetcher: signins.fetcher(server, port),
		// Without it a token lasts an hour, and the link comes back with it.
		RequestRefreshToken: true,
		Client:              client,
		// Called after a code is exchanged: the one place the SDK shows
		// the client it registered, which a refresh needs next launch.
		NewTokenSource: func(ctx context.Context, c *oauth2.Config, tok *oauth2.Token) (oauth2.TokenSource, error) {
			saved := savedFrom(c, tok)
			if err := signins.tokens.Save(server, saved); err != nil {
				return nil, err
			}
			return &persisting{server: server, tokens: signins.tokens, saved: saved, src: c.TokenSource(ctx, tok)}, nil
		},
		DynamicClientRegistrationConfig: &auth.DynamicClientRegistrationConfig{
			Metadata: &oauthex.ClientRegistrationMetadata{
				ClientName:              "detent",
				RedirectURIs:            []string{redirect},
				GrantTypes:              []string{"authorization_code", "refresh_token"},
				ResponseTypes:           []string{"code"},
				TokenEndpointAuthMethod: "none",
			},
		},
	}
	if a.ClientIDMetadataURL != "" {
		cfg.ClientIDMetadataDocumentConfig = &auth.ClientIDMetadataDocumentConfig{URL: a.ClientIDMetadataURL}
	}
	if a.ClientID != "" {
		creds := &oauthex.ClientCredentials{ClientID: a.ClientID}
		if a.ClientSecret != "" {
			creds.ClientSecretAuth = &oauthex.ClientSecretAuth{ClientSecret: a.ClientSecret}
		}
		cfg.PreregisteredClient = creds
	}
	saved, err := signins.tokens.Load(server)
	if err != nil {
		return nil, err
	}
	if saved != nil {
		ctx := context.WithValue(context.Background(), oauth2.HTTPClient, client)
		src := saved.config(redirect).TokenSource(ctx, saved.Token)
		cfg.InitialTokenSource = &persisting{server: server, tokens: signins.tokens, saved: saved, src: src}
	}
	inner, err := auth.NewAuthorizationCodeHandler(cfg)
	if err != nil {
		return nil, fmt.Errorf("mcp: %s: %w", server, err)
	}
	return &serial{server: server, inner: inner, signins: signins}, nil
}

// serial lets one sign-in run per server. A request whose 401 came
// while another was signing in retries on the new token instead.
type serial struct {
	server  string
	inner   auth.OAuthHandler
	signins *SignIns
	mu      sync.Mutex
}

var _ auth.OAuthHandler = (*serial)(nil)

func (s *serial) TokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	return s.inner.TokenSource(ctx)
}

// Authorize reports a sign-in that asked a human, whichever way it
// ended, so a row waiting on it never waits on nothing.
func (s *serial) Authorize(ctx context.Context, req *http.Request, resp *http.Response) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.renewed(ctx, req) {
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.Body.Close()
	}
	s.signins.begin(s.server)
	err := s.inner.Authorize(ctx, req, resp)
	s.signins.end(s.server, err)
	return err
}

// renewed reports whether the token changed since req was sent.
func (s *serial) renewed(ctx context.Context, req *http.Request) bool {
	ts, err := s.inner.TokenSource(ctx)
	if err != nil || ts == nil {
		return false
	}
	tok, err := ts.Token()
	if err != nil || tok == nil {
		return false
	}
	return req.Header.Get("Authorization") != "Bearer "+tok.AccessToken
}

// persisting saves a token whenever it changes. Errors pass through
// untouched: the transport reads invalid_grant to start a new sign-in.
type persisting struct {
	server string
	tokens Tokens
	src    oauth2.TokenSource
	mu     sync.Mutex
	saved  *Saved
}

func (p *persisting) Token() (*oauth2.Token, error) {
	tok, err := p.src.Token()
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.saved.Token == nil || tok.AccessToken != p.saved.Token.AccessToken {
		next := *p.saved
		next.Token = tok
		// Unsaved is still a token: this session works, the next signs in.
		if p.tokens.Save(p.server, &next) == nil {
			p.saved = &next
		}
	}
	return tok, nil
}

func savedFrom(c *oauth2.Config, tok *oauth2.Token) *Saved {
	return &Saved{ClientID: c.ClientID, ClientSecret: c.ClientSecret,
		AuthURL: c.Endpoint.AuthURL, TokenURL: c.Endpoint.TokenURL, AuthStyle: c.Endpoint.AuthStyle,
		Scopes: c.Scopes, Token: tok}
}

// config is what a refresh needs, rebuilt from what a sign-in saved.
func (s *Saved) config(redirect string) *oauth2.Config {
	return &oauth2.Config{ClientID: s.ClientID, ClientSecret: s.ClientSecret, RedirectURL: redirect,
		Endpoint: oauth2.Endpoint{AuthURL: s.AuthURL, TokenURL: s.TokenURL, AuthStyle: s.AuthStyle},
		Scopes:   s.Scopes}
}

// freePort picks the redirect's port now, since the SDK takes the URI up
// front; it is listened on only if a sign-in happens.
func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}
