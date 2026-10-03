// Package trust asks before detent acts on configuration a repository
// carries. A cloned repo can redirect the API key, drop the sandbox and
// launch MCP servers, so its files are read only once the human says so.
package trust

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/vitzeno/detent/internal/private"
)

// Files are what detent reads from the working directory, in hash order.
var Files = []string{".detent.yaml", ".detent.yml", ".env", ".mcp.json"}

// Options are each input to Decide, injectable so a test needs no terminal.
type Options struct {
	// Dir is the working directory the files are read from.
	Dir string
	// State holds the record of approvals, DefaultDir outside tests.
	State string
	// Flag is -trust: trusted for this run and never recorded.
	Flag bool
	// Ask puts the question and reads the answer, nil when nobody can answer.
	Ask func() bool
	Out io.Writer
}

// Decision says whether the repo-local files may be read.
type Decision struct {
	Trusted bool
	// Present are the repo-local files that exist, whether or not trusted.
	Present []string
	// Files are the bytes that were hashed, by name, and nil unless trusted.
	// Loading these rather than the disk is what makes an approval mean anything.
	Files map[string][]byte
}

// DefaultDir is beside the event store and the MCP tokens.
func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "detent")
}

// Decide answers from the record when it can and asks when it must.
// A directory with none of Files needs no trust and asks nothing.
func Decide(o Options) (Decision, error) {
	dir, err := filepath.Abs(o.Dir)
	if err != nil {
		return Decision{}, fmt.Errorf("trust: %w", err)
	}
	present, files, sum, err := digest(dir)
	if err != nil {
		return Decision{}, err
	}
	d := Decision{Present: present}
	if len(present) == 0 || o.Flag {
		d.Trusted, d.Files = true, files
		return d, nil
	}
	// No state dir means no record, never a relative path the repo could ship.
	path := ""
	approved := map[string]string{}
	if o.State != "" {
		path = filepath.Join(o.State, "trust.json")
		if approved, err = load(path); err != nil {
			return Decision{}, err
		}
	}
	prior, seen := approved[dir]
	if seen && prior == sum {
		d.Trusted, d.Files = true, files
		return d, nil
	}
	names := strings.Join(present, ", ")
	if o.Ask == nil {
		fmt.Fprintf(o.Out, "detent: ignoring %s in %s, not trusted. Run detent here to review them, or pass -trust\n", names, dir)
		return d, nil
	}
	fmt.Fprint(o.Out, Summary(dir, present, files, seen))
	fmt.Fprint(o.Out, "Trust this directory? [y/N] ")
	if !o.Ask() {
		fmt.Fprintf(o.Out, "detent: ignoring %s, using your own configuration only\n", names)
		return d, nil
	}
	approved[dir] = sum
	if path != "" {
		if err := save(o.State, path, approved); err != nil {
			return Decision{}, err
		}
	}
	d.Trusted, d.Files = true, files
	return d, nil
}

// Reader answers from one line of in: y or yes, any case.
func Reader(in io.Reader) func() bool {
	return func() bool {
		// A byte at a time, so nothing past the line is taken from whoever reads in next.
		var line []byte
		b := make([]byte, 1)
		for {
			n, err := in.Read(b)
			if n == 1 {
				if b[0] == '\n' {
					break
				}
				line = append(line, b[0])
			}
			if err != nil {
				break
			}
		}
		switch strings.ToLower(strings.TrimSpace(string(line))) {
		case "y", "yes":
			return true
		}
		return false
	}
}

// digest names the files present, keeps their bytes and hashes them with their names.
func digest(dir string) ([]string, map[string][]byte, string, error) {
	var present []string
	files := map[string][]byte{}
	h := sha256.New()
	for _, name := range Files {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return nil, nil, "", fmt.Errorf("trust: %w", err)
		}
		present = append(present, name)
		files[name] = raw
		fmt.Fprintf(h, "%s\x00%d\x00", name, len(raw))
		h.Write(raw)
	}
	return present, files, hex.EncodeToString(h.Sum(nil)), nil
}

// record is trust.json: each approved directory's digest.
type record struct {
	Dirs map[string]string `json:"dirs"`
}

func load(path string) (map[string]string, error) {
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return map[string]string{}, nil
	case err != nil:
		return nil, fmt.Errorf("trust: %w", err)
	}
	// Anyone who can write it can approve a directory, so it is refused as ssh refuses a key.
	if !private.Is(info) {
		return nil, fmt.Errorf("trust: %s is open to others; chmod 600 it", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("trust: %w", err)
	}
	var r record
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("trust: %s: %w", path, err)
	}
	if r.Dirs == nil {
		r.Dirs = map[string]string{}
	}
	return r.Dirs, nil
}

// save writes aside and renames, so a crash never leaves half a record.
func save(dir, path string, dirs map[string]string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("trust: %w", err)
	}
	raw, err := json.MarshalIndent(record{Dirs: dirs}, "", "  ")
	if err != nil {
		return fmt.Errorf("trust: %w", err)
	}
	// CreateTemp makes the file 0600.
	tmp, err := os.CreateTemp(dir, ".trust-*")
	if err != nil {
		return fmt.Errorf("trust: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("trust: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("trust: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("trust: %w", err)
	}
	return nil
}
