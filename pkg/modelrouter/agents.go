package modelrouter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// AgentManager writes only the settings required to route Codex or Claude Code.
// Previous values are saved under Home/.modu/modelrouter so Restore can return
// the agent to its earlier configuration.
type AgentManager struct {
	Home       string
	GatewayURL string
	Config     Config
}

func (m AgentManager) Use(agent, model string) error {
	if err := m.Config.Validate(); err != nil {
		return err
	}
	r, _ := New(m.Config)
	if _, _, ok := r.resolve(model); !ok {
		return fmt.Errorf("unknown model %q", model)
	}
	if !strings.HasPrefix(m.GatewayURL, "http://127.0.0.1:") && !strings.HasPrefix(m.GatewayURL, "http://[::1]:") && !strings.HasPrefix(m.GatewayURL, "http://localhost:") {
		return fmt.Errorf("gateway URL must be a loopback HTTP address")
	}
	switch agent {
	case "codex":
		return m.useCodex(model)
	case "claude":
		return m.useClaude(model)
	default:
		return fmt.Errorf("unsupported agent %q", agent)
	}
}

func (m AgentManager) Restore(agent string) error {
	switch agent {
	case "codex":
		return m.restoreCodex()
	case "claude":
		return m.restoreClaude()
	default:
		return fmt.Errorf("unsupported agent %q", agent)
	}
}

func (m AgentManager) home() string {
	if m.Home != "" {
		return m.Home
	}
	h, _ := os.UserHomeDir()
	return h
}

func (m AgentManager) codexDir() string {
	if m.Home == "" && os.Getenv("CODEX_HOME") != "" {
		return os.Getenv("CODEX_HOME")
	}
	return filepath.Join(m.home(), ".codex")
}

func (m AgentManager) claudeDir() string {
	if m.Home == "" && os.Getenv("CLAUDE_CONFIG_DIR") != "" {
		return os.Getenv("CLAUDE_CONFIG_DIR")
	}
	return filepath.Join(m.home(), ".claude")
}

func (m AgentManager) stashPath(agent string) string {
	return filepath.Join(m.home(), ".modu", "modelrouter", agent+"-stash.json")
}

func readStash(path string) (map[string]*string, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state map[string]*string
	if err := json.Unmarshal(b, &state); err != nil {
		return nil, err
	}
	return state, nil
}

func saveStash(path string, state map[string]*string) error {
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(b, '\n'), 0o600)
}

func atomicWrite(path string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".modelrouter-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func readOptional(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	return b, err
}

func pointer(s string) *string { return &s }

