package subagent

import (
	"os"
	"path/filepath"
	"strings"
)

// SubagentDefinition holds the parsed metadata and system prompt for a subagent.
type SubagentDefinition struct {
	Name         string
	Description  string
	Tools        []string // tool names to allow; empty = no tools
	Model        string   // optional model ID override
	SystemPrompt string
	FilePath     string
	Source       string // "user" or "project"
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
			for _, t := range strings.Split(value, ",") {
				t = strings.TrimSpace(t)
				if t != "" {
					def.Tools = append(def.Tools, t)
				}
			}
		case "model":
			def.Model = value
		}
	}
}
