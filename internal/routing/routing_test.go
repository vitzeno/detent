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
		{name: "the sandbox", sel: Sandbox(box), want: box, where: "sandbox"},
		{name: "this machine", sel: Host(host), want: host, where: "host"},
		{name: "a sandbox nobody wired never falls back to the host", sel: Sandbox(nil), where: "sandbox", wantsErr: true},
		{name: "the zero Selector is that missing sandbox", sel: Selector{}, where: "sandbox", wantsErr: true},
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
