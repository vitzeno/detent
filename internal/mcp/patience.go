package mcp

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// connectTimeout is how long a server has to start and list its tools,
// not counting a human signing in.
var connectTimeout = 30 * time.Second

// patience is a deadline that stops while a human is at the browser,
// so a sign-in is not cut off by how long a server may take to answer.
type patience struct {
	mu     sync.Mutex
	d      time.Duration
	timer  *time.Timer
	paused int
}

type patienceKey struct{}

// withPatience cancels ctx once d has passed outside any pause.
func withPatience(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(parent)
	p := &patience{d: d}
	p.timer = time.AfterFunc(d, func() { cancel(fmt.Errorf("no answer within %s", d)) })
	return context.WithValue(ctx, patienceKey{}, p), func() {
		p.timer.Stop()
		cancel(context.Canceled)
	}
}

func patienceFrom(ctx context.Context) *patience {
	p, _ := ctx.Value(patienceKey{}).(*patience)
	return p
}

func (p *patience) pause() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.paused++; p.paused == 1 {
		p.timer.Stop()
	}
}

// resume starts the clock again from the whole budget.
func (p *patience) resume() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.paused--; p.paused == 0 {
		p.timer.Reset(p.d)
	}
}
