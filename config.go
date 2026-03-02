package ark

// CRC: crc-Config.md

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config represents the parsed ark.toml configuration.
type Config struct {
	Dotfiles      bool     `toml:"dotfiles"`
	GlobalInclude []string `toml:"include"`
	GlobalExclude []string `toml:"exclude"`
	Sources       []Source `toml:"source"`
	Errors        []string `toml:"-"`
}

// Source is a directory entry in the configuration.
type Source struct {
	Dir      string   `toml:"dir"`
	Strategy string   `toml:"strategy"`
	Include  []string `toml:"include"`
	Exclude  []string `toml:"exclude"`
}

// LoadConfig reads and validates an ark.toml file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.validate()
	// Expand ~ in source directory paths
	for i := range cfg.Sources {
		cfg.Sources[i].Dir = expandHome(cfg.Sources[i].Dir)
	}
	return &cfg, nil
}

// WriteDefaultConfig writes an initial ark.toml with default excludes.
func WriteDefaultConfig(path string) error {
	const defaultConfig = `# Ark configuration
dotfiles = true

# Global patterns — apply to all sources
include = []
exclude = [".git/", ".env", "node_modules/", "__pycache__/", ".DS_Store"]

# Sources — directories to watch
# [[source]]
# dir = "~/notes"
# strategy = "markdown"
`
	return os.WriteFile(path, []byte(defaultConfig), 0644)
}

// EffectivePatterns returns the combined global + per-source patterns.
func (c *Config) EffectivePatterns(src Source) (includes, excludes []string) {
	includes = make([]string, 0, len(c.GlobalInclude)+len(src.Include))
	includes = append(includes, c.GlobalInclude...)
	includes = append(includes, src.Include...)

	excludes = make([]string, 0, len(c.GlobalExclude)+len(src.Exclude))
	excludes = append(excludes, c.GlobalExclude...)
	excludes = append(excludes, src.Exclude...)
	return
}

// HasErrors returns true if the config has validation errors.
func (c *Config) HasErrors() bool {
	return len(c.Errors) > 0
}

// validate checks for identical include/exclude strings.
func (c *Config) validate() {
	c.Errors = nil
	// Check global patterns
	c.checkDuplicates(c.GlobalInclude, c.GlobalExclude, "global")
	// Check per-source patterns against their effective set
	for _, src := range c.Sources {
		inc, exc := c.EffectivePatterns(src)
		c.checkDuplicates(inc, exc, src.Dir)
	}
}

func (c *Config) checkDuplicates(includes, excludes []string, context string) {
	excSet := make(map[string]bool, len(excludes))
	for _, e := range excludes {
		excSet[e] = true
	}
	for _, inc := range includes {
		if excSet[inc] {
			c.Errors = append(c.Errors, fmt.Sprintf(
				"pattern %q appears in both include and exclude (%s)", inc, context))
		}
	}
}

func expandHome(path string) string {
	if len(path) >= 2 && path[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}
