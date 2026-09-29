// Package config handles configuration for maestro-runner.
package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config represents the workspace configuration (config.yaml).
type Config struct {
	// App info
	AppID string `yaml:"appId"` // App bundle ID or package name

	// Flow selection
	Flows       []string `yaml:"flows"`       // Glob patterns for flows
	IncludeTags []string `yaml:"includeTags"` // Tags to include
	ExcludeTags []string `yaml:"excludeTags"` // Tags to exclude

	// Execution settings
	Env map[string]string `yaml:"env"` // Environment variables

	// Device settings
	Platform string `yaml:"platform"` // Target platform
	Device   string `yaml:"device"`   // Target device

	// Driver settings
	WaitForIdleTimeout int `yaml:"waitForIdleTimeout"` // Wait for device idle in ms (0 = disabled, default 200)

	// Per-platform options, Maestro's `platform:` map
	// (platform: {android: {disableAnimations: true}}). `platform: android`
	// still sets Platform above.
	PlatformSettings PlatformSettings `yaml:"-"`
}

// PlatformSettings holds Maestro's per-platform workspace options.
type PlatformSettings struct {
	Android PlatformOptions `yaml:"android"`
	IOS     PlatformOptions `yaml:"ios"`
}

// PlatformOptions are the options Maestro supports for one platform.
type PlatformOptions struct {
	DisableAnimations bool `yaml:"disableAnimations"`
}

// DisableAnimations reports whether the workspace turns animations off for
// the given platform ("android" or "ios").
func (c *Config) DisableAnimations(platform string) bool {
	if c == nil {
		return false
	}
	switch platform {
	case "android":
		return c.PlatformSettings.Android.DisableAnimations
	case "ios":
		return c.PlatformSettings.IOS.DisableAnimations
	}
	return false
}

// UnmarshalYAML accepts `platform:` both as our target-platform string and
// as Maestro's map of per-platform options.
func (c *Config) UnmarshalYAML(node *yaml.Node) error {
	var settings *yaml.Node
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == "platform" && node.Content[i+1].Kind == yaml.MappingNode {
				settings = node.Content[i+1]
				content := make([]*yaml.Node, 0, len(node.Content)-2)
				content = append(content, node.Content[:i]...)
				node.Content = append(content, node.Content[i+2:]...)
				break
			}
		}
	}
	type plain Config
	if err := node.Decode((*plain)(c)); err != nil {
		return err
	}
	if settings != nil {
		return settings.Decode(&c.PlatformSettings)
	}
	return nil
}

// Load loads configuration from a file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path) //#nosec G304 -- user-provided config file
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// LoadFromDir looks for config.yaml or config.yml in the directory.
func LoadFromDir(dir string) (*Config, error) {
	// Try config.yaml first
	configPath := filepath.Join(dir, "config.yaml")
	if _, err := os.Stat(configPath); err == nil {
		return Load(configPath)
	}

	// Try config.yml
	configPath = filepath.Join(dir, "config.yml")
	if _, err := os.Stat(configPath); err == nil {
		return Load(configPath)
	}

	// No config file found, return empty config
	return &Config{}, nil
}
