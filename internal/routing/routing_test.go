package routing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/capture"
	"github.com/vitzeno/detent/internal/engine"
)

func TestSelect(t *testing.T) {
	host, box := stub{"host"}, stub{"sandbox"}
	cases := []struct {
		name     string
		sel      Selector
		want     engine.Runner
		where    string
		wantsErr bool
	}{
		{name: "the sandbox by default", sel: Selector{Host: host, Sandbox: box}, want: box, where: "sandbox"},
		{name: "host only", sel: Selector{Host: host, Sandbox: box, HostOnly: true}, want: host, where: "host"},
		{name: "host only with no sandbox", sel: Selector{Host: host, HostOnly: true}, want: host, where: "host"},
		{name: "a missing sandbox never falls back to the host", sel: Selector{Host: host}, where: "sandbox", wantsErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Risk is ignored today, so a rule that reads it is a deliberate change here.
			for _, risk := range []event.Risk{event.UnknownRisk(), {Dangerous: true}} {
				got, where := c.sel.Select(risk)
				assert.Equal(t, c.where, where)
				if !c.wantsErr {
					assert.Equal(t, c.want, got)
					continue
				}
				_, err := got.Run(t.Context(), "ls", nil)
				require.ErrorIs(t, err, ErrNoSandbox)
			}
		})
	}
}

type stub struct{ name string }

func (s stub) Run(context.Context, string, chan<- capture.StreamEvent) (capture.Result, error) {
	return capture.Result{Stdout: s.name}, nil
}
