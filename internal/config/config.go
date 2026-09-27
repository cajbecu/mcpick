// Package config reads ~/.mcpick/config.yaml, the user's defaults for the
// picker. The file is optional and so is every key in it; mcpick never
// writes it. A key it cannot make sense of keeps its default and is reported
// as a warning, so a typo in a preference never stops a launch.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cajbecu/mcpick/internal/fsutil"
	"gopkg.in/yaml.v3"
)

// DefaultMaxRows is how many server rows the picker shows at once when the
// file says nothing.
const DefaultMaxRows = 10

// Config is what the file can set. Add a field here, a case in parse, and a
// line in docs/reference.md.
type Config struct {
	// MaxRows caps the server rows in view at once; the rest scroll.
	MaxRows int
}

// Default is the configuration with no file at all.
func Default() Config { return Config{MaxRows: DefaultMaxRows} }

// Path is the file: config.yaml in mcpick's home.
func Path() string { return filepath.Join(fsutil.MCPickHome(), "config.yaml") }

// Load reads Path. A missing file is the defaults; anything else that goes
// wrong is a warning for stderr, never an error, with the value it fell
// back to.
func Load() (Config, []string) { return Read(Path()) }

// Read is Load for a given file.
func Read(path string) (Config, []string) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Default(), nil
	}
	name := fsutil.ShortenHome(path)
	if err != nil {
		return Default(), []string{fmt.Sprintf("%s: %v; using defaults", name, err)}
	}
	return parse(data, name)
}

// parse reads the keys it knows and ignores the rest: an older mcpick must
// not complain about a key a newer one added.
func parse(data []byte, name string) (Config, []string) {
	cfg := Default()
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return cfg, []string{fmt.Sprintf("%s: %v; using defaults", name, err)}
	}
	var warns []string
	warn := func(key string, got any, want string, using any) {
		warns = append(warns, fmt.Sprintf("%s: %s: want %s, got %v; using %v", name, key, want, got, using))
	}
	if v, ok := raw["max_rows"]; ok {
		if n, ok := v.(int); ok && n >= 1 {
			cfg.MaxRows = n
		} else {
			warn("max_rows", v, "a whole number of at least 1", cfg.MaxRows)
		}
	}
	return cfg, warns
}
