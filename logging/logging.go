// Package logging writes one JSONL stream per session and names the
// vocabulary it is written in. One stream, not one file per
// component: the thing anyone investigates is a step, and a step
// crosses several components.
//
// Standard library only, so ui may import it.
package logging

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Setup opens this session's log and makes it the default logger.
// A path it cannot open is reported but not fatal: it discards
// instead, since no session is worth failing over its log.
func Setup(session string, opts ...Option) (func() error, error) {
	var o settings
	for _, apply := range opts {
		apply(&o)
	}
	dir := o.dir
	if dir == "" {
		dir = DefaultDir()
	}
	if session == "" {
		session = "session"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return disable(fmt.Errorf("logging: %w", err))
	}
	path := filepath.Join(dir, session+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return disable(fmt.Errorf("logging: %w", err))
	}
	bodies = o.bodies
	slog.SetDefault(slog.New(slog.NewJSONHandler(f, &slog.HandlerOptions{
		Level: parseLevel(o.level),
	})).With(KeySession, session))
	return f.Close, nil
}

// For returns the logger a component writes with.
func For(component string) *slog.Logger {
	return slog.Default().With(KeyComponent, component)
}

// Body returns text only when bodies are allowed, a length
// otherwise. Use it for anything unbounded.
func Body(text string) string {
	if bodies {
		return text
	}
	return fmt.Sprintf("(%d bytes, set log_bodies to record)", len(text))
}

// DefaultDir is where session logs live, beside the saved views.
func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "detent", "logs")
}

// bodies is package-level so no call site has to thread it.
var bodies bool

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func disable(err error) (func() error, error) {
	slog.SetDefault(slog.New(slog.DiscardHandler))
	return func() error { return nil }, err
}
