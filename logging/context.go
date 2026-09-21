package logging

import (
	"context"
	"log/slog"
)

// Correlation travels in the context rather than through every
// signature. A goal or step set once upstream reaches every record
// written beneath it, which is what makes "show me step 7" a single
// filter across every component that touched it.

type marksKey struct{}

type marks struct {
	goal int
	step int
}

// WithGoal marks ctx as belonging to goal n.
func WithGoal(ctx context.Context, n int) context.Context {
	m, _ := ctx.Value(marksKey{}).(marks)
	m.goal = n
	return context.WithValue(ctx, marksKey{}, m)
}

// WithStep marks ctx as belonging to step n, the same number the UI
// shows against a row and /rollback takes.
func WithStep(ctx context.Context, n int) context.Context {
	m, _ := ctx.Value(marksKey{}).(marks)
	m.step = n
	return context.WithValue(ctx, marksKey{}, m)
}

// contextHandler copies whatever marks the context carries onto every
// record, so a call site never repeats them.
type contextHandler struct{ slog.Handler }

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if m, ok := ctx.Value(marksKey{}).(marks); ok {
		if m.goal > 0 {
			r.AddAttrs(slog.Int(KeyGoal, m.goal))
		}
		if m.step > 0 {
			r.AddAttrs(slog.Int(KeyStep, m.step))
		}
	}
	return h.Handler.Handle(ctx, r)
}

func (h contextHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return contextHandler{h.Handler.WithAttrs(as)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{h.Handler.WithGroup(name)}
}
