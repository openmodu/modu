package subagent

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/utils"
)

// SubagentDefinition holds the parsed metadata and system prompt for a subagent.
type SubagentDefinition struct {
	Name              string
	Description       string
	Tools             []string // tool names to allow; empty = no tools
	DisallowedTools   []string // tool names to remove after allow-list resolution
	HarnessBlockTools []string
	Skills            []string
	MemoryScope       string
	PermissionMode    string
	Background        bool
	Effort            string
	Isolation         string
	Model             string // optional model ID override
	ThinkingLevel     agent.ThinkingLevel
	MaxTurns          int
	SystemPrompt      string
	FilePath          string
	Source            string // "user" or "project"
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

	fields, body, ok := utils.ParseFrontmatter(content)
	def.SystemPrompt = body
	if ok {
		applyFrontmatter(fields, def)
	}

	return def, nil
}

func applyFrontmatter(fields map[string]string, def *SubagentDefinition) {
	for key, value := range fields {
		switch key {
		case "name":
			def.Name = value
		case "description":
			def.Description = value
		case "tools":
			def.Tools = appendCSV(def.Tools, value)
		case "disallowed_tools", "disallowed-tools":
			def.DisallowedTools = appendCSV(def.DisallowedTools, value)
		case "harness_block_tools", "harness-block-tools":
			def.HarnessBlockTools = appendCSV(def.HarnessBlockTools, value)
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
