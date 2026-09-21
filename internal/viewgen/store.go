package viewgen

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vitzeno/detent/viewspec"
)

// Store keeps generated specs on disk, one JSON file per key. Files
// rather than a database on purpose: a spec you can read and fix by
// hand is the payoff for caching one at all, and /view edit opens it.
type Store struct{ Dir string }

// Load returns the cached spec for key. A missing or unreadable file
// is a miss, never an error: the worst case is generating again.
func (s *Store) Load(key string) (*viewspec.Spec, bool) {
	if s == nil || s.Dir == "" {
		return nil, false
	}
	raw, err := os.ReadFile(s.path(key))
	if err != nil {
		return nil, false
	}
	var spec viewspec.Spec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil, false
	}
	return &spec, true
}

// Save writes spec under key, creating the directory if needed.
func (s *Store) Save(key string, spec *viewspec.Spec) error {
	if s == nil || s.Dir == "" {
		return nil
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return fmt.Errorf("viewgen: cache dir: %w", err)
	}
	raw, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return fmt.Errorf("viewgen: encoding spec: %w", err)
	}
	return os.WriteFile(s.path(key), append(raw, '\n'), 0o644)
}

// Path is where key's spec lives, so a caller can open it in an editor.
func (s *Store) Path(key string) string { return s.path(key) }

// DefaultDir is where specs live when config says nothing.
func DefaultDir() string {
	dir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, ".local", "state", "detent", "views")
}

func (s *Store) path(key string) string { return filepath.Join(s.Dir, key+".json") }
