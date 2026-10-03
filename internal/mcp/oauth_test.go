package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/tool"
)

// The whole sign-in, as a human at a browser would do it: a link, a
// redirect, a token on disk, and the server's tools offered.
func TestSignIn_EndToEnd(t *testing.T) {
	f := newFakeAuth(t)
	r := rig(t, Tokens{Dir: t.TempDir()})
	seen := record(t, r.bus)
	browse(t, r.bus)

	errs := r.connect(context.Background(), map[string]Config{"notion": {URL: f.url() + "/mcp"}})
	require.Empty(t, errs)
	_, ok := r.reg.Lookup("notion__ping")
	assert.True(t, ok, "the server's tools are offered once signed in")

	saved, err := r.tokens.Load("notion", f.url()+"/mcp")
	require.NoError(t, err)
	require.NotNil(t, saved)
	assert.Equal(t, f.url()+"/token", saved.TokenURL)
	assert.NotEmpty(t, saved.Token.RefreshToken)

	events := seen()
	assert.Contains(t, kinds(events), event.AuthorizationWaitingKind)
	assert.Contains(t, kinds(events), event.ServerAuthorizedKind)
	assert.NotContains(t, kinds(events), event.AuthorizationFailedKind)
	assert.Equal(t, event.AuthSignedIn, r.in.Status()[0].Auth)
	assert.Equal(t, []string{"S256"}, f.methods, "PKCE is S256, never plain")
}

// The code and the tokens stay in this process: nothing a store, a log
// or a front-end sees carries one.
func TestSignIn_PutsNoSecretOnTheBus(t *testing.T) {
	f := newFakeAuth(t)
	r := rig(t, Tokens{Dir: t.TempDir()})
	seen := record(t, r.bus)
	browse(t, r.bus)
	require.Empty(t, r.connect(context.Background(), map[string]Config{"notion": {URL: f.url() + "/mcp"}}))

	var published strings.Builder
	for _, e := range seen() {
		raw, err := event.Encode(e)
		require.NoError(t, err)
		published.Write(raw)
	}
	require.NotEmpty(t, f.secrets)
	for _, secret := range f.secrets {
		assert.NotContains(t, published.String(), secret)
	}
}

// The redirect comes back to this machine alone, never every interface.
func TestSignIn_RedirectsToLoopback(t *testing.T) {
	f := newFakeAuth(t)
	r := rig(t, Tokens{Dir: t.TempDir()})
	browse(t, r.bus)
	require.Empty(t, r.connect(context.Background(), map[string]Config{"notion": {URL: f.url() + "/mcp"}}))
	redirect, err := url.Parse(f.redirect)
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1", redirect.Hostname())
}

// The second launch reads the token the first saved: no link at all.
func TestSignIn_ASecondLaunchSignsInFromTheSavedToken(t *testing.T) {
	f := newFakeAuth(t)
	tokens := Tokens{Dir: t.TempDir()}
	first := rig(t, tokens)
	browse(t, first.bus)
	servers := map[string]Config{"notion": {URL: f.url() + "/mcp"}}
	require.Empty(t, first.connect(context.Background(), servers))

	second := rig(t, tokens)
	seen := record(t, second.bus)
	require.Empty(t, second.connect(context.Background(), servers))
	assert.NotContains(t, kinds(seen()), event.AuthorizationWaitingKind)
	assert.Equal(t, event.AuthSignedIn, second.in.Status()[0].Auth)
}

// A project config that reuses a signed-in server's name with its own
// URL must not receive that server's token, on any request at all.
func TestSignIn_ATokenNeverFollowsItsNameToAnotherServer(t *testing.T) {
	f := newFakeAuth(t)
	var mu sync.Mutex
	var auths []string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auths = append(auths, r.Header.Get("Authorization"))
		mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(other.Close)

	tokens := Tokens{Dir: t.TempDir()}
	first := rig(t, tokens)
	browse(t, first.bus)
	require.Empty(t, first.connect(context.Background(), map[string]Config{"notion": {URL: f.url() + "/mcp"}}))

	second := rig(t, tokens)
	_ = second.connect(context.Background(), map[string]Config{"notion": {URL: other.URL + "/mcp"}})
	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, auths, "the other server was never asked")
	for _, a := range auths {
		assert.Empty(t, a, "the token went to a server it was not issued by")
	}
}

