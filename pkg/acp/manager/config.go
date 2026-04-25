package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// AgentConfig describes one ACP agent (typically one CLI binary: Claude
// Code, Codex, Gemini).
type AgentConfig struct {
	ID             string            `json:"id"`
	Name           string            `json:"name,omitempty"`
	Command        string            `json:"command"`
	Args           []string          `json:"args,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	PermissionMode string            `json:"permissionMode,omitempty"` // "default" | "bypass"
}

// Config is the root config object. Version is declared so future config
// shapes can be detected; readers should default to 1.
type Config struct {
	Version      int           `json:"version,omitempty"`
	Agents       []AgentConfig `json:"agents"`
	DefaultAgent string        `json:"defaultAgent,omitempty"`
	Workdir      string        `json:"workdir,omitempty"` // default cwd for tasks; empty = process cwd
}

// DefaultConfigPaths is the lookup order LoadConfig uses when called with
// no explicit paths. First match wins.
func DefaultConfigPaths() []string {
	home, _ := os.UserHomeDir()
	paths := []string{"acp.config.json"}
	if home != "" {
		paths = append(paths,
			filepath.Join(home, ".modu", "acp.config.json"),
			filepath.Join(home, ".config", "modu", "acp.json"),
		)
	}
	return paths
}

// LoadConfig reads the first existing file from paths (or DefaultConfigPaths
// when paths is empty) and parses it as a Config. It validates agent IDs
// are unique and DefaultAgent — if set — refers to a known agent.
func LoadConfig(paths ...string) (*Config, error) {
	if len(paths) == 0 {
		paths = DefaultConfigPaths()
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("acp/manager: read %s: %w", p, err)
		}
		return parseConfig(data, p)
	}
	return nil, fmt.Errorf("acp/manager: no config file found in %v", paths)
}

func parseConfig(data []byte, source string) (*Config, error) {
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("acp/manager: parse %s: %w", source, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("acp/manager: validate %s: %w", source, err)
	}
	return &cfg, nil
}

// Validate enforces schema invariants.
func (c *Config) Validate() error {
	seen := make(map[string]bool, len(c.Agents))
	for i, a := range c.Agents {
		if a.ID == "" {
			return fmt.Errorf("agents[%d]: id is required", i)
		}
		if seen[a.ID] {
			return fmt.Errorf("agents[%d]: duplicate id %q", i, a.ID)
		}
		seen[a.ID] = true
		if a.Command == "" {
			return fmt.Errorf("agents[%d] (%s): command is required", i, a.ID)
		}
	}
	if c.DefaultAgent != "" && !seen[c.DefaultAgent] {
		return fmt.Errorf("defaultAgent %q not in agents list", c.DefaultAgent)
	}
	return nil
}

// Agent looks up an agent config by ID.
func (c *Config) Agent(id string) (AgentConfig, bool) {
	for _, a := range c.Agents {
		if a.ID == id {
			return a, true
		}
	}
	return AgentConfig{}, false
}
