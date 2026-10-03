package mcp

import (
	"context"
	"fmt"
	"sync"

	"github.com/vitzeno/detent/event"
)

// Watch answers ListServers, and the two intents a sign-in takes:
// AuthorizeServer dials a server again, OpenAuthorization opens its link.
// The stop waits for sign-ins in flight, which give up once ctx ends.
func Watch(ctx context.Context, bus *event.Bus, in *Invokers, redial func(context.Context, string) error,
	signins *SignIns) func() {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	stop := bus.Handle(event.Only(event.ListServersKind,
		event.AuthorizeServerKind, event.OpenAuthKind), func(rec event.Record) {
		switch v := rec.Event.(type) {
		case event.ListServers:
			bus.Publish(event.ServersListed{Servers: in.Status()})
		case event.AuthorizeServer:
			if redial == nil {
				notice(bus, fmt.Errorf("signing in to %s is not available here", v.Server))
				return
			}
			// A sign-in waits on a human for up to ten minutes, off this loop.
			wg.Go(func() { notice(bus, redial(ctx, v.Server)) })
		case event.OpenAuthorization:
			if signins == nil {
				notice(bus, fmt.Errorf("no sign-in is waiting for %s", v.Server))
				return
			}
			notice(bus, signins.Open(v.Server))
		}
	})
	return func() {
		stop()
		cancel()
		wg.Wait()
	}
}

func notice(bus *event.Bus, err error) {
	if err != nil {
		bus.Publish(event.Notice{Level: "error", Text: err.Error()})
	}
}
