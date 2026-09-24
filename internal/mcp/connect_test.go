package mcp

import (
	"context"
	"os"
	"testing"

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
	in, errs := ConnectAll(context.Background(), reg, map[string]Config{
		"good":   {Command: bin},
		"broken": {Command: "/nonexistent/server"},
	})
	t.Cleanup(func() { _ = in.Close() })

	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "broken")

	_, ok := reg.Lookup("good__echo")
	assert.True(t, ok, "the working server's tools are missing")
}

// Disabled is a choice and says nothing; configured with neither a
// command nor a url is a mistake and says so.
func TestConnectAll_SkipsDisabledButReportsEmpty(t *testing.T) {
	bin, err := fakeServer()
	require.NoError(t, err)

	reg := tool.Standard()
	in, errs := ConnectAll(context.Background(), reg, map[string]Config{
		"off":     {Command: bin, Disabled: true},
		"unnamed": {},
	})
	t.Cleanup(func() { _ = in.Close() })

	require.Len(t, errs, 1, "a misconfigured server went unreported")
	assert.Contains(t, errs[0].Error(), "unnamed")
	assert.Empty(t, in.Servers(), "neither one connected")
	_, ok := reg.Lookup("off__echo")
	assert.False(t, ok, "a disabled server registered tools")
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

// The environment a server actually receives, not just what we built.
func TestConnectAll_TheServerSeesItsConfiguredEnv(t *testing.T) {
	bin, err := fakeServer()
	require.NoError(t, err)
	t.Setenv("DETENT_MARKER", "from-detent")

	reg := tool.Standard()
	in, errs := ConnectAll(context.Background(), reg, map[string]Config{
		"demo": {Command: bin, Env: map[string]string{"DETENT_MARKER": "from-config"}},
	})
	require.Empty(t, errs)
	t.Cleanup(func() { _ = in.Close() })

	call, err := reg.Prepare("demo__echo", nil)
	require.NoError(t, err)
	res := in.Invoke(context.Background(), call)
	assert.Contains(t, res.Stdout, "env=from-config")
}

// Sorted, so the tool order a model sees does not shuffle per run and
// cost a prompt cache hit.
func TestConnectAll_RegistersInAStableOrder(t *testing.T) {
	bin, err := fakeServer()
	require.NoError(t, err)

	var runs [][]string
	for range 3 {
		reg := tool.Standard()
		in, _ := ConnectAll(context.Background(), reg, map[string]Config{
			"zulu": {Command: bin}, "alpha": {Command: bin}, "mike": {Command: bin},
		})
		runs = append(runs, reg.Names())
		_ = in.Close()
	}
	assert.Equal(t, runs[0], runs[1])
	assert.Equal(t, runs[1], runs[2])
	assert.Less(t, indexOf(runs[0], "alpha__echo"), indexOf(runs[0], "mike__echo"))
}

func indexOf(names []string, want string) int {
	for i, n := range names {
		if n == want {
			return i
		}
	}
	return -1
}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "detent-mcp")
	if err != nil {
		panic(err)
	}
	buildDir = dir
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// /mcp draws from this, so it has to hold every configured server,
// including the ones that never connected.
func TestConnectAll_StatusHoldsEveryServer(t *testing.T) {
	bin, err := fakeServer()
	require.NoError(t, err)

	in, _ := ConnectAll(context.Background(), tool.Standard(), map[string]Config{
		"good":    {Command: bin},
		"broken":  {Command: "/nonexistent/server"},
		"off":     {Command: bin, Disabled: true},
		"unnamed": {},
	})
	t.Cleanup(func() { _ = in.Close() })

	byName := map[string]event.ServerSummary{}
	for _, s := range in.Status() {
		byName[s.Name] = s
	}
	require.Len(t, byName, 4, "a configured server is missing from the page")

	assert.Equal(t, 1, byName["good"].Tools)
	assert.Empty(t, byName["good"].Err)
	assert.NotEmpty(t, byName["broken"].Err, "a failed server has nothing to show a human")
	assert.True(t, byName["off"].Disabled)
	assert.Zero(t, byName["off"].Tools)
	assert.NotEmpty(t, byName["unnamed"].Err, "a server with no command says nothing")
}