// After a fresh sign-in is asked for, the old session refreshing its
// token must not write back the file the human asked to forget.
func TestPersisting_ARetiredSessionNeverSavesAgain(t *testing.T) {
	for _, retired := range []bool{false, true} {
		tokens := Tokens{Dir: t.TempDir()}
		signins := NewSignIns(nil, nil, tokens, nil)
		p := &persisting{server: "notion", resource: notionURL, signins: signins,
			gen: signins.generation("notion"), saved: saved(),
			src: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "refreshed"})}
		if retired {
			require.NoError(t, signins.retire("notion", notionURL))
		}
		tok, err := p.Token()
		require.NoError(t, err)
		assert.Equal(t, "refreshed", tok.AccessToken, "a retired session still works")

		got, err := tokens.Load("notion", notionURL)
		require.NoError(t, err)
		if retired {
			assert.Nil(t, got, "a forgotten token came back")
		} else {
			require.NotNil(t, got)
			assert.Equal(t, "refreshed", got.Token.AccessToken)
		}
	}
}

// A token past its expiry is refreshed, and the new one is what is on
// disk afterwards, so the launch after that does not refresh again.
func TestSignIn_RefreshesAnExpiredTokenAndSavesIt(t *testing.T) {
	f := newFakeAuth(t)
	f.expiresIn = 1 // inside oauth2's ten-second margin: expired on arrival
	tokens := Tokens{Dir: t.TempDir()}
	first := rig(t, tokens)
	browse(t, first.bus)
	servers := map[string]Config{"notion": {URL: f.url() + "/mcp"}}
	require.Empty(t, first.connect(context.Background(), servers))
	before, err := tokens.Load("notion", f.url()+"/mcp")
	require.NoError(t, err)

	second := rig(t, tokens)
	seen := record(t, second.bus)
	require.Empty(t, second.connect(context.Background(), servers))
	after, err := tokens.Load("notion", f.url()+"/mcp")
	require.NoError(t, err)
	assert.NotEqual(t, before.Token.AccessToken, after.Token.AccessToken)
	assert.NotContains(t, kinds(seen()), event.AuthorizationWaitingKind, "a refresh asks nobody")
}

// Nobody came: the link expires, says so, and the server is signed out.
func TestSignIn_ExpiresWhenNobodySignsIn(t *testing.T) {
	f := newFakeAuth(t)
	r := rig(t, Tokens{Dir: t.TempDir()})
	r.signins.wait = 150 * time.Millisecond
	seen := record(t, r.bus)

	errs := r.connect(context.Background(), map[string]Config{"notion": {URL: f.url() + "/mcp"}})
	require.Len(t, errs, 1)
	_, ok := r.reg.Lookup("notion__ping")
	assert.False(t, ok)
	assert.Equal(t, event.AuthSignedOut, r.in.Status()[0].Auth)
	assert.Eventually(t, func() bool { return failedWith(seen(), "the link expired") }, 2*time.Second, 10*time.Millisecond)
}

// esc on a waiting Call, or quitting: the wait ends at once.
func TestSignIn_StopsWithItsContext(t *testing.T) {
	f := newFakeAuth(t)
	r := rig(t, Tokens{Dir: t.TempDir()})
	seen := record(t, r.bus)
	waiting, stop := r.bus.Subscribe(event.Only(event.AuthorizationWaitingKind))
	defer stop()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan []error)
	go func() {
		done <- r.connect(ctx, map[string]Config{"notion": {URL: f.url() + "/mcp"}})
	}()
	<-waiting
	cancel()
	select {
	case errs := <-done:
		assert.Len(t, errs, 1)
	case <-time.After(2 * time.Second):
		t.Fatal("a cancelled sign-in kept waiting")
	}
	assert.Eventually(t, func() bool { return failedWith(seen(), "stopped") }, 2*time.Second, 10*time.Millisecond)
}

func TestSignIn_SaysWhenTheServerRefuses(t *testing.T) {
	f := newFakeAuth(t)
	f.deny = true
	r := rig(t, Tokens{Dir: t.TempDir()})
	seen := record(t, r.bus)
	browse(t, r.bus)
	require.Len(t, r.connect(context.Background(), map[string]Config{"notion": {URL: f.url() + "/mcp"}}), 1)
	var reason string
	for _, e := range seen() {
		if failed, ok := e.(event.AuthorizationFailed); ok {
			reason = failed.Reason
		}
	}
	assert.Contains(t, reason, "access_denied")
}