var topKey = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_-]*)\s*=\s*(.*)$`)

func getTOMLTop(b []byte, key string) *string {
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "[") {
			break
		}
		m := topKey.FindStringSubmatch(line)
		if m != nil && m[1] == key {
			var value string
			if _, err := fmt.Sscanf(m[2], "%q", &value); err == nil {
				return pointer(value)
			}
			return pointer(strings.TrimSpace(m[2]))
		}
	}
	return nil
}

func setTOMLTop(b []byte, values map[string]*string) []byte {
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	seen := map[string]bool{}
	var out []string
	firstTable := false
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "[") && !firstTable {
			firstTable = true
			keys := sortedMissing(values, seen)
			for _, key := range keys {
				if values[key] != nil {
					out = append(out, key+" = "+strconv.Quote(*values[key]))
				}
			}
		}
		if !firstTable {
			m := topKey.FindStringSubmatch(line)
			if m != nil {
				if v, ok := values[m[1]]; ok {
					seen[m[1]] = true
					if v != nil {
						out = append(out, m[1]+" = "+strconv.Quote(*v))
					}
					continue
				}
			}
		}
		out = append(out, line)
	}
	if !firstTable {
		for _, key := range sortedMissing(values, seen) {
			if values[key] != nil {
				out = append(out, key+" = "+strconv.Quote(*values[key]))
			}
		}
	}
	return []byte(strings.Join(out, "\n") + "\n")
}

func sortedMissing(values map[string]*string, seen map[string]bool) []string {
	var keys []string
	for key := range values {
		if !seen[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func removeTOMLTable(b []byte, name string) []byte {
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	var out []string
	skip := false
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "[") {
			skip = trim == "["+name+"]"
		}
		if !skip {
			out = append(out, line)
		}
	}
	return []byte(strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n")
}

func (m AgentManager) useCodex(model string) error {
	path := filepath.Join(m.codexDir(), "config.toml")
	b, err := readOptional(path)
	if err != nil {
		return err
	}
	if len(b) > 0 {
		var parsed any
		if err := toml.Unmarshal(b, &parsed); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	state, err := readStash(m.stashPath("codex"))
	if err != nil {
		return err
	}
	if state == nil {
		state = map[string]*string{}
		for _, key := range []string{"model", "model_provider", "model_catalog_json"} {
			state[key] = getTOMLTop(b, key)
		}
		if err := saveStash(m.stashPath("codex"), state); err != nil {
			return err
		}
	}
	catalog := filepath.Join(m.codexDir(), "modu-models.json")
	if err := atomicWrite(catalog, codexCatalog(m.Config), 0o600); err != nil {
		return err
	}
	b = removeTOMLTable(b, "model_providers.modu_router")
	b = setTOMLTop(b, map[string]*string{"model": pointer(model), "model_provider": pointer("modu_router"), "model_catalog_json": pointer(catalog)})
	b = append([]byte(strings.TrimRight(string(b), "\n")), []byte("\n\n[model_providers.modu_router]\nname = \"modu router\"\nbase_url = "+strconv.Quote(strings.TrimRight(m.GatewayURL, "/")+"/v1")+"\nwire_api = \"responses\"\nexperimental_bearer_token = \"modu\"\n")...)
	return atomicWrite(path, b, 0o600)
}

func codexCatalog(cfg Config) []byte {
	models := []map[string]any{}
	for _, p := range cfg.Providers {
		for _, model := range p.Models {
			id := p.ID + "/" + model
			models = append(models, map[string]any{
				"slug": id, "display_name": id, "description": id + " via modu router",
				"base_instructions":       "You are a coding assistant.",
				"default_reasoning_level": nil, "supported_reasoning_levels": []any{},
				"shell_type": "unified_exec", "visibility": "list", "supported_in_api": true,
				"priority": len(models) + 1, "support_verbosity": false, "default_verbosity": nil,
				"apply_patch_tool_type": "freeform", "truncation_policy": map[string]any{"mode": "tokens", "limit": 10000},
				"experimental_supported_tools": []string{}, "input_modalities": []string{"text"},
			})
		}
	}
	b, _ := json.MarshalIndent(map[string]any{"models": models}, "", "  ")
	return append(b, '\n')
}

func (m AgentManager) restoreCodex() error {
	state, err := readStash(m.stashPath("codex"))
	if err != nil || state == nil {
		return err
	}
	path := filepath.Join(m.codexDir(), "config.toml")
	b, err := readOptional(path)
	if err != nil {
		return err
	}
	if p := getTOMLTop(b, "model_provider"); p == nil || *p != "modu_router" {
		return fmt.Errorf("Codex no longer uses modu_router; refusing to overwrite its current settings")
	}
	b = removeTOMLTable(b, "model_providers.modu_router")
	b = setTOMLTop(b, state)
	if err := atomicWrite(path, b, 0o600); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(m.codexDir(), "modu-models.json"))
	return os.Remove(m.stashPath("codex"))
}

func (m AgentManager) useClaude(model string) error {
	path := filepath.Join(m.claudeDir(), "settings.json")
	b, err := readOptional(path)
	if err != nil {
		return err
	}
	settings := map[string]any{}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &settings); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	env, _ := settings["env"].(map[string]any)
	if env == nil {
		env = map[string]any{}
	}
	keys := []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_SMALL_FAST_MODEL"}
	state, err := readStash(m.stashPath("claude"))
	if err != nil {
		return err
	}
	if state == nil {
		state = map[string]*string{"model": optionalString(settings, "model")}
		for _, key := range keys {
			state[key] = optionalString(env, key)
		}
		if err := saveStash(m.stashPath("claude"), state); err != nil {
			return err
		}
	}
	settings["model"] = model
	env["ANTHROPIC_BASE_URL"] = strings.TrimRight(m.GatewayURL, "/")
	env["ANTHROPIC_AUTH_TOKEN"] = "modu"
	for _, key := range keys[2:] {
		env[key] = model
	}
	settings["env"] = env
	return writeJSONFile(path, settings)
}

func optionalString(obj map[string]any, key string) *string {
	if value, ok := obj[key].(string); ok {
		return pointer(value)
	}
	return nil
}

func writeJSONFile(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(b, '\n'), 0o600)
}

func (m AgentManager) restoreClaude() error {
	state, err := readStash(m.stashPath("claude"))
	if err != nil || state == nil {
		return err
	}
	path := filepath.Join(m.claudeDir(), "settings.json")
	b, err := readOptional(path)
	if err != nil {
		return err
	}
	settings := map[string]any{}
	if err := json.Unmarshal(b, &settings); err != nil {
		return err
	}
	env, _ := settings["env"].(map[string]any)
	if env == nil {
		return fmt.Errorf("Claude Code no longer uses the gateway")
	}
	if base, _ := env["ANTHROPIC_BASE_URL"].(string); base != strings.TrimRight(m.GatewayURL, "/") {
		return fmt.Errorf("Claude Code no longer uses the gateway; refusing to overwrite its current settings")
	}
	for key, value := range state {
		target := env
		if key == "model" {
			target = settings
		}
		if value == nil {
			delete(target, key)
		} else {
			target[key] = *value
		}
	}
	settings["env"] = env
	if err := writeJSONFile(path, settings); err != nil {
		return err
	}
	return os.Remove(m.stashPath("claude"))
}
