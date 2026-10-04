// Package logging writes one JSONL stream per session, not a file per
// component, since a step crosses several. Standard library plus event,
// so event itself can never log.
package logging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"unicode/utf8"
)

const snippetBytes = 200

// bodies is package-level so no call site has to thread it.
var bodies atomic.Bool

// Setup opens this session's log as the default logger. A path it cannot
// open is reported, not fatal: it discards instead.
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
	if session != filepath.Base(session) {
		return disable(fmt.Errorf("logging: %q is not a session name", session))
	}
	// Owner only: with bodies on the file holds every prompt and output.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return disable(fmt.Errorf("logging: %w", err))
	}
	path := filepath.Join(dir, session+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return disable(fmt.Errorf("logging: %w", err))
	}
	// OpenFile keeps an existing file's mode, which may predate this.
	if err := f.Chmod(0o600); err != nil {
		return disable(fmt.Errorf("logging: %w", errors.Join(err, f.Close())))
	}
	lvl, lvlErr := parseLevel(o.level)
	bodies.Store(o.bodies)
	current.Store(session)
	slog.SetDefault(slog.New(sessionHandler{slog.NewJSONHandler(f, &slog.HandlerOptions{
		Level: lvl,
	})}))
	closer := func() error {
		// A late write after this goes nowhere rather than to a closed file.
		slog.SetDefault(slog.New(slog.DiscardHandler))
		return f.Close()
	}
	return closer, lvlErr
}

// For returns the logger a component writes with.
func For(component string) *slog.Logger {
	return slog.Default().With(KeyComponent, component)
}

// Body returns text only when bodies are allowed, a length
// otherwise. Use it for anything unbounded.
func Body(text string) string {
	if bodies.Load() {
		return text
	}
	return fmt.Sprintf("(%d bytes, set log_bodies to record)", len(text))
}

// Snippet is Body for text worth keeping some of, such as an error that
// may quote an endpoint's reply: its first snippetBytes when withheld.
func Snippet(text string) string {
	if bodies.Load() || len(text) <= snippetBytes {
		return text
	}
	cut := snippetBytes
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return fmt.Sprintf("%s... (%d bytes, set log_bodies to record)", text[:cut], len(text))
}

// DefaultDir is where session logs live, beside the saved views.
func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "detent", "logs")
}

func parseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "", "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return slog.LevelInfo, fmt.Errorf("logging: unknown level %q, logging at info", s)
}

func disable(err error) (func() error, error) {
	slog.SetDefault(slog.New(slog.DiscardHandler))
	return func() error { return nil }, err
}

// current is the session records are filed under. /new moves it, so it is
// read as each record is written rather than fixed into a logger.
var current atomic.Value

// sessionHandler adds the current session to every record.
type sessionHandler struct{ slog.Handler }

func (h sessionHandler) Handle(ctx context.Context, r slog.Record) error {
	if s, ok := current.Load().(string); ok {
		r.AddAttrs(slog.String(KeySession, s))
	}
	return h.Handler.Handle(ctx, r)
}

func (h sessionHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return sessionHandler{h.Handler.WithAttrs(attrs)}
}

func (h sessionHandler) WithGroup(name string) slog.Handler {
	return sessionHandler{h.Handler.WithGroup(name)}
}