// A request that is not this sign-in's is refused, and the real one
// still gets through afterwards.
func TestSignIn_RefusesAStrayRedirect(t *testing.T) {
	f := newFakeAuth(t)
	r := rig(t, Tokens{Dir: t.TempDir()})
	waiting, stop := r.bus.Subscribe(event.Only(event.AuthorizationWaitingKind))
	defer stop()
	stray := make(chan int, 1)
	go func() {
		for rec := range waiting {
			link := rec.Event.(event.AuthorizationWaiting).URL
			u, _ := url.Parse(link)
			back := u.Query().Get("redirect_uri") + "?code=forged&state=wrong"
			if resp, err := http.Get(back); err == nil {
				stray <- resp.StatusCode
				resp.Body.Close()
			}
			if resp, err := http.Get(link); err == nil {
				resp.Body.Close()
			}
		}
	}()
	require.Empty(t, r.connect(context.Background(), map[string]Config{"notion": {URL: f.url() + "/mcp"}}))
	select {
	case code := <-stray:
		assert.Equal(t, http.StatusBadRequest, code)
	case <-time.After(5 * time.Second):
		t.Fatal("the stray redirect was never answered")
	}
}

// A provider that whitelists one redirect needs the port fixed, and one
// with scopes to choose needs them asked for: Claude Code's oauth fields.
func TestSignIn_HonoursClaudeCodesOAuthFields(t *testing.T) {
	f := newFakeAuth(t)
	port, err := freePort()
	require.NoError(t, err)
	r := rig(t, Tokens{Dir: t.TempDir()})
	browse(t, r.bus)
	require.Empty(t, r.connect(context.Background(), map[string]Config{"notion": {URL: f.url() + "/mcp",
		OAuth: &OAuth{CallbackPort: port, Scopes: Scopes{"read", "write"}}}}))

	redirect, err := url.Parse(f.redirect)
	require.NoError(t, err)
	assert.Equal(t, fmt.Sprint(port), redirect.Port())
	// A set: the SDK merges scopes with any granted before, in no order.
	assert.ElementsMatch(t, []string{"read", "write"}, strings.Fields(f.scope))
}

// Signing in is the server's to ask for. One that answers without a 401
// is never asked about, never shown a link, and never called signed in.
func TestSignIn_AServerThatNeverAsksIsNeverSignedIn(t *testing.T) {
	plain := httpServer(t, nil) // before the rig, so it closes after the session
	r := rig(t, Tokens{Dir: t.TempDir()})
	seen := record(t, r.bus)
	require.Empty(t, r.connect(context.Background(), map[string]Config{"plain": {URL: plain}}))
	assert.NotContains(t, kinds(seen()), event.AuthorizationWaitingKind)
	assert.Empty(t, r.in.Status()[0].Auth)
	entries, err := os.ReadDir(r.tokens.Dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "and nothing is registered or saved for it")
}

// A waiting link opens in a browser on request, and only while waiting.
func TestSignIns_OpensOnlyAWaitingLink(t *testing.T) {
	f := newFakeAuth(t)
	r := rig(t, Tokens{Dir: t.TempDir()})
	opened := make(chan string, 1)
	r.signins.open = func(link string) error { opened <- link; return nil }
	assert.Error(t, r.signins.Open("notion"), "nothing is waiting yet")

	waiting, stop := r.bus.Subscribe(event.Only(event.AuthorizationWaitingKind))
	defer stop()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = r.connect(ctx, map[string]Config{"notion": {URL: f.url() + "/mcp"}})
	}()
	// Awaited, so nothing registers after the rig has closed.
	defer func() { cancel(); <-done }()
	link := (<-waiting).Event.(event.AuthorizationWaiting).URL
	require.NoError(t, r.signins.Open("notion"))
	assert.Equal(t, link, <-opened)
}

