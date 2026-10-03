package config

import (
	"cmp"
	"os"
	"strconv"
	"strings"
)

// Resolve layers built-ins, file, environment, then flags, each setting
// only the fields it has. steps is -1 when not passed, so -steps 0 applies.
func Resolve(file, flags Config, steps int) Config {
	out := Default()
	merge(&out, file)
	merge(&out, envConfig(os.Getenv))
	merge(&out, flags)
	if steps >= 0 {
		out.Steps = steps
	}
	return out
}

// merge overwrites the fields o sets in c, leaving the rest alone.
func merge(c *Config, o Config) {
	if o.BaseURL != "" {
		c.BaseURL = o.BaseURL
	}
	if o.Model != "" {
		c.Model = o.Model
	}
	if o.APIKey != "" {
		c.APIKey = o.APIKey
	}
	// len, not nil: an empty map sets nothing, and the example config
	// spells headers out as {}.
	if len(o.Headers) > 0 {
		c.Headers = o.Headers
	}
	if o.Steps != 0 {
		c.Steps = o.Steps
	}
	if o.ContextTokens != 0 {
		c.ContextTokens = o.ContextTokens
	}
	if o.CommandTimeout != "" {
		c.CommandTimeout = o.CommandTimeout
	}
	if o.FinishCheck != nil {
		c.FinishCheck = o.FinishCheck
	}
	if o.JevAPIKey != "" {
		c.JevAPIKey = o.JevAPIKey
	}
	if o.JevModel != "" {
		c.JevModel = o.JevModel
	}
	if o.JevEndpoint != "" {
		c.JevEndpoint = o.JevEndpoint
	}
	if o.RiskThreshold != 0 {
		c.RiskThreshold = o.RiskThreshold
	}
	if o.Theme != "" {
		c.Theme = o.Theme
	}
	if o.Views != "" {
		c.Views = o.Views
	}
	if o.LogLevel != "" {
		c.LogLevel = o.LogLevel
	}
	if o.LogDir != "" {
		c.LogDir = o.LogDir
	}
	if o.LogBodies != nil {
		c.LogBodies = o.LogBodies
	}
	if o.SandboxMode != "" {
		c.SandboxMode = o.SandboxMode
	}
	if o.SandboxSocket != "" {
		c.SandboxSocket = o.SandboxSocket
	}
	if o.SandboxImage != "" {
		c.SandboxImage = o.SandboxImage
	}
	if o.SandboxRuntime != "" {
		c.SandboxRuntime = o.SandboxRuntime
	}
	if o.SandboxNetwork != "" {
		c.SandboxNetwork = o.SandboxNetwork
	}
	if o.SandboxWorkspace != "" {
		c.SandboxWorkspace = o.SandboxWorkspace
	}
}

// A risk_threshold of 0 cannot be told from an omitted key. A flag for
// it would need the -1 sentinel steps uses.

// envInt reads a numeric variable. Unset and unparseable both give 0,
// which merge skips.
func envInt(v string) int {
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return n
}

// envBool reads a true or false variable, in any of the spellings people
// use for one. Unset and unparseable both give nil, which merge skips.
func envBool(v string) *bool {
	var b bool
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "t", "true", "y", "yes", "on":
		b = true
	case "0", "f", "false", "n", "no", "off":
		b = false
	default:
		return nil
	}
	return &b
}

// envKeys are every variable envConfig reads.
var envKeys = []string{
	"DETENT_BASE_URL", "DETENT_MODEL", "DETENT_API_KEY", "OPENROUTER_API_KEY", "OPENAI_API_KEY",
	"DETENT_CONTEXT_TOKENS", "DETENT_COMMAND_TIMEOUT", "DETENT_FINISH_CHECK", "TYPESAFE_API_KEY",
	"DETENT_THEME", "DETENT_VIEWS", "DETENT_LOG_LEVEL", "DETENT_LOG_DIR", "DETENT_LOG_BODIES",
	"DETENT_SANDBOX_MODE", "DETENT_SANDBOX_SOCKET", "DETENT_SANDBOX_RUNTIME", "DETENT_SANDBOX_NETWORK",
}

// envConfig reads the environment as one layer. For aliased keys the
// first set variable wins.
func envConfig(getenv func(string) string) Config {
	return Config{
		BaseURL: getenv("DETENT_BASE_URL"),
		Model:   getenv("DETENT_MODEL"),
		APIKey: cmp.Or(
			getenv("DETENT_API_KEY"),
			getenv("OPENROUTER_API_KEY"),
			getenv("OPENAI_API_KEY"),
		),
		ContextTokens:  envInt(getenv("DETENT_CONTEXT_TOKENS")),
		CommandTimeout: getenv("DETENT_COMMAND_TIMEOUT"),
		FinishCheck:    envBool(getenv("DETENT_FINISH_CHECK")),
		JevAPIKey:      getenv("TYPESAFE_API_KEY"),
		Theme:          getenv("DETENT_THEME"),
		Views:          getenv("DETENT_VIEWS"),
		LogLevel:       getenv("DETENT_LOG_LEVEL"),
		LogDir:         getenv("DETENT_LOG_DIR"),
		LogBodies:      envBool(getenv("DETENT_LOG_BODIES")),

		SandboxMode:    getenv("DETENT_SANDBOX_MODE"),
		SandboxSocket:  getenv("DETENT_SANDBOX_SOCKET"),
		SandboxRuntime: getenv("DETENT_SANDBOX_RUNTIME"),
		SandboxNetwork: getenv("DETENT_SANDBOX_NETWORK"),
	}
}
