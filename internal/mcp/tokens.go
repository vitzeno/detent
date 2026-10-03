package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/oauth2"

	"github.com/vitzeno/detent/internal/private"
)

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// Tokens keeps each sign-in on disk, one 0600 file per name and URL, not
// the keychain: that costs cgo or a shell-out per platform.
type Tokens struct{ Dir string }

// TokensDir is beside the event store: secrets nobody authored belong
// in state, not in ~/.config.
func TokensDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "detent", "mcp")
}

// Saved is what a sign-in leaves: the client registered for it and the
// endpoints a refresh needs, since the SDK writes none of it down.
type Saved struct {
	// Resource is the server the token was issued for, checked on load.
	Resource     string `json:"resource"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`
	AuthURL      string `json:"auth_url"`
	TokenURL     string `json:"token_url"`
	// AuthStyle is how the token endpoint takes a client secret.
	AuthStyle oauth2.AuthStyle `json:"auth_style,omitempty"`
	Scopes    []string         `json:"scopes,omitempty"`
	Token     *oauth2.Token    `json:"token"`
}

// Load returns the sign-in for server at resource, or nil when it has none.
func (t Tokens) Load(server, resource string) (*Saved, error) {
	path, err := t.path(server, resource)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil //nolint:nilnil // no token yet means signing in, not failing
	case err != nil:
		return nil, fmt.Errorf("mcp: %s token: %w", server, err)
	}
	// Refused rather than read, as ssh refuses a key others can read.
	if !private.Is(info) {
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
	if s.Resource != canonical(resource) {
		return nil, nil //nolint:nilnil // a token for another URL is no token for this one
	}
	return &s, nil
}

// Save writes aside and renames: a refresh and a sign-in can race, and
// half a file must not read back as a token.
func (t Tokens) Save(server, resource string, s *Saved) error {
	path, err := t.path(server, resource)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(t.Dir, 0o700); err != nil {
		return fmt.Errorf("mcp: token dir: %w", err)
	}
	next := *s
	next.Resource = canonical(resource)
	raw, err := json.MarshalIndent(next, "", "  ") //nolint:gosec // the client secret is meant to be kept, in a 0600 file
	if err != nil {
		return fmt.Errorf("mcp: %s token: %w", server, err)
	}
	// CreateTemp makes the file 0600, so it is never briefly readable.
	tmp, err := os.CreateTemp(t.Dir, ".token-*")
	if err != nil {
		return fmt.Errorf("mcp: %s token: %w", server, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // gone already once renamed
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close() // the write error is the one worth reporting
		return fmt.Errorf("mcp: %s token: %w", server, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("mcp: %s token: %w", server, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("mcp: %s token: %w", server, err)
	}
	return nil
}

// Forget removes a sign-in, so the next dial asks for a new one.
func (t Tokens) Forget(server, resource string) error {
	path, err := t.path(server, resource)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("mcp: %s token: %w", server, err)
	}
	return t.dropLegacy(server)
}

// path names a file by the server's name made safe, then a hash of the
// name and URL: "../x" cannot leave the directory, and "a/b" is not "a_b".
func (t Tokens) path(server, resource string) (string, error) {
	if t.Dir == "" {
		return "", errors.New("mcp: no directory for tokens")
	}
	sum := sha256.Sum256([]byte(server + "\x00" + canonical(resource)))
	return filepath.Join(t.Dir, safeName(server)+"-"+hex.EncodeToString(sum[:8])+".json"), nil
}

// canonical is the server a token belongs to: scheme, host and path. The
// query is left out, since it may carry a key that rotates.
func canonical(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) + strings.TrimSuffix(u.EscapedPath(), "/")
}

// dropLegacy removes a token an older detent saved under the name alone.
// Nothing says which server it was issued by, so it is never read.
func (t Tokens) dropLegacy(server string) error {
	if t.Dir == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(server))
	path := filepath.Join(t.Dir, safeName(server)+"-"+hex.EncodeToString(sum[:4])+".json")
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("mcp: %s token: %w", server, err)
	}
	return nil
}

func safeName(server string) string { return unsafeChars.ReplaceAllString(server, "_") }
