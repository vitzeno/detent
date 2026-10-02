package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

// AuthorizeServer is a human asking for a fresh sign-in: the server
// named is dialled again, and a refusal reaches them as a notice.
func TestWatch_AuthorizeServerDialsTheServerAgain(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	asked := make(chan string, 1)
	stop := Watch(bus, NewInvokers(), func(name string) error { asked <- name; return nil }, nil)
	defer stop()
	bus.Publish(event.AuthorizeServer{Server: "notion"})
	select {
	case name := <-asked:
		assert.Equal(t, "notion", name)
	case <-time.After(2 * time.Second):
		t.Fatal("AuthorizeServer dialled nothing")
	}
}

func TestWatch_OpenAuthorizationOpensOnlyAWaitingLink(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	opened := make(chan string, 1)
	signins := NewSignIns(bus, nil, Tokens{}, func(link string) error { opened <- link; return nil })
	notices, unsub := bus.Subscribe(event.Only(event.NoticeKind))
	defer unsub()
	defer Watch(bus, NewInvokers(), nil, signins)()

	bus.Publish(event.OpenAuthorization{Server: "notion"})
	select {
	case rec := <-notices:
		assert.Contains(t, rec.Event.(event.Notice).Text, "no sign-in is waiting")
	case <-time.After(2 * time.Second):
		t.Fatal("opening nothing said nothing")
	}

	signins.live["notion"] = "https://as.example/authorize?state=s"
	bus.Publish(event.OpenAuthorization{Server: "notion"})
	select {
	case link := <-opened:
		assert.Equal(t, "https://as.example/authorize?state=s", link)
	case <-time.After(2 * time.Second):
		t.Fatal("the waiting link was never opened")
	}
}

// Asked again, a server signs in afresh: the old token is forgotten,
// not reused, and the tools stay offered under the new session.
func TestRedialer_SignsInAfresh(t *testing.T) {
	f := newFakeAuth(t)
	r := rig(t, Tokens{Dir: t.TempDir()})
	browse(t, r.bus)
	servers := map[string]Config{"notion": {URL: f.url() + "/mcp"}, "local": {Command: "true"}}
	_ = r.connect(context.Background(), servers)
	before, err := r.tokens.Load("notion")
	require.NoError(t, err)

	redial := Redialer(context.Background(), r.reg, r.in, servers, r.signins)
	require.NoError(t, redial("notion"))
	after, err := r.tokens.Load("notion")
	require.NoError(t, err)
	assert.NotEqual(t, before.Token.AccessToken, after.Token.AccessToken)
	_, ok := r.reg.Lookup("notion__ping")
	assert.True(t, ok)
	got, ok := r.in.tools["notion__ping"]
	require.True(t, ok)
	assert.Equal(t, "notion", got.server.Name)
	// Registered again beside its old self, it was notion__ping_2.
	for _, name := range r.reg.Names() {
		assert.NotContains(t, name, "notion__ping_", "a stale copy is still offered")
	}

	assert.ErrorContains(t, redial("nobody"), "no MCP server is called nobody")
	assert.ErrorContains(t, redial("local"), "launched, not reached")
}

// The sign-in address comes from the server's own metadata, so only a
// web address is ever shown or opened: never a file or an app's scheme.
func TestOpenable_AllowsOnlyTheWeb(t *testing.T) {
	for link, ok := range map[string]bool{
		"https://mcp.notion.com/authorize?x=1":    true,
		"http://127.0.0.1:8080/authorize":         true,
		"http://localhost/authorize":              true,
		"http://[::1]:9/authorize":                true,
		"http://example.com/authorize":            false,
		"file:///etc/passwd":                      false,
		"vscode://open?file=x":                    false,
		"javascript:alert(1)":                     false,
		"http://127.0.0.1.evil.example/authorize": false,
	} {
		err := openable(link)
		if ok {
			assert.NoError(t, err, link)
		} else {
			assert.Error(t, err, link)
		}
	}
}
