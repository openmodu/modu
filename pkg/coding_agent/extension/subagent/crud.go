package subagent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	csubagent "github.com/openmodu/modu/pkg/coding_agent/subagent"
	"github.com/openmodu/modu/pkg/utils"
)

// crudConfigKeys lists the frontmatter keys we recognise on create/update.
// Unknown keys produce an error so a typo doesn't silently land in a profile
// file and become invisible at parse time.
var crudConfigKeys = map[string]struct{}{
	"name":                {},
	"description":         {},
	"systemPrompt":        {},
	"system_prompt":       {},
	"tools":               {},
	"disallowed_tools":    {},
	"disallowed-tools":    {},
	"harness_block_tools": {},
	"harness-block-tools": {},
	"skills":              {},
	"memory":              {},
	"memory_scope":        {},
	"memory-scope":        {},
	"permission_mode":     {},
	"permission-mode":     {},
	"background":          {},
	"effort":              {},
	"isolation":           {},
	"model":               {},
	"default_context":     {},
	"default-context":     {},
	"thinking":            {},
	"thinking_level":      {},
	"thinking-level":      {},
	"max_turns":           {},
	"max-turns":           {},
	"default_reads":       {},
	"default-reads":       {},
	"reads":               {},
	"default_progress":    {},
	"default-progress":    {},
	"progress":            {},
	"scope":               {},
}

// nameSanitizer trims a user-supplied profile name to kebab-case. Same rule
// as pi-subagents' sanitizeName so the on-disk filename and discovery name
// agree.
var nameSanitizer = regexp.MustCompile(`[^a-z0-9-]+`)

func handleGet(ext *Extension, args map[string]any) (string, error) {
	name, _ := args["agent"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf(`get requires "agent"`)
	}
	scope, err := decodeAgentScope(args)
	if err != nil {
		return "", err
	}
	def, ok := ext.loader.Get(name)
	if !ok {
		return "", fmt.Errorf("agent %q not found", name)
	}
	if scope != "" && scope != "both" && def.Source != scope {
		return "", fmt.Errorf("agent %q not found in scope %q (source: %s)", name, scope, def.Source)
	}
	return formatAgentDetail(def), nil
}

func handleCreate(ext *Extension, args map[string]any) (string, error) {
	cfg, err := configMap(args["config"])
	if err != nil {
		return "", err
	}
	if cfg == nil {
		return "", fmt.Errorf(`create requires "config" object`)
	}
	name, err := requiredConfigName(cfg)
	if err != nil {
		return "", err
	}
	if _, exists := ext.loader.Get(name); exists {
		return "", fmt.Errorf("agent %q already exists; use action=update to modify", name)
	}
	// Use the sanitized name everywhere — the frontmatter, the filename, and
	// what loader.Get returns must all agree.
	cfg["name"] = name
	targetDir, source, err := targetDirForCreate(ext, cfg)
	if err != nil {
		return "", err
	}
	path := filepath.Join(targetDir, name+".md")
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("profile file already exists at %s; remove it or use action=update", path)
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", fmt.Errorf("create profile dir %s: %w", targetDir, err)
	}
	content := buildProfileContent(cfg, "")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write profile %s: %w", path, err)
	}
	ext.discover()
	return fmt.Sprintf("Created agent %q at %s (source: %s).", name, path, source), nil
}

func handleUpdate(ext *Extension, args map[string]any) (string, error) {
	name, _ := args["agent"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf(`update requires "agent"`)
	}
	def, ok := ext.loader.Get(name)
	if !ok {
		return "", fmt.Errorf("agent %q not found", name)
	}
	cfg, err := configMap(args["config"])
	if err != nil {
		return "", err
	}
	if cfg == nil {
		return "", fmt.Errorf(`update requires "config" object`)
	}
	if newName, ok := cfg["name"].(string); ok {
		if strings.TrimSpace(newName) != "" && strings.TrimSpace(newName) != def.Name {
			return "", fmt.Errorf("renaming via update is not supported; delete and recreate to rename")
		}
	}
	if scope, ok := cfg["scope"].(string); ok && strings.TrimSpace(scope) != "" {
		return "", fmt.Errorf("scope changes via update are not supported; delete and recreate to move scope")
	}
	existing, err := os.ReadFile(def.FilePath)
	if err != nil {
		return "", fmt.Errorf("read profile %s: %w", def.FilePath, err)
	}
	merged, body := mergeFrontmatter(string(existing), cfg)
	content := assembleProfileContent(merged, body)
	if err := os.WriteFile(def.FilePath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write profile %s: %w", def.FilePath, err)
	}
	ext.discover()
	return fmt.Sprintf("Updated agent %q at %s.", def.Name, def.FilePath), nil
}

func handleDelete(ext *Extension, args map[string]any) (string, error) {
	name, _ := args["agent"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf(`delete requires "agent"`)
	}
	def, ok := ext.loader.Get(name)
	if !ok {
		return "", fmt.Errorf("agent %q not found", name)
	}
	if err := os.Remove(def.FilePath); err != nil {
		return "", fmt.Errorf("remove profile %s: %w", def.FilePath, err)
	}
	ext.discover()
	return fmt.Sprintf("Deleted agent %q (was at %s).", def.Name, def.FilePath), nil
}

