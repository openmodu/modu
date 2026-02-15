package skills

import (
	"os"
	"path/filepath"
	"strings"
)

// PromptTemplate represents a loadable prompt template.
type PromptTemplate struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// TemplateManager handles prompt template discovery and expansion.
type TemplateManager struct {
	agentDir  string
	cwd       string
	templates map[string]*PromptTemplate
}

// NewTemplateManager creates a new template manager.
func NewTemplateManager(agentDir, cwd string) *TemplateManager {
	return &TemplateManager{
		agentDir:  agentDir,
		cwd:       cwd,
		templates: make(map[string]*PromptTemplate),
	}
}

// Discover finds and loads all templates from global and project directories.
func (m *TemplateManager) Discover() error {
	// Global templates
	globalDir := filepath.Join(m.agentDir, "prompts")
	if err := m.loadFromDir(globalDir); err != nil && !os.IsNotExist(err) {
		return err
	}

	// Project templates (override global)
	projectDir := filepath.Join(m.cwd, ".coding_agent", "prompts")
	if err := m.loadFromDir(projectDir); err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}

// Expand expands a template invocation like "/templatename args" into the full prompt.
func (m *TemplateManager) Expand(input string) (string, bool) {
	input = strings.TrimSpace(input)
	if !strings.HasPrefix(input, "/") {
		return input, false
	}

	parts := strings.SplitN(input[1:], " ", 2)
	name := parts[0]
	args := ""
	if len(parts) > 1 {
		args = parts[1]
	}

	tmpl, ok := m.templates[name]
	if !ok {
		return input, false
	}

	content := tmpl.Content
	// Simple placeholder replacement
	content = strings.ReplaceAll(content, "{{args}}", args)
	content = strings.ReplaceAll(content, "{{ args }}", args)

	return content, true
}

// List returns all available template names.
func (m *TemplateManager) List() []string {
	names := make([]string, 0, len(m.templates))
	for name := range m.templates {
		names = append(names, name)
	}
	return names
}

func (m *TemplateManager) loadFromDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := filepath.Ext(entry.Name())
		if ext != ".md" && ext != ".txt" {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		name := strings.TrimSuffix(entry.Name(), ext)
		m.templates[name] = &PromptTemplate{
			Name:    name,
			Content: string(data),
		}
	}

	return nil
}
