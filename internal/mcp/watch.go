package mcp

import (
	"fmt"

	"github.com/vitzeno/detent/event"
)

// Watch answers ListServers, and the two intents a sign-in takes:
// AuthorizeServer dials a server again, OpenAuthorization opens its link.
func Watch(bus *event.Bus, in *Invokers, redial func(string) error, signins *SignIns) func() {
	intents, stop := bus.Subscribe(event.Only(event.ListServersKind,
		event.AuthorizeServerKind, event.OpenAuthKind))
	go func() {
		for rec := range intents {
			switch v := rec.Event.(type) {
			case event.ListServers:
				bus.Publish(event.ServersListed{Servers: in.Status()})
			case event.AuthorizeServer:
				if redial == nil {
					notice(bus, fmt.Errorf("signing in to %s is not available here", v.Server))
					continue
				}
				// A sign-in waits on a human for up to ten minutes, off this loop.
				go func() { notice(bus, redial(v.Server)) }()
			case event.OpenAuthorization:
				if signins == nil {
					notice(bus, fmt.Errorf("no sign-in is waiting for %s", v.Server))
					continue
				}
				notice(bus, signins.Open(v.Server))
			}
		}
	}()
	return stop
}

func notice(bus *event.Bus, err error) {
	if err != nil {
		bus.Publish(event.Notice{Level: "error", Text: err.Error()})
	}
}