// One server waiting for a human must not keep another's tools away.
func TestConnectAll_AWaitingSignInHoldsUpNoOtherServer(t *testing.T) {
	f := newFakeAuth(t)
	// Before the rig: cleanup runs last first, and a server closed while
	// a session is open waits for it.
	plain := httpServer(t, nil)
	r := rig(t, Tokens{Dir: t.TempDir()})
	ctx, cancel := context.WithCancel(context.Background())
	plainReady := make(chan bool, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ConnectAll(ctx, r.reg, r.in, map[string]Config{
			"notion": {URL: f.url() + "/mcp"},
			"plain":  {URL: plain},
		}, func(s event.ServerSummary) {
			if s.Name == "plain" {
				_, ok := r.reg.Lookup("plain__ping")
				plainReady <- ok
			}
		}, WithSignIns(r.signins))
	}()
	defer func() { cancel(); <-done }()
	select {
	case ok := <-plainReady:
		assert.True(t, ok, "plain's tools are registered by the time it reports")
	case <-time.After(5 * time.Second):
		t.Fatal("a server waiting on a sign-in held up another")
	}
}

// Two requests refused together make one sign-in, not two links: the
// second retries on the token the first got.
func TestSerial_SignsInOnceForRequestsRefusedTogether(t *testing.T) {
	inner := &fakeHandler{release: make(chan struct{}), token: "old"}
	s := &serial{server: "notion", inner: inner, signins: NewSignIns(nil, nil, Tokens{}, nil)}
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			req := httptest.NewRequest("POST", "/mcp", nil)
			req.Header.Set("Authorization", "Bearer old")
			resp := &http.Response{Body: io.NopCloser(strings.NewReader(""))}
			assert.NoError(t, s.Authorize(context.Background(), req, resp))
		})
	}
	require.Eventually(t, func() bool { return inner.calls.Load() == 1 }, time.Second, 5*time.Millisecond)
	close(inner.release)
	wg.Wait()
	assert.Equal(t, int32(1), inner.calls.Load())
}

// fakeAuth is an authorization server and the MCP server it guards, on
// one httptest server: everything a sign-in touches, with no network.
type fakeAuth struct {
	srv       *httptest.Server
	expiresIn int
	deny      bool

	mu       sync.Mutex
	n        int
	access   map[string]bool
	refresh  map[string]bool
	codes    map[string]string // code to its PKCE challenge
	methods  []string          // code_challenge_method each authorize carried
	scope    string            // the scope the last authorize asked for
	redirect string            // the redirect URI registered last
	secrets  []string          // every code and token issued, to look for later
}

