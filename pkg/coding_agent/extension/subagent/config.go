package subagent

import (
	"fmt"
)

// Config is the per-extension configuration loaded from the `config:` block
// of extensions.yaml.
type Config struct {
	// AgentsDir is the directory scanned for *.md agent profile files.
	// Empty means use the host's standard agent discovery paths:
	// {AgentDir}/agents and {Cwd}/.coding_agent/agents.
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
	// ForceTopLevelAsync mirrors pi's `forceTopLevelAsync` config. When true,
	// a top-level single-mode call that omits the `async` argument defaults
	// to background dispatch (overriding the profile's `background: false`).
	// An explicit `async: false` still wins. Parallel/chain batch async is
	// not yet covered — see PARITY.md.
	ForceTopLevelAsync bool
}

// DefaultConfig returns the defaults applied when no `config:` block is
// present in extensions.yaml.
func DefaultConfig() Config {
	return Config{
		MaxDepth:       1,
		TimeoutSeconds: 600,
	}
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
		case "force_top_level_async", "force-top-level-async":
			b, err := asBool(k, v)
			if err != nil {
				return err
			}
			c.ForceTopLevelAsync = b
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

func asBool(k string, v any) (bool, error) {
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be bool, got %T", k, v)
	}
	return b, nil
}
