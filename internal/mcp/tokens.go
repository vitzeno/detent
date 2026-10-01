package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"golang.org/x/oauth2"
)

// Tokens keeps each server's sign-in on disk, one 0600 file a server.
// Not the keychain: that costs cgo or a shell-out per platform.
type Tokens struct{ Dir string }

// Saved is what a sign-in leaves: the client registered for it and the
// endpoints a refresh needs, since the SDK writes none of it down.
type Saved struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`
	AuthURL      string `json:"auth_url"`
	TokenURL     string `json:"token_url"`
	// AuthStyle is how the token endpoint takes a client secret.
	AuthStyle oauth2.AuthStyle `json:"auth_style,omitempty"`
	Scopes    []string         `json:"scopes,omitempty"`
	Token     *oauth2.Token    `json:"token"`
}

// TokensDir is beside the event store: secrets nobody authored belong
// in state, not in ~/.config.
func TokensDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "detent", "mcp")
}

// Load returns the server's sign-in, or nil when it has none.
func (t Tokens) Load(server string) (*Saved, error) {
	path, err := t.path(server)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("mcp: %s token: %w", server, err)
	}
	// Refused rather than read, as ssh refuses a key others can read.
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("mcp: %s is readable by others; chmod 600 it", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("mcp: %s token: %w", server, err)
	}
	var s Saved
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("mcp: %s token: %w", server, err)
	}
	return &s, nil
}

// Save writes aside and renames: a refresh and a sign-in can race, and
// half a file must not read back as a token.
func (t Tokens) Save(server string, s *Saved) error {
	path, err := t.path(server)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(t.Dir, 0o700); err != nil {
		return fmt.Errorf("mcp: token dir: %w", err)
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("mcp: %s token: %w", server, err)
	}
	// CreateTemp makes the file 0600, so it is never briefly readable.
	tmp, err := os.CreateTemp(t.Dir, ".token-*")
	if err != nil {
		return fmt.Errorf("mcp: %s token: %w", server, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("mcp: %s token: %w", server, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("mcp: %s token: %w", server, err)
	}
	return os.Rename(tmp.Name(), path)
}

// Forget removes a sign-in, so the next dial asks for a new one.
func (t Tokens) Forget(server string) error {
	path, err := t.path(server)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("mcp: %s token: %w", server, err)
	}
	return nil
}

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// path names a server's file by its name made safe, then a hash of the
// real one: "../x" cannot leave the directory, and "a/b" is not "a_b".
func (t Tokens) path(server string) (string, error) {
	if t.Dir == "" {
		return "", errors.New("mcp: no directory for tokens")
	}
	sum := sha256.Sum256([]byte(server))
	name := unsafeChars.ReplaceAllString(server, "_") + "-" + hex.EncodeToString(sum[:4])
	return filepath.Join(t.Dir, name+".json"), nil
}
