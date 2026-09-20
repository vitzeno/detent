package config

import (
	"cmp"
	"os"
)

// Resolve layers sources low to high: built-ins, file, environment,
// flags. Each layer only overwrites fields it actually sets. flags is
// a Config like any other layer — the caller builds it from whatever
// command-line flags were actually passed, leaving the rest zero.
//
// steps is separate because Config.Steps can't carry its own "not set"
// signal the way apply()'s other fields do: 0 is itself a meaningful,
// deliberate value (explicitly unbounded, see flag.Int's default in
// main.go), so flags.Steps alone could never distinguish "the flag
// wasn't passed" from "-steps 0 was passed". steps uses -1 for that.
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
}

// Known limitation: RiskThreshold and Steps both treat their Go zero
// value as "this layer didn't set it" (see the checks above), so
// neither can be explicitly reset to exactly 0 from the file or env
// layers — a config file with `risk_threshold: 0` is indistinguishable
// from one that omits the key entirely, and falls through to
// agent's own RiskThresholdDefault instead. Steps is unaffected in
// practice only because its own effective default (unbounded) already
// equals 0; RiskThreshold has no such coincidence. -steps has an
// explicit -1 sentinel (see Resolve) precisely to avoid this for the
// one layer where it was reachable; giving RiskThreshold a real flag
// would need the same treatment.

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
	}
}
