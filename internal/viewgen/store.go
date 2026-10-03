package viewgen

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vitzeno/detent/viewspec"
)

// Store keeps composed specs on disk, one JSON file per key. Files on
// purpose: a spec you can read and fix by hand is the payoff for caching.
type Store struct{ Dir string }

// DefaultDir is where specs live when config says nothing.
func DefaultDir() string {
	dir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, ".local", "state", "detent", "views")
}

// Load returns the saved spec for key. A missing or unreadable file
// is a miss, never an error: the worst case is composing it again.
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
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return fmt.Errorf("viewgen: spec dir: %w", err)
	}
	raw, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return fmt.Errorf("viewgen: encoding spec: %w", err)
	}
	// Written aside and renamed: two rows can compose at once, and a
	// truncating write leaves half a file for the next Load to reject.
	tmp, err := os.CreateTemp(s.Dir, ".spec-*")
	if err != nil {
		return fmt.Errorf("viewgen: spec temp: %w", err)
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // gone already once renamed
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("viewgen: writing spec: %w", errors.Join(err, tmp.Close()))
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("viewgen: writing spec: %w", err)
	}
	return os.Rename(tmp.Name(), s.path(key))
}

// Path is where key's spec lives, so a caller can open it in an editor.
func (s *Store) Path(key string) string { return s.path(key) }

func (s *Store) path(key string) string { return filepath.Join(s.Dir, key+".json") }
