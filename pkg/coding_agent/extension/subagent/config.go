package subagent

import (
	"fmt"
	"os"
	"path/filepath"
)

// Config is the per-extension configuration loaded from the `config:` block
// of extensions.yaml.
type Config struct {
	// AgentsDir is the directory scanned for *.md agent profile files.
	// Defaults to ~/.modu_code/agents. Empty string disables discovery.
	AgentsDir string
	// DefaultModel is applied when the selected profile's Model field is
	// empty. Empty here too means "inherit caller's current model".
	DefaultModel string
	// MaxDepth caps how many nested subagent calls are allowed. Defaults
	// to 1 — only the main session can spawn; spawned children cannot
	// themselves spawn. Set higher to allow recursive delegation chains.
	MaxDepth int
	// TimeoutSeconds bounds one forked session's wall-clock time. Zero
	// means "no extension-imposed timeout" — context cancellation by the
	// host still applies.
	TimeoutSeconds int
}

// DefaultConfig returns the defaults applied when no `config:` block is
// present in extensions.yaml. Notably AgentsDir falls back to the
// well-known ~/.modu_code/agents/ path so a brand-new install can drop a
// profile there and have it picked up without any YAML.
func DefaultConfig() Config {
	return Config{
		AgentsDir:      defaultAgentsDir(),
		MaxDepth:       1,
		TimeoutSeconds: 600,
	}
}

func defaultAgentsDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".modu_code", "agents")
}

// apply merges a yaml-decoded map into c. Unknown keys produce errors so a
// typo'd config doesn't silently fall back to the default — the user wants
// to know immediately.
func (c *Config) apply(cfg map[string]any) error {
	for k, v := range cfg {
		switch k {
		case "agents_dir":
			s, err := asString(k, v)
			if err != nil {
				return err
			}
			c.AgentsDir = s
		case "default_model":
			s, err := asString(k, v)
			if err != nil {
				return err
			}
			c.DefaultModel = s
		case "max_depth":
			n, err := asInt(k, v)
			if err != nil {
				return err
			}
			c.MaxDepth = n
		case "timeout_seconds":
			n, err := asInt(k, v)
			if err != nil {
				return err
			}
			c.TimeoutSeconds = n
		default:
			return fmt.Errorf("unknown config key: %s", k)
		}
	}
	return nil
}

func asString(k string, v any) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s must be string, got %T", k, v)
	}
	return s, nil
}

func asInt(k string, v any) (int, error) {
	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64:
		return int(n), nil
	default:
		return 0, fmt.Errorf("%s must be int, got %T", k, v)
	}
}
