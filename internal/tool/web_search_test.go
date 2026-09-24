package tool

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

func TestWebSearch_LowersToOneReadableCurl(t *testing.T) {
	cmd, err := WebSearch{}.Lower(Args{"query": "containerd rootless snapshot"})
	require.NoError(t, err)

	assert.Equal(t, "curl -sS --max-time 30 -H 'x-no-cache: true' "+
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