// configMap decodes the action's "config" argument. Accepts a map directly or
// an inline JSON-like string-encoded shape (mirrors pi's permissive intake).
func configMap(raw any) (map[string]any, error) {
	if raw == nil {
		return nil, nil
	}
	switch v := raw.(type) {
	case map[string]any:
		return v, nil
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return nil, nil
		}
		return nil, fmt.Errorf("config string parsing is not supported; pass an object")
	default:
		return nil, fmt.Errorf("config must be an object, got %T", raw)
	}
}

func requiredConfigName(cfg map[string]any) (string, error) {
	raw, ok := cfg["name"].(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf(`config.name is required`)
	}
	name := sanitizeProfileName(raw)
	if name == "" {
		return "", fmt.Errorf("config.name %q sanitizes to empty; use kebab-case (a-z, 0-9, -)", raw)
	}
	return name, nil
}

func sanitizeProfileName(raw string) string {
	lower := strings.ToLower(strings.TrimSpace(raw))
	lower = strings.ReplaceAll(lower, " ", "-")
	cleaned := nameSanitizer.ReplaceAllString(lower, "-")
	for strings.Contains(cleaned, "--") {
		cleaned = strings.ReplaceAll(cleaned, "--", "-")
	}
	return strings.Trim(cleaned, "-")
}

// targetDirForCreate picks where to write a new profile:
//   - cfg.AgentsDir set → always write there.
//   - otherwise scope=user → {AgentDir}/agents; scope=project (default) →
//     {Cwd}/.coding_agent/agents.
func targetDirForCreate(ext *Extension, cfg map[string]any) (string, string, error) {
	if ext.cfg.AgentsDir != "" {
		return ext.cfg.AgentsDir, "extra", nil
	}
	scope := "project"
	if raw, ok := cfg["scope"].(string); ok && strings.TrimSpace(raw) != "" {
		scope = strings.ToLower(strings.TrimSpace(raw))
	}
	switch scope {
	case "user":
		if ext.api == nil {
			return "", "", fmt.Errorf("user scope requires the host API; extension not initialized")
		}
		return filepath.Join(ext.api.AgentDir(), "agents"), "user", nil
	case "project":
		if ext.api == nil {
			return "", "", fmt.Errorf("project scope requires the host API; extension not initialized")
		}
		return filepath.Join(ext.api.Cwd(), ".coding_agent", "agents"), "project", nil
	default:
		return "", "", fmt.Errorf("scope must be 'user' or 'project', got %q", scope)
	}
}

// buildProfileContent produces the full .md file contents for create. The
// body argument lets handlers seed an empty file with a starter string; we
// currently pass "" and rely on cfg.systemPrompt to supply the body.
func buildProfileContent(cfg map[string]any, body string) string {
	frontmatter := frontmatterFromConfig(cfg)
	bodyText, ok := stringField(cfg, "systemPrompt", "system_prompt")
	if ok {
		body = bodyText
	}
	return assembleProfileContent(frontmatter, body)
}

func assembleProfileContent(frontmatter map[string]string, body string) string {
	var b strings.Builder
	if len(frontmatter) > 0 {
		b.WriteString("---\n")
		for _, key := range orderedFrontmatterKeys(frontmatter) {
			fmt.Fprintf(&b, "%s: %s\n", key, frontmatter[key])
		}
		b.WriteString("---\n")
	}
	body = strings.TrimSpace(body)
	if body != "" {
		b.WriteString("\n")
		b.WriteString(body)
		b.WriteString("\n")
	}
	return b.String()
}

// frontmatterFromConfig translates the cfg map into a frontmatter dict. Only
// known keys are emitted; non-frontmatter keys like systemPrompt/scope are
// consumed elsewhere.
func frontmatterFromConfig(cfg map[string]any) map[string]string {
	out := map[string]string{}
	for key, value := range cfg {
		if _, ok := crudConfigKeys[key]; !ok {
			continue
		}
		if isBodyField(key) || isVirtualField(key) {
			continue
		}
		s, ok := stringifyConfigValue(value)
		if !ok || strings.TrimSpace(s) == "" {
			continue
		}
		out[normalizeFrontmatterKey(key)] = s
	}
	return out
}

func mergeFrontmatter(existing string, cfg map[string]any) (map[string]string, string) {
	frontMap, body, _ := utils.ParseFrontmatter(existing)
	merged := map[string]string{}
	for k, v := range frontMap {
		merged[k] = v
	}
	updates := frontmatterFromConfig(cfg)
	for k, v := range updates {
		merged[k] = v
	}
	if newBody, ok := stringField(cfg, "systemPrompt", "system_prompt"); ok {
		body = newBody
	}
	return merged, body
}

