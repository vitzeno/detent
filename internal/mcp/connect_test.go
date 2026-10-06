package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/tool"
)

// A server that will not start must cost its own tools and nothing
// else: the session runs, the others connect.
func TestConnectAll_OneBadServerDoesNotStopTheRest(t *testing.T) {
	bin, err := fakeServer()
	require.NoError(t, err)

	reg := tool.Standard()
	in := NewInvokers()
	errs := ConnectAll(context.Background(), reg, in, map[string]Config{
		"good": {Command: bin}, "broken": {Command: "/nonexistent/server"},
	}, nil)
	t.Cleanup(func() { _ = in.Close() })

	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "broken")

	_, ok := reg.Lookup("good__echo")
	assert.True(t, ok, "the working server's tools are missing")
}

// Disabled is a choice and says nothing. Configured with neither a
// command nor a url is a mistake and says so.
func TestConnectAll_SkipsDisabledButReportsEmpty(t *testing.T) {
	bin, err := fakeServer()
	require.NoError(t, err)

	reg := tool.Standard()
	in := NewInvokers()
	errs := ConnectAll(context.Background(), reg, in, map[string]Config{
		"off": {Command: bin, Disabled: true}, "unnamed": {},
	}, nil)
	t.Cleanup(func() { _ = in.Close() })

	require.Len(t, errs, 1, "a misconfigured server went unreported")
	assert.Contains(t, errs[0].Error(), "unnamed")
	assert.Empty(t, in.Servers(), "neither one connected")
	_, ok := reg.Lookup("off__echo")
	assert.False(t, ok, "a disabled server registered tools")
}

// /mcp draws from this, so it has to hold every configured server,
// including the ones that never connected.
func TestConnectAll_StatusHoldsEveryServer(t *testing.T) {
	bin, err := fakeServer()
	require.NoError(t, err)

	in := NewInvokers()
	_ = ConnectAll(context.Background(), tool.Standard(), in, map[string]Config{
		"good": {Command: bin}, "broken": {Command: "/nonexistent/server"}, "off": {Command: bin, Disabled: true}, "unnamed": {},
	}, nil)
	t.Cleanup(func() { _ = in.Close() })

	byName := map[string]event.ServerSummary{}
	for _, s := range in.Status() {
		byName[s.Name] = s
	}
	require.Len(t, byName, 4, "a configured server is missing from the page")

	assert.Equal(t, 1, byName["good"].Tools)
	assert.Empty(t, byName["good"].Err)
	assert.True(t, byName["good"].Connected)
	assert.False(t, byName["broken"].Connected)
	assert.NotEmpty(t, byName["broken"].Err, "a failed server has nothing to show a human")
	assert.True(t, byName["off"].Disabled)
	assert.Zero(t, byName["off"].Tools)
	assert.NotEmpty(t, byName["unnamed"].Err, "a server with no command says nothing")
}

// Dialled concurrently but registered sorted, so the tool order a model
// sees does not shuffle per run and cost a prompt cache hit.
func TestConnectAll_RegistersInAStableOrder(t *testing.T) {
	bin, err := fakeServer()
	require.NoError(t, err)

	var runs [][]event.ToolName
	for range 3 {
		reg := tool.Standard()
		in := NewInvokers()
		_ = ConnectAll(context.Background(), reg, in, map[string]Config{
			"zulu": {Command: bin}, "alpha": {Command: bin}, "mike": {Command: bin},
		}, nil)
		runs = append(runs, reg.Names())
		_ = in.Close()
	}
	assert.Equal(t, runs[0], runs[1])
	assert.Equal(t, runs[1], runs[2])
	assert.Less(t, indexOf(runs[0], "alpha__echo"), indexOf(runs[0], "mike__echo"))
}

// A quick failure must not wait on a slow neighbour: report is called
// as each server settles, not once they all have.
func TestConnectAll_ReportsAFailureWithoutWaiting(t *testing.T) {
	slow := make(chan struct{})
	held := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-slow }))
	// Cleanup is LIFO, so the handler is released before Close waits
	// on it. The other way round deadlocks.
	t.Cleanup(held.Close)
	t.Cleanup(func() { close(slow) })

	reported := make(chan event.ServerSummary, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		in := NewInvokers()
		_ = ConnectAll(context.Background(), tool.Standard(), in, map[string]Config{
			"broken": {Command: "/nonexistent/server"},
			"slow":   {URL: held.URL},
		}, func(s event.ServerSummary) { reported <- s })
	}()

	select {
	case s := <-reported:
		assert.Equal(t, "broken", s.Name)
		assert.NotEmpty(t, s.Err)
	case <-done:
		t.Fatal("nothing was reported until every server had settled")
	case <-time.After(10 * time.Second):
		t.Fatal("the quick failure was never reported")
	}
}

