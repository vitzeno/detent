// Package logging configures detent's structured log and names the
// vocabulary it is written in.
//
// One stream per session, not one per component. The unit anyone
// actually investigates is a step, and a step crosses four or five
// components: splitting by component would make filtering easy and
// correlating impossible. A component field gives the split for free
// and keeps the join.
//
// It imports the standard library only, so ui and any other package
// outside internal/ can use it without breaking its own import rules.
package logging

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Options configures Setup.
type Options struct {
	// Dir holds one file per session. Empty means DefaultDir.
	Dir string
	// Session identifies this run and names its file.
	Session string
	// Level is "debug", "info", "warn" or "error".
	Level string
	// Bodies allows prompts, replies and command output into the log.
	// Off by default: the shape of a reply answers most questions, and
	// bodies carry secrets and bulk.
	Bodies bool
}

// Setup opens this session's log and makes it the default logger.
// The returned closer flushes it. A path that cannot be opened is not
// fatal: logging is never worth failing a session over, so it falls
// back to discarding and says so on stderr.
func Setup(o Options) (func() error, error) {
	dir := o.Dir
	if dir == "" {
		dir = DefaultDir()
	}
	if o.Session == "" {
		o.Session = "session"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return disable(fmt.Errorf("logging: %w", err))
	}
	path := filepath.Join(dir, o.Session+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return disable(fmt.Errorf("logging: %w", err))
	}
	bodies = o.Bodies
	slog.SetDefault(slog.New(contextHandler{slog.NewJSONHandler(f, &slog.HandlerOptions{
		Level: parseLevel(o.Level),
	})}).With(KeySession, o.Session))
	return f.Close, nil
}

// DefaultDir is where session logs live, beside the saved views.
func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "detent", "logs")
}

// For returns the logger a component writes with.
func For(component string) *slog.Logger {
	return slog.Default().With(KeyComponent, component)
}

// bodies is read by Body; a package-level flag rather than a
// parameter so no call site has to thread it.
var bodies bool

// Body returns text only when bodies are allowed, and a length
// otherwise. Use it for anything unbounded: prompts, model replies,
// command output.
func Body(text string) string {
	if bodies {
		return text
	}
	return fmt.Sprintf("(%d bytes, set log_bodies to record)", len(text))
}

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
