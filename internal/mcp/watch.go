package mcp

import "github.com/vitzeno/detent/event"

// Watch answers ListServers, since a front-end cannot ask this package
// directly. Wired beside logging.Watch and store.Watch.
func Watch(bus *event.Bus, in *Invokers) func() {
	intents, stop := bus.Subscribe(event.Only(event.ListServersKind))
	go func() {
		for range intents {
			bus.Publish(event.ServersListed{Servers: in.Status()})
		}
	}()
	return stop
}
