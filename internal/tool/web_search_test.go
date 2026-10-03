package tool

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/capture"
)

func TestWebSearch_LowersToOneReadableCurl(t *testing.T) {
	cmd, err := WebSearch{}.Lower(Args{"query": "containerd rootless snapshot"})
	require.NoError(t, err)

	assert.Equal(t, "curl -fsS --max-time 30 -H 'x-no-cache: true' "+
		"'https://r.jina.ai/https://lite.duckduckgo.com/lite/?q=containerd+rootless+snapshot'", cmd)
	assert.NotContains(t, cmd, "\n", "the row shows this on one line")
	assert.Less(t, len(cmd), 200, "a human has to be able to read what they approve")
}

// No key is in the command, so none is in the row, the log, the events
// table, or the container. That is the whole reason it is this engine.
func TestWebSearch_CarriesNoCredential(t *testing.T) {
	cmd, err := WebSearch{}.Lower(Args{"query": "anything"})
	require.NoError(t, err)
	for _, smell := range []string{"key", "token", "Authorization", "Bearer", "api"} {
		assert.NotContains(t, strings.ToLower(cmd), strings.ToLower(smell))
	}
}

// A query is model-authored text going into a shell command, so it is
// the one input here that has to be hostile-proof. Run it, don't assert.
func TestWebSearch_HostileQueriesCannotEscapeTheShell(t *testing.T) {
	dir := t.TempDir()
	for _, q := range []string{
		"'; touch pwned #",
		"$(touch pwned)",
		"`touch pwned`",
		"a' && touch pwned && echo '",
		"; rm -rf / ;",
		"héllo 世界 🎉 & | > <",
	} {
		t.Run(q, func(t *testing.T) {
			cmd, err := WebSearch{}.Lower(Args{"query": q})
			require.NoError(t, err)

			// true, not curl: the shell has finished expanding by the
			// time it runs anything, so the network adds nothing.
			c := exec.Command("sh", "-c", strings.Replace(cmd, "curl", "true", 1))
			c.Dir = dir
			_, _ = c.CombinedOutput()

			_, err = os.Stat(filepath.Join(dir, "pwned"))
			assert.True(t, os.IsNotExist(err), "the query escaped its quoting")

			// The file check cannot tell the two defences apart:
			// encoding is why nothing reaches the shell at all.
			for _, meta := range []string{"$(", "`", ";", "&&", "|"} {
				assert.NotContains(t, cmd, meta, "a shell metacharacter survived encoding")
			}
		})
	}
}

func TestWebSearch_RefusesAnEmptyQuery(t *testing.T) {
	for _, q := range []string{"", "   ", "\n\t"} {
		_, err := WebSearch{}.Lower(Args{"query": q})
		assert.Error(t, err, "an empty search is a tool result, not a request")
	}
}

// Read-only, so it runs on the parallel path and never asks: a search
// changes nothing, and confirming one is the wrong kind of friction.
func TestWebSearch_IsReadOnly(t *testing.T) {
	assert.Equal(t, event.MutRead, WebSearch{}.Describe().Mutability)
}

// Deliberately no declared shape: glamour prints every link's
// destination, and DuckDuckGo's redirects double the output.
func TestWebSearch_DoesNotClaimAShape(t *testing.T) {
	assert.Empty(t, WebSearch{}.Describe().Renders)
}

func TestWebSearch_NativeAsksWhatTheCommandWould(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		_, _ = w.Write([]byte("1. A result\r\nhttps://example.com\n"))
	}))
	defer srv.Close()
	searchAt(t, srv)

	res := WebSearch{}.Run(t.Context(), Args{"query": "containerd rootless"})
	require.Equal(t, 0, res.ExitCode, res.Stderr)
	assert.Equal(t, "1. A result\nhttps://example.com\n", res.Stdout)
	require.NotNil(t, got)
	assert.Equal(t, "/"+searchBase+"containerd+rootless", got.URL.RequestURI())
	assert.Equal(t, "true", got.Header.Get("x-no-cache"))
}

// curl -f fails on an error status rather than printing the error page.
func TestWebSearch_NativeFailsOnAnErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	searchAt(t, srv)

	res := WebSearch{}.Run(t.Context(), Args{"query": "x"})
	assert.Equal(t, 22, res.ExitCode)
	assert.Empty(t, res.Stdout)
	assert.Contains(t, res.Stderr, "429")
}

func TestWebSearch_NativeCapsWhatItKeeps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("a result line\n", 2000)))
	}))
	defer srv.Close()
	searchAt(t, srv)

	res := WebSearch{}.Run(t.Context(), Args{"query": "x"})
	assert.True(t, res.Truncated)
	assert.LessOrEqual(t, len(res.Stdout), capture.MaxOutputBytes)
}

func TestWebSearch_NativeRefusesWhatLowerRefuses(t *testing.T) {
	for _, q := range []string{"  ", strings.Repeat("q", maxQueryBytes+1)} {
		assert.NotZero(t, WebSearch{}.Run(t.Context(), Args{"query": q}).ExitCode)
	}
}

// searchAt points web_search at srv for one test.
func searchAt(t *testing.T, srv *httptest.Server) {
	t.Helper()
	was := readerPrefix
	readerPrefix = srv.URL + "/"
	t.Cleanup(func() { readerPrefix = was })
}
