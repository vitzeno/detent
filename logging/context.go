package logging

import (
	"context"
	"log/slog"
)

// WithGoal marks ctx as belonging to goal n.
func WithGoal(ctx context.Context, n int) context.Context {
	m, _ := ctx.Value(marksKey{}).(marks)
	m.goal = n
	return context.WithValue(ctx, marksKey{}, m)
}

// WithStep marks ctx as step n, the number the UI shows on a row.
func WithStep(ctx context.Context, n int) context.Context {
	m, _ := ctx.Value(marksKey{}).(marks)
	m.step = n
	return context.WithValue(ctx, marksKey{}, m)
}

// Correlation travels in the context, not in signatures: a mark set
// once upstream reaches every record written beneath it.
type marksKey struct{}

type marks struct {
	goal int
	step int
}

// contextHandler copies the context's marks onto every record.
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
