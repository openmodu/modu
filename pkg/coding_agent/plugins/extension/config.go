// Package extension's config.go resolves the active extension list for a
// CodingSession by combining the builtin Registry with the on-disk YAML
// config at ~/.modu/extensions.yaml (legacy ~/.modu_code/extensions.yaml is
// still honored when only it exists).
//
// Resolution rules:
//
//	missing/empty file  → every registered builtin, lexicographic order, no per-ext config
//	present file        → entries listed in `extensions:` first (file order),
//	                      then every unmentioned builtin (lexicographic order).
//	                      The file is an overlay, not a whitelist — configuring
//	                      one extension does not silently drop the others.
//	  - `enabled: false`   → extension disabled (the only way to turn one off)
//	  - unknown `name`     → warning to stderr, entry skipped
//	  - duplicate `name`   → warning to stderr, first occurrence kept
//	  - non-nil `config:`  → passed to ApplyConfig if the extension implements
//	                         ConfigurableExtension; ignored otherwise
//
// Unknown-name and duplicate-name conditions warn instead of erroring so an
// older config file does not lock out a new modu_code build (or vice versa).
package extension

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ExtensionEntry is one entry in extensions.yaml.
type ExtensionEntry struct {
	Name string `yaml:"name"`
	// Enabled is a pointer so an absent field defaults to "enabled"; only an
	// explicit `enabled: false` skips the entry.
	Enabled *bool          `yaml:"enabled,omitempty"`
	Config  map[string]any `yaml:"config,omitempty"`
}

// configFile mirrors the on-disk shape of extensions.yaml.
type configFile struct {
	Extensions []ExtensionEntry `yaml:"extensions"`
}

// LoadOptions controls how LoadEnabled resolves the extension list. Both
// fields are optional — leaving them zero uses production defaults.
type LoadOptions struct {
	// ConfigPath overrides the default ~/.modu/extensions.yaml lookup.
	// Tests pass an explicit path; production passes "".
	ConfigPath string
	// Stderr receives one-line warnings for unknown / duplicate names.
	// Defaults to os.Stderr.
	Stderr io.Writer
}

// DefaultConfigPath returns ~/.modu/extensions.yaml (the agent runtime
// directory), falling back to the legacy ~/.modu_code/extensions.yaml when
// only that file exists. Empty string if the home directory cannot be
// determined — LoadEnabled treats that as "no config file" and falls back
// to builtins.
func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	primary := filepath.Join(home, ".modu", "extensions.yaml")
	if fileExists(primary) {
		return primary
	}
	if legacy := filepath.Join(home, ".modu_code", "extensions.yaml"); fileExists(legacy) {
		return legacy
	}
	return primary
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// LoadEnabled resolves the active extension list. See the package-level
// docstring on this file for the full resolution rules.
//
// Returned errors are limited to parse failures and ApplyConfig errors —
// unknown / missing names never error.
func LoadEnabled(opts LoadOptions) ([]Extension, error) {
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	path := opts.ConfigPath
	if path == "" {
		path = DefaultConfigPath()
	}

	cfg, exists, err := readConfigFile(path)
	if err != nil {
		return nil, err
	}
	if !exists {
		return buildExtensions(BuiltinNames(), nil)
	}

	seen := map[string]bool{}
	var order []string
	perConfig := map[string]map[string]any{}
	for _, entry := range cfg.Extensions {
		if entry.Name == "" {
			continue
		}
		if seen[entry.Name] {
			fmt.Fprintf(stderr, "extension %q listed twice in %s, keeping first occurrence\n",
				entry.Name, path)
			continue
		}
		seen[entry.Name] = true
		if entry.Enabled != nil && !*entry.Enabled {
			continue
		}
		if _, ok := FactoryFor(entry.Name); !ok {
			fmt.Fprintf(stderr, "extension %q in %s not registered, skipping\n",
				entry.Name, path)
			continue
		}
		order = append(order, entry.Name)
		perConfig[entry.Name] = entry.Config
	}
	// Overlay semantics: builtins the file never mentions stay enabled, so a
	// user adding `config:` for one extension doesn't have to enumerate (and
	// hand-maintain) the full builtin list.
	for _, name := range BuiltinNames() {
		if !seen[name] {
			order = append(order, name)
		}
	}
	return buildExtensions(order, perConfig)
}

// buildExtensions invokes each factory, optionally applies per-ext config,
// and returns the resulting Extension instances in the given order.
func buildExtensions(names []string, perConfig map[string]map[string]any) ([]Extension, error) {
	out := make([]Extension, 0, len(names))
	for _, n := range names {
		factory, ok := FactoryFor(n)
		if !ok {
			continue
		}
		ext := factory()
		if cfg, ok := perConfig[n]; ok && cfg != nil {
			if c, ok := ext.(ConfigurableExtension); ok {
				if err := c.ApplyConfig(cfg); err != nil {
					return nil, fmt.Errorf("apply config to extension %q: %w", n, err)
				}
			}
		}
		out = append(out, ext)
	}
	return out, nil
}

// readConfigFile loads and decodes the YAML at path. Returns (nil, false, nil)
// for missing or completely empty files so the caller can fall back cleanly.
func readConfigFile(path string) (*configFile, bool, error) {
	if path == "" {
		return nil, false, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer f.Close()

	var cfg configFile
	dec := yaml.NewDecoder(f)
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			// Empty file — treat as "no config".
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cfg, true, nil
}
