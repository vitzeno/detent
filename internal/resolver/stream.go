package resolver

import (
	"github.com/vitzeno/detent/internal/agent"
	"github.com/vitzeno/detent/internal/shell"
	"github.com/vitzeno/detent/internal/ui"
)

// sinkAdapter bridges ui.StreamSink to agent.StreamSink.
type sinkAdapter struct {
	sink ui.StreamSink
}

func (a sinkAdapter) OnEvent(e shell.StreamEvent) {
	if a.sink != nil {
		a.sink.OnEvent(e.Line, e.Stderr)
	}
}

func toStreamSink(sink ui.StreamSink) agent.StreamSink {
	if sink == nil {
		return nil
	}
	return sinkAdapter{sink: sink}
}
