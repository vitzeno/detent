package config

import (
	"cmp"
	"os"
	"strconv"
)

// Resolve layers built-ins, file, environment, then flags, each setting
// only the fields it has. steps is -1 when not passed, so -steps 0 applies.
func Resolve(file, flags Config, steps int) Config {
	out := Default()
	out.apply(file)
	out.apply(envConfig())
	out.apply(flags)
	if steps >= 0 {
		out.Steps = steps
	}
	return out
}

// apply overwrites the fields o sets, leaving the rest alone.
func (c *Config) apply(o Config) {
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
	if o.LogBodies {
		c.LogBodies = true
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
// which apply skips.
func envInt(key string) int {
	n, err := strconv.Atoi(os.Getenv(key))
	if err != nil {
		return 0
	}
	return n
}

// envConfig reads the environment as one layer. For aliased keys the
// first set variable wins.
func envConfig() Config {
	return Config{
		BaseURL: os.Getenv("DETENT_BASE_URL"),
		Model:   os.Getenv("DETENT_MODEL"),
		APIKey: cmp.Or(
			os.Getenv("DETENT_API_KEY"),
			os.Getenv("OPENROUTER_API_KEY"),
			os.Getenv("OPENAI_API_KEY"),
		),
		ContextTokens:  envInt("DETENT_CONTEXT_TOKENS"),
		CommandTimeout: os.Getenv("DETENT_COMMAND_TIMEOUT"),
		JevAPIKey:      os.Getenv("TYPESAFE_API_KEY"),
		Theme:          os.Getenv("DETENT_THEME"),
		Views:          os.Getenv("DETENT_VIEWS"),
		LogLevel:       os.Getenv("DETENT_LOG_LEVEL"),
		LogDir:         os.Getenv("DETENT_LOG_DIR"),
		LogBodies:      os.Getenv("DETENT_LOG_BODIES") != "",

		SandboxMode:    os.Getenv("DETENT_SANDBOX_MODE"),
		SandboxSocket:  os.Getenv("DETENT_SANDBOX_SOCKET"),
		SandboxRuntime: os.Getenv("DETENT_SANDBOX_RUNTIME"),
		SandboxNetwork: os.Getenv("DETENT_SANDBOX_NETWORK"),
	}
}