func newFakeAuth(t *testing.T) *fakeAuth {
	t.Helper()
	f := &fakeAuth{expiresIn: 3600, access: map[string]bool{}, refresh: map[string]bool{}, codes: map[string]string{}}
	server := sdk.NewServer(&sdk.Implementation{Name: "guarded", Version: "1"}, nil)
	server.AddTool(&sdk.Tool{Name: "ping", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "pong"}}}, nil
		})
	mcp := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		ok := f.access[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		f.mu.Unlock()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+f.url()+`/.well-known/oauth-protected-resource/mcp"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mcp.ServeHTTP(w, r)
	})
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"resource": f.url() + "/mcp", "authorization_servers": []string{f.url()}})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"issuer": f.url(), "authorization_endpoint": f.url() + "/authorize",
			"token_endpoint": f.url() + "/token", "registration_endpoint": f.url() + "/register",
			"response_types_supported": []string{"code"}, "code_challenge_methods_supported": []string{"S256"},
			"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
			"token_endpoint_auth_methods_supported": []string{"none"},
		})
	})
	mux.HandleFunc("/register", f.register)
	mux.HandleFunc("/authorize", f.authorize)
	mux.HandleFunc("/token", f.token)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAuth) url() string { return f.srv.URL }

func (f *fakeAuth) register(w http.ResponseWriter, r *http.Request) {
	var meta struct {
		RedirectURIs []string `json:"redirect_uris"`
	}
	_ = json.NewDecoder(r.Body).Decode(&meta)
	f.mu.Lock()
	f.n++
	id := fmt.Sprintf("client-%d", f.n)
	f.redirect = meta.RedirectURIs[0]
	f.mu.Unlock()
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{"client_id": id, "redirect_uris": meta.RedirectURIs,
		"token_endpoint_auth_method": "none"})
}

// authorize stands in for the human: it consents at once and redirects.
func (f *fakeAuth) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	back, _ := url.Parse(q.Get("redirect_uri"))
	answer := url.Values{"state": {q.Get("state")}}
	f.mu.Lock()
	f.methods = append(f.methods, q.Get("code_challenge_method"))
	f.scope = q.Get("scope")
	if f.deny {
		answer.Set("error", "access_denied")
	} else {
		f.n++
		code := fmt.Sprintf("code-%d", f.n)
		f.codes[code] = q.Get("code_challenge")
		f.secrets = append(f.secrets, code)
		answer.Set("code", code)
	}
	f.mu.Unlock()
	back.RawQuery = answer.Encode()
	http.Redirect(w, r, back.String(), http.StatusFound)
}

func (f *fakeAuth) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		challenge, ok := f.codes[r.Form.Get("code")]
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]any{"error": "invalid_grant"})
			return
		}
		delete(f.codes, r.Form.Get("code"))
	case "refresh_token":
		if !f.refresh[r.Form.Get("refresh_token")] {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]any{"error": "invalid_grant"})
			return
		}
	}
	f.n++
	access, refresh := fmt.Sprintf("access-%d", f.n), fmt.Sprintf("refresh-%d", f.n)
	f.access[access], f.refresh[refresh] = true, true
	f.secrets = append(f.secrets, access, refresh)
	writeJSON(w, map[string]any{"access_token": access, "token_type": "Bearer",
		"expires_in": f.expiresIn, "refresh_token": refresh})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// browse is the human: it follows every link a sign-in shows.
func browse(t *testing.T, bus *event.Bus) {
	t.Helper()
	waiting, stop := bus.Subscribe(event.Only(event.AuthorizationWaitingKind))
	t.Cleanup(stop)
	go func() {
		for rec := range waiting {
			if resp, err := http.Get(rec.Event.(event.AuthorizationWaiting).URL); err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
		}
	}()
}

// record keeps every fact published, to look for what should and
// should not have been.
func record(t *testing.T, bus *event.Bus) func() []event.Event {
	t.Helper()
	all, stop := bus.Subscribe(nil)
	t.Cleanup(stop)
	var mu sync.Mutex
	var got []event.Event
	go func() {
		for rec := range all {
			mu.Lock()
			got = append(got, rec.Event)
			mu.Unlock()
		}
	}()
	return func() []event.Event {
		bus.Settle(time.Second)
		mu.Lock()
		defer mu.Unlock()
		return append([]event.Event(nil), got...)
	}
}

// failedWith reports whether notion's sign-in was said to fail for reason.
func failedWith(events []event.Event, reason string) bool {
	for _, e := range events {
		if f, ok := e.(event.AuthorizationFailed); ok && f.Server == "notion" && f.Reason == reason {
			return true
		}
	}
	return false
}

func kinds(events []event.Event) []event.Kind {
	var out []event.Kind
	for _, e := range events {
		out = append(out, e.Kind())
	}
	return out
}

type signInRig struct {
	bus     *event.Bus
	reg     *tool.Registry
	in      *Invokers
	signins *SignIns
	tokens  Tokens
}

func rig(t *testing.T, tokens Tokens) signInRig {
	t.Helper()
	bus := event.New()
	t.Cleanup(bus.Close)
	in := NewInvokers()
	t.Cleanup(func() { _ = in.Close() })
	return signInRig{bus: bus, reg: tool.Standard(), in: in, tokens: tokens,
		signins: NewSignIns(bus, in, tokens, nil)}
}

func (r signInRig) connect(ctx context.Context, servers map[string]Config) []error {
	return ConnectAll(ctx, r.reg, r.in, servers, nil, WithSignIns(r.signins))
}

// fakeHandler signs in by swapping the token, once released.
type fakeHandler struct {
	calls   atomic.Int32
	release chan struct{}
	mu      sync.Mutex
	token   string
}

func (h *fakeHandler) TokenSource(context.Context) (oauth2.TokenSource, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: h.token}), nil
}

func (h *fakeHandler) Authorize(context.Context, *http.Request, *http.Response) error {
	h.calls.Add(1)
	<-h.release
	h.mu.Lock()
	h.token = "new"
	h.mu.Unlock()
	return nil
}
