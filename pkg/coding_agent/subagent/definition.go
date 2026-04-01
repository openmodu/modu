package subagent

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/openmodu/modu/pkg/agent"
)

// SubagentDefinition holds the parsed metadata and system prompt for a subagent.
type SubagentDefinition struct {
	Name            string
	Description     string
	Tools           []string // tool names to allow; empty = no tools
	DisallowedTools []string // tool names to remove after allow-list resolution
	Skills          []string
	MemoryScope     string
	PermissionMode  string
	Background      bool
	Effort          string
	Isolation       string
	Model           string // optional model ID override
	ThinkingLevel   agent.ThinkingLevel
	MaxTurns        int
	SystemPrompt    string
	FilePath        string
	Source          string // "user" or "project"
}

// ParseDefinition reads a markdown file and parses its YAML frontmatter into a
// SubagentDefinition. The file body becomes the system prompt.
func ParseDefinition(path, source string) (*SubagentDefinition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	content := string(data)
	name := strings.TrimSuffix(filepath.Base(path), ".md")

	def := &SubagentDefinition{
		Name:     name,
		FilePath: path,
		Source:   source,
	}

	// Parse YAML frontmatter if present.
	if strings.HasPrefix(content, "---\n") || strings.HasPrefix(content, "---\r\n") {
		normalized := strings.ReplaceAll(content, "\r\n", "\n")
		normalized = strings.ReplaceAll(normalized, "\r", "\n")
		end := strings.Index(normalized[4:], "\n---")
		if end >= 0 {
			frontmatter := normalized[4 : 4+end]
			def.SystemPrompt = strings.TrimSpace(normalized[4+end+4:])
			parseFrontmatter(frontmatter, def)
		}
	} else {
		def.SystemPrompt = strings.TrimSpace(content)
	}

	return def, nil
}

func parseFrontmatter(fm string, def *SubagentDefinition) {
	for _, line := range strings.Split(fm, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "name":
			def.Name = value
		case "description":
			def.Description = value
		case "tools":
			def.Tools = appendCSV(def.Tools, value)
		case "disallowed_tools", "disallowed-tools":
			def.DisallowedTools = appendCSV(def.DisallowedTools, value)
		case "skills":
			def.Skills = appendCSV(def.Skills, value)
		case "memory", "memory_scope", "memory-scope":
			def.MemoryScope = value
		case "permission_mode", "permission-mode":
			def.PermissionMode = value
		case "background":
			def.Background = strings.EqualFold(value, "true")
		case "effort":
			def.Effort = value
		case "isolation":
			def.Isolation = value
		case "model":
			def.Model = value
		case "thinking", "thinking_level", "thinking-level":
			def.ThinkingLevel = agent.ThinkingLevel(value)
		case "max_turns", "max-turns":
			if n, err := strconv.Atoi(value); err == nil && n > 0 {
				def.MaxTurns = n
			}
		}
	}
}

func appendCSV(dst []string, value string) []string {
	for _, t := range strings.Split(value, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			dst = append(dst, t)
		}
	}
	return dst
}