// Every configured server is listed before any answers, so /mcp draws
// a slow one as still connecting rather than leaving it out.
func TestConnectAll_ListsAServerBeforeItAnswers(t *testing.T) {
	slow := make(chan struct{})
	held := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-slow }))
	// Cleanup is LIFO, so the handler is released before Close waits
	// on it. The other way round deadlocks.
	t.Cleanup(held.Close)
	t.Cleanup(func() { close(slow) })

	in := NewInvokers()
	settled := make(chan struct{}, 4)
	go func() {
		_ = ConnectAll(context.Background(), tool.Standard(), in, map[string]Config{
			"broken": {Command: "/nonexistent/server"},
			"slow":   {URL: held.URL},
		}, func(event.ServerSummary) { settled <- struct{}{} })
	}()

	select {
	case <-settled:
	case <-time.After(10 * time.Second):
		t.Fatal("nothing settled")
	}

	byName := map[string]event.ServerSummary{}
	for _, s := range in.Status() {
		byName[s.Name] = s
	}
	require.Len(t, byName, 2, "a server that has not answered is missing from the page")
	assert.False(t, byName["slow"].Connected, "a pending server is drawn as connected")
	assert.Empty(t, byName["slow"].Err, "a pending server is drawn as failed")
}

// The environment a server actually receives, not just what we built.
func TestConnectAll_TheServerSeesItsConfiguredEnv(t *testing.T) {
	bin, err := fakeServer()
	require.NoError(t, err)
	t.Setenv("DETENT_MARKER", "from-detent")

	reg := tool.Standard()
	in := NewInvokers()
	errs := ConnectAll(context.Background(), reg, in, map[string]Config{
		"demo": {Command: bin, Env: map[string]string{"DETENT_MARKER": "from-config"}},
	}, nil)
	require.Empty(t, errs)
	t.Cleanup(func() { _ = in.Close() })

	call, err := reg.Prepare("demo__echo", nil)
	require.NoError(t, err)
	res := in.Invoke(context.Background(), call)
	assert.Contains(t, res.Stdout, "env=from-config")
}

// Config names what a server gets. detent's own keys are not part of
// that, or every server would hold the model's credentials.
func TestEnviron_CarriesWhatIsNamedAndNotWhatIsNot(t *testing.T) {
	t.Setenv("DETENT_API_KEY", "should-not-travel")
	t.Setenv("PATH", "/usr/bin")

	got := environ(map[string]string{"GITHUB_TOKEN": "named"})

	assert.Contains(t, got, "GITHUB_TOKEN=named")
	assert.Contains(t, got, "PATH=/usr/bin", "a server needs PATH to run at all")
	for _, e := range got {
		assert.NotContains(t, e, "should-not-travel", "detent's own key reached a server")
	}
}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "detent-mcp")
	if err != nil {
		panic(err)
	}
	buildDir = dir
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func indexOf(names []event.ToolName, want event.ToolName) int {
	for i, n := range names {
		if n == want {
			return i
		}
	}
	return -1
}

// A config's secrets come from the environment, and an error goes on the bus,
// into the store and to the model, so none of them may ride along.
func TestRedact_TakesOutEverySecretAConfigHolds(t *testing.T) {
	c := Config{
		URL:     "https://u:hunter2pw@mcp.example.com/k3yInThePath1234/mcp?token=q5ecret99",
		Headers: map[string]string{"Authorization": "Bearer h3ader-secret"},
		Env:     map[string]string{"GITHUB_TOKEN": "ghp_envsecret123", "DEBUG": "1"},
	}
	err := fmt.Errorf("dial %s: refused; it said: auth ghp_envsecret123 failed for k3yInThePath1234, "+
		"header Bearer h3ader-secret, query q5ecret99, debug=1", c.URL)
	got := redact(c, err).Error()
	for _, secret := range []string{"hunter2pw", "k3yInThePath1234", "q5ecret99", "h3ader-secret", "ghp_envsecret123"} {
		assert.NotContains(t, got, secret)
	}
	assert.Contains(t, got, "debug=1", "a short value is not taken for a secret")
	assert.Contains(t, got, "mcp.example.com", "the host says which server it was")
	assert.ErrorIs(t, redact(c, err), err, "and it still unwraps")
}

func TestRedactURL_KeepsOnlySchemeAndHost(t *testing.T) {
	for raw, want := range map[string]string{
		"https://mcp.linear.app/mcp":                  "https://mcp.linear.app/…",
		"https://mcp.example.com/sk-abc123/sse?x=1#f": "https://mcp.example.com/…",
		"https://user:pw@mcp.example.com":             "https://mcp.example.com",
		"http://localhost:8080/":                      "http://localhost:8080",
		"http://localhost:8080/?token=abc":            "http://localhost:8080/…",
	} {
		assert.Equal(t, want, redactURL(raw), raw)
	}
}
