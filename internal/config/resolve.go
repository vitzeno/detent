package config

import (
	"cmp"
	"os"
)

// Resolve layers sources low to high: built-ins, file, environment,
// flags. Each layer only overwrites fields it actually sets.
func Resolve(file Config, url, model, key string, steps int) Config {
	out := Default()
	out.apply(file)
	out.apply(envConfig())
	out.apply(Config{BaseURL: url, Model: model, APIKey: key})
	if steps >= 0 { // flags default to -1 = unset; 0 explicitly selects unbounded
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
