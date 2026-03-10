package coding_agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/crosszan/modu/pkg/agent"
)

const defaultSystemPrompt = `You are a coding assistant that helps users with software development tasks. You have access to tools for reading, writing, editing files, running bash commands, and searching code.

Guidelines:
- Read files before modifying them to understand existing code
- Use edit for precise modifications instead of rewriting entire files
- Run tests after making changes to verify correctness
- Respect existing code style and conventions
- Explain your changes when relevant`

// SystemPromptBuilder constructs the system prompt from multiple sources.
type SystemPromptBuilder struct {
	customPrompt  string
	tools         []agent.AgentTool
	contextFiles  []string
	skillsPrompt  string
	appendPrompts []string
	cwd           string
}

// NewSystemPromptBuilder creates a new system prompt builder.
func NewSystemPromptBuilder(cwd string) *SystemPromptBuilder {
	return &SystemPromptBuilder{cwd: cwd}
}

// SetCustomPrompt sets a custom base prompt (replaces the default).
func (b *SystemPromptBuilder) SetCustomPrompt(prompt string) *SystemPromptBuilder {
	b.customPrompt = prompt
	return b
}

// SetTools sets the active tools whose descriptions will be included.
func (b *SystemPromptBuilder) SetTools(tools []agent.AgentTool) *SystemPromptBuilder {
	b.tools = tools
	return b
}

// AddContextFile adds a context file path to be loaded.
func (b *SystemPromptBuilder) AddContextFile(path string) *SystemPromptBuilder {
	b.contextFiles = append(b.contextFiles, path)
	return b
}

// SetSkillsPrompt sets the pre-formatted skills prompt (XML format per Agent Skills spec).
func (b *SystemPromptBuilder) SetSkillsPrompt(prompt string) *SystemPromptBuilder {
	b.skillsPrompt = prompt
	return b
}

// AppendPrompt adds additional prompt text from settings or extensions.
func (b *SystemPromptBuilder) AppendPrompt(prompt string) *SystemPromptBuilder {
	b.appendPrompts = append(b.appendPrompts, prompt)
	return b
}

// Build constructs the final system prompt.
func (b *SystemPromptBuilder) Build() string {
	var parts []string

	// 1. Base prompt
	basePrompt := b.customPrompt
	if basePrompt == "" {
		basePrompt = defaultSystemPrompt
	}
	parts = append(parts, basePrompt)

	// 2. Tool descriptions
	if len(b.tools) > 0 {
		var toolDescs []string
		toolDescs = append(toolDescs, "# Available Tools")
		for _, tool := range b.tools {
			toolDescs = append(toolDescs, fmt.Sprintf("## %s\n%s", tool.Name(), tool.Description()))
		}
		parts = append(parts, strings.Join(toolDescs, "\n\n"))
	}

	// 3. Context files (AGENTS.md, .agents.md, etc.)
	for _, path := range b.contextFiles {
		content := b.loadContextFile(path)
		if content != "" {
			parts = append(parts, fmt.Sprintf("# Context: %s\n%s", filepath.Base(path), content))
		}
	}

	// Auto-discover standard context files
	bootstrapFiles := []string{
		"AGENTS.md",
		"SOUL.md",
		"USER.md",
		"IDENTITY.md",
		".agents.md",
		"CLAUDE.md",
		".claude.md",
	}

	for _, name := range bootstrapFiles {
		path := filepath.Join(b.cwd, name)
		content := b.loadContextFile(path)
		if content != "" {
			parts = append(parts, fmt.Sprintf("## %s\n\n%s", name, content))
		}
	}

	// 4. Skill descriptions (XML format per Agent Skills spec)
	if b.skillsPrompt != "" {
		parts = append(parts, b.skillsPrompt)
	}

	// 5. Append prompts
	for _, p := range b.appendPrompts {
		parts = append(parts, p)
	}

	// 6. Environment info
	envInfo := fmt.Sprintf("# Environment\n- Current date: %s\n- Working directory: %s",
		time.Now().Format("2006-01-02"),
		b.cwd)
	parts = append(parts, envInfo)

	return strings.Join(parts, "\n\n---\n\n")
}

func (b *SystemPromptBuilder) loadContextFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
