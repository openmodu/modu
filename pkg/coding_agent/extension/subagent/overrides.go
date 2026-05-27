package subagent

import (
	"fmt"
	"strings"

	csubagent "github.com/openmodu/modu/pkg/coding_agent/subagent"
)

type skillOverride struct {
	set      bool
	disabled bool
	names    []string
}

func decodeSkillOverride(raw any) (skillOverride, error) {
	if raw == nil {
		return skillOverride{}, nil
	}
	switch v := raw.(type) {
	case bool:
		return skillOverride{set: true, disabled: !v}, nil
	case string:
		return skillOverride{set: true, names: splitCSV(v)}, nil
	case []string:
		return skillOverride{set: true, names: cleanStrings(v)}, nil
	case []any:
		names := make([]string, 0, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return skillOverride{}, fmt.Errorf("skill[%d] must be string, got %T", i, item)
			}
			names = append(names, splitCSV(s)...)
		}
		return skillOverride{set: true, names: cleanStrings(names)}, nil
	default:
		return skillOverride{}, fmt.Errorf("skill must be a string, array, or boolean, got %T", raw)
	}
}

func effectiveSkills(def *csubagent.SubagentDefinition, override skillOverride) []string {
	if override.set {
		if override.disabled {
			return nil
		}
		if len(override.names) > 0 {
			return override.names
		}
	}
	if def == nil {
		return nil
	}
	return append([]string(nil), def.Skills...)
}

func effectiveModel(def *csubagent.SubagentDefinition, cfg Config, override string) string {
	if model := strings.TrimSpace(override); model != "" {
		return model
	}
	if def != nil && strings.TrimSpace(def.Model) != "" {
		return def.Model
	}
	return cfg.DefaultModel
}

// effectiveThinking prefers a per-call thinking override over the profile's
// ThinkingLevel. Returns the empty string when neither side set anything, so
// the host falls back to its default policy.
func effectiveThinking(def *csubagent.SubagentDefinition, override string) string {
	if v := strings.TrimSpace(override); v != "" {
		return v
	}
	if def != nil {
		return string(def.ThinkingLevel)
	}
	return ""
}

// effectiveIsolation prefers a per-call override over the profile setting.
// Callers pass the override only when something like `worktree: true` on a
// parallel group means "force every child into an isolated worktree" — they
// pass "" otherwise and the profile decides.
func effectiveIsolation(def *csubagent.SubagentDefinition, override string) string {
	if v := strings.TrimSpace(override); v != "" {
		return v
	}
	if def != nil {
		return def.Isolation
	}
	return ""
}

func effectiveContext(def *csubagent.SubagentDefinition, override string) string {
	if strings.TrimSpace(override) != "" {
		return normalizeContextMode(override)
	}
	if def != nil {
		return normalizeContextMode(def.DefaultContext)
	}
	return ""
}

func normalizeContextMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "fresh":
		return ""
	case "fork":
		return "fork"
	default:
		return strings.TrimSpace(mode)
	}
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
