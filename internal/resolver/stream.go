package resolver

import (
	"context"

	"github.com/vitzeno/detent/internal/host"
	"github.com/vitzeno/detent/ui"
)

// relayEvents translates host.StreamEvent onto events, dropping under
// backpressure. Returns agent's input channel and a done signal.
func relayEvents(ctx context.Context, events chan<- ui.StreamEvent) (chan host.StreamEvent, <-chan struct{}) {
	done := make(chan struct{})
	if events == nil {
		close(done)
		return nil, done
	}
	agentCh := make(chan host.StreamEvent, 64)
	go func() {
		defer close(done)
		for e := range agentCh {
			select {
			case events <- ui.StreamEvent{Line: e.Line, Stderr: e.Stderr}:
			case <-ctx.Done():
			default:
				select {
				case events <- ui.StreamEvent{Line: "…[live output dropped: UI lag]"}:
				default:
				}
			}
		}
	}()
	return agentCh, done
}