func stringField(cfg map[string]any, aliases ...string) (string, bool) {
	for _, key := range aliases {
		if raw, ok := cfg[key]; ok {
			if s, ok := raw.(string); ok {
				return s, true
			}
		}
	}
	return "", false
}

func isBodyField(key string) bool {
	return key == "systemPrompt" || key == "system_prompt"
}

func isVirtualField(key string) bool {
	return key == "scope"
}

// normalizeFrontmatterKey collapses aliases so the on-disk file uses the
// canonical name the loader expects.
func normalizeFrontmatterKey(key string) string {
	switch key {
	case "memory_scope", "memory-scope":
		return "memory"
	case "default-context":
		return "default_context"
	case "thinking_level", "thinking-level":
		return "thinking"
	case "max-turns":
		return "max_turns"
	case "default-reads":
		return "default_reads"
	case "default-progress":
		return "default_progress"
	case "disallowed-tools":
		return "disallowed_tools"
	case "harness-block-tools":
		return "harness_block_tools"
	default:
		return key
	}
}

// stringifyConfigValue coerces JSON-decoded values into the simple string
// format the frontmatter parser expects. Lists become CSV; booleans become
// "true"/"false"; numbers become their decimal text.
func stringifyConfigValue(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case bool:
		return strconv.FormatBool(v), true
	case int:
		return strconv.Itoa(v), true
	case int64:
		return strconv.FormatInt(v, 10), true
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10), true
		}
		return strconv.FormatFloat(v, 'f', -1, 64), true
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					parts = append(parts, s)
				}
			}
		}
		return strings.Join(parts, ","), true
	case []string:
		parts := make([]string, 0, len(v))
		for _, s := range v {
			if s = strings.TrimSpace(s); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ","), true
	default:
		return "", false
	}
}

func orderedFrontmatterKeys(m map[string]string) []string {
	priority := []string{
		"name", "description", "model", "tools", "disallowed_tools",
		"harness_block_tools", "skills", "memory", "permission_mode",
		"background", "effort", "isolation", "default_context", "thinking",
		"max_turns", "default_reads", "default_progress",
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(m))
	for _, key := range priority {
		if _, ok := m[key]; ok {
			out = append(out, key)
			seen[key] = true
		}
	}
	for key := range m {
		if !seen[key] {
			out = append(out, key)
		}
	}
	return out
}

// formatAgentDetail mirrors pi-subagents' detail rendering — concise enough
// for an LLM to consume, with one line per frontmatter field plus the system
// prompt body.
func formatAgentDetail(def *csubagent.SubagentDefinition) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Agent %s\n", def.Name)
	if def.Source != "" {
		fmt.Fprintf(&b, "source: %s\n", def.Source)
	}
	if def.FilePath != "" {
		fmt.Fprintf(&b, "file: %s\n", def.FilePath)
	}
	if def.Description != "" {
		fmt.Fprintf(&b, "description: %s\n", def.Description)
	}
	if def.Model != "" {
		fmt.Fprintf(&b, "model: %s\n", def.Model)
	}
	if len(def.Tools) > 0 {
		fmt.Fprintf(&b, "tools: %s\n", strings.Join(def.Tools, ","))
	}
	if len(def.DisallowedTools) > 0 {
		fmt.Fprintf(&b, "disallowed_tools: %s\n", strings.Join(def.DisallowedTools, ","))
	}
	if len(def.HarnessBlockTools) > 0 {
		fmt.Fprintf(&b, "harness_block_tools: %s\n", strings.Join(def.HarnessBlockTools, ","))
	}
	if len(def.Skills) > 0 {
		fmt.Fprintf(&b, "skills: %s\n", strings.Join(def.Skills, ","))
	}
	if def.MemoryScope != "" {
		fmt.Fprintf(&b, "memory: %s\n", def.MemoryScope)
	}
	if def.PermissionMode != "" {
		fmt.Fprintf(&b, "permission_mode: %s\n", def.PermissionMode)
	}
	if def.Background {
		b.WriteString("background: true\n")
	}
	if def.Effort != "" {
		fmt.Fprintf(&b, "effort: %s\n", def.Effort)
	}
	if def.Isolation != "" {
		fmt.Fprintf(&b, "isolation: %s\n", def.Isolation)
	}
	if def.DefaultContext != "" {
		fmt.Fprintf(&b, "default_context: %s\n", def.DefaultContext)
	}
	if def.ThinkingLevel != "" {
		fmt.Fprintf(&b, "thinking: %s\n", def.ThinkingLevel)
	}
	if def.MaxTurns > 0 {
		fmt.Fprintf(&b, "max_turns: %d\n", def.MaxTurns)
	}
	if len(def.DefaultReads) > 0 {
		fmt.Fprintf(&b, "default_reads: %s\n", strings.Join(def.DefaultReads, ","))
	}
	if def.DefaultProgress {
		b.WriteString("default_progress: true\n")
	}
	body := strings.TrimSpace(def.SystemPrompt)
	if body != "" {
		b.WriteString("\nsystem prompt:\n")
		b.WriteString(body)
	}
	return strings.TrimRight(b.String(), "\n")
}
