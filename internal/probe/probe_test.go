package probe

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/host"
)

type fakeJudge struct {
	answers classify.Answers
	err     error
}

func (f fakeJudge) Ask(_ context.Context, _ classify.State, qs classify.Questions) (classify.Answers, classify.Usage, error) {
	if f.err != nil {
		return nil, classify.Usage{}, f.err
	}
	out := classify.Answers{}
	for id := range qs {
		if a, ok := f.answers[id]; ok {
			out[id] = a
		}
	}
	return out, classify.Usage{}, nil
}

func TestSelect(t *testing.T) {
	tests := []struct {
		name  string
		judge Judge
		want  []string // probe names, in Menu order
	}{
		{
			name:  "nil judge falls back to the cheapest probe",
			judge: nil,
			want:  []string{"dir_listing"},
		},
		{
			name:  "judge error falls back to the cheapest probe",
			judge: fakeJudge{err: errors.New("boom")},
			want:  []string{"dir_listing"},
		},
		{
			name: "judge gates each probe independently on its own noul",
			judge: fakeJudge{answers: classify.Answers{
				"dir_listing":       {Noul: 0.9},
				"git_context":       {Noul: 0.4},
				"process_snapshot":  {Noul: 0.5},
				"network_listeners": {Noul: 0.1},
				// disk_usage: no answer at all — must not be selected
			}},
			want: []string{"dir_listing", "process_snapshot"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Select(context.Background(), tt.judge, "some goal")
			var names []string
			for _, p := range got {
				names = append(names, p.Name)
			}
			assert.Equal(t, tt.want, names)
		})
	}
}

type hostRunner struct{}

func (hostRunner) Run(ctx context.Context, command string) (host.Result, error) {
	return host.NewShell().Run(ctx, command, nil)
}

type fakeRunner func(ctx context.Context, command string) (host.Result, error)

func (f fakeRunner) Run(ctx context.Context, command string) (host.Result, error) {
	return f(ctx, command)
}

func TestRun(t *testing.T) {
	t.Run("empty probes returns empty", func(t *testing.T) {
		assert.Empty(t, New(hostRunner{}).Run(context.Background(), nil))
	})

	t.Run("combines output from multiple probes in order", func(t *testing.T) {
		probes := []Probe{
			{Name: "a", Command: "echo one"},
			{Name: "b", Command: "echo two"},
		}
		out := New(hostRunner{}).Run(context.Background(), probes)
		assert.Contains(t, out, "$ echo one")
		assert.Contains(t, out, "one")
		assert.Contains(t, out, "$ echo two")
		assert.Contains(t, out, "two")
	})

	t.Run("failed command is noted, not fatal", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		out := New(hostRunner{}).Run(ctx, []Probe{{Name: "a", Command: "echo hi"}})
		assert.Contains(t, out, "failed")
	})

	t.Run("runs through the injected runner, not host.Run directly", func(t *testing.T) {
		var got string
		fake := fakeRunner(func(_ context.Context, command string) (host.Result, error) {
			got = command
			return host.Result{Stdout: "stubbed\n"}, nil
		})
		out := New(fake).Run(context.Background(), []Probe{{Name: "a", Command: "should not really run"}})
		assert.Equal(t, "should not really run", got)
		assert.Contains(t, out, "stubbed")
	})
}

func TestMenu_NamesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range Menu {
		assert.False(t, seen[p.Name], "duplicate probe name %q", p.Name)
		seen[p.Name] = true
		assert.NotEmpty(t, p.Command)
		assert.NotEmpty(t, p.Instructions)
	}
}
