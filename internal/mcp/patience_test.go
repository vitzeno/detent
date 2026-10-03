package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A server gets a bounded time to answer, but a human at the browser
// does not use it up.
func TestPatience_StopsWhileAHumanSignsIn(t *testing.T) {
	ctx, cancel := withPatience(context.Background(), 50*time.Millisecond)
	defer cancel()
	p := patienceFrom(ctx)
	require.NotNil(t, p)

	p.pause()
	time.Sleep(150 * time.Millisecond)
	require.NoError(t, ctx.Err(), "a paused deadline ran out")

	p.resume()
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the deadline never came back")
	}
	assert.ErrorContains(t, context.Cause(ctx), "no answer within 50ms")
}

// A server that never answers is given up on, and says how long it had.
func TestDial_GivesUpOnAServerThatNeverAnswers(t *testing.T) {
	slow := make(chan struct{})
	held := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-slow }))
	t.Cleanup(held.Close)
	t.Cleanup(func() { close(slow) })
	defer func(d time.Duration) { connectTimeout = d }(connectTimeout)
	connectTimeout = 100 * time.Millisecond

	got := dial(context.Background(), "mute", Config{URL: held.URL},
		NewSignIns(nil, nil, Tokens{Dir: t.TempDir()}, nil))
	require.Error(t, got.err)
	assert.Contains(t, got.err.Error(), "no answer within 100ms")
}
