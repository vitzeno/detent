package config

import (
	"cmp"
	"os"
)

// Resolve layers sources low to high: built-ins, file, environment,
// flags. Each layer only overwrites fields it actually sets.
//
// steps is separate: 0 is a real value for Config.Steps (explicitly
// unbounded), so flags.Steps can't tell "not passed" from "-steps 0".
// steps uses -1 for that instead.
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
	if o.Headers != nil {
		c.Headers = o.Headers
	}
	if o.Steps != 0 {
		c.Steps = o.Steps
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
	if o.SandboxWorkspace != "" {
		c.SandboxWorkspace = o.SandboxWorkspace
	}
}

// Known limitation: a config file with `risk_threshold: 0` is
// indistinguishable from one that omits the key, and falls through to
// agent's RiskThresholdDefault. Steps dodges this only because its own
// default (unbounded) already is 0. Giving RiskThreshold a flag would
// need the same -1 sentinel treatment as steps above.

// envConfig reads the environment as one layer. Key aliases fall back in
// order; the first set variable wins.
func envConfig() Config {
	return Config{
		BaseURL: os.Getenv("DETENT_BASE_URL"),
		Model:   os.Getenv("DETENT_MODEL"),
		APIKey: cmp.Or(
			os.Getenv("DETENT_API_KEY"),
			os.Getenv("OPENROUTER_API_KEY"),
			os.Getenv("OPENAI_API_KEY"),
		),
		JevAPIKey: os.Getenv("TYPESAFE_API_KEY"),
		Theme:     os.Getenv("DETENT_THEME"),

		SandboxMode:    os.Getenv("DETENT_SANDBOX_MODE"),
		SandboxSocket:  os.Getenv("DETENT_SANDBOX_SOCKET"),
		SandboxRuntime: os.Getenv("DETENT_SANDBOX_RUNTIME"),
	}
}
