package capabilities

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// LoadFile reads one domain file, YAML or JSON by extension (§3: YAML for
// hand-authored files where comments justify danger tiers; JSON for
// introspection-derived catalogs, e.g. a future MCP tools/list adapter).
func LoadFile(path string) (Domain, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Domain{}, fmt.Errorf("capabilities: reading %s: %w", path, err)
	}

	var d Domain
	switch filepath.Ext(path) {
	case ".yaml", ".yml":
		err = yaml.Unmarshal(data, &d)
	case ".json":
		err = json.Unmarshal(data, &d)
	default:
		return Domain{}, fmt.Errorf("capabilities: %s: unrecognized extension (want .yaml or .json)", path)
	}
	if err != nil {
		return Domain{}, fmt.Errorf("capabilities: parsing %s: %w", path, err)
	}

	if err := validateDomain(d, path); err != nil {
		return Domain{}, err
	}
	return d, nil
}

// validateDomain catches authoring mistakes at load time rather than at
// first use — the same "fail loudly" discipline §3 requires for missing
// handlers, applied to the schema itself.
func validateDomain(d Domain, path string) error {
	if d.Domain == "" {
		return fmt.Errorf("capabilities: %s: missing domain name", path)
	}
	if d.AvailableWhen == "" {
		return fmt.Errorf("capabilities: %s: domain %q missing available_when", path, d.Domain)
	}
	seen := make(map[string]bool, len(d.Actions))
	for _, a := range d.Actions {
		if a.Name == "" {
			return fmt.Errorf("capabilities: %s: domain %q has an action with no name", path, d.Domain)
		}
		if seen[a.Name] {
			return fmt.Errorf("capabilities: %s: domain %q: duplicate action %q", path, d.Domain, a.Name)
		}
		seen[a.Name] = true
		if a.Danger == "" {
			return fmt.Errorf("capabilities: %s: action %q missing danger", path, a.Name)
		}
		if a.Produces == "" {
			return fmt.Errorf("capabilities: %s: action %q missing produces", path, a.Name)
		}
		if a.Reducer == "" {
			return fmt.Errorf("capabilities: %s: action %q missing reducer", path, a.Name)
		}
		for _, arg := range a.Args {
			if arg.Name == "" || arg.Type == "" {
				return fmt.Errorf("capabilities: %s: action %q has an arg with no name or type", path, a.Name)
			}
		}
	}
	return nil
}
