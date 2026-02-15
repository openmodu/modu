package skills

import (
	"os"
	"path/filepath"
	"strings"
)

// Skill represents a discovered skill with its metadata and content.
type Skill struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Tags        []string          `json:"tags,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Content     string            `json:"content"`
	FilePath    string            `json:"filePath"`
}

// Manager handles skill discovery and loading.
type Manager struct {
	agentDir string
	cwd      string
	skills   map[string]*Skill
}

// NewManager creates a new skill manager.
func NewManager(agentDir, cwd string) *Manager {
	return &Manager{
		agentDir: agentDir,
		cwd:      cwd,
		skills:   make(map[string]*Skill),
	}
}

// Discover finds and loads all skills from global and project directories.
func (m *Manager) Discover() error {
	// Global skills
	globalDir := filepath.Join(m.agentDir, "skills")
	if err := m.loadFromDir(globalDir); err != nil {
		// Non-fatal if dir doesn't exist
		if !os.IsNotExist(err) {
			return err
		}
	}

	// Project skills (override global)
	projectDir := filepath.Join(m.cwd, ".coding_agent", "skills")
	if err := m.loadFromDir(projectDir); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	}

	return nil
}

// Get returns a skill by name.
func (m *Manager) Get(name string) (*Skill, bool) {
	s, ok := m.skills[name]
	return s, ok
}

// List returns all discovered skills.
func (m *Manager) List() []*Skill {
	result := make([]*Skill, 0, len(m.skills))
	for _, s := range m.skills {
		result = append(result, s)
	}
	return result
}

// GetDescriptions returns formatted descriptions for system prompt inclusion.
func (m *Manager) GetDescriptions() []string {
	var descs []string
	for _, s := range m.skills {
		desc := "- /" + s.Name
		if s.Description != "" {
			desc += ": " + s.Description
		}
		descs = append(descs, desc)
	}
	return descs
}

func (m *Manager) loadFromDir(dir string) error {
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
		skill, err := m.loadSkillFile(path)
		if err != nil {
			continue // Skip malformed skills
		}

		m.skills[skill.Name] = skill
	}

	return nil
}

func (m *Manager) loadSkillFile(path string) (*Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	content := string(data)
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	skill := &Skill{
		Name:     name,
		Content:  content,
		FilePath: path,
		Metadata: make(map[string]string),
	}

	// Parse YAML frontmatter if present
	if strings.HasPrefix(content, "---\n") {
		end := strings.Index(content[4:], "\n---")
		if end >= 0 {
			frontmatter := content[4 : 4+end]
			skill.Content = strings.TrimSpace(content[4+end+4:])
			parseFrontmatter(frontmatter, skill)
		}
	}

	return skill, nil
}

func parseFrontmatter(fm string, skill *Skill) {
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
			skill.Name = value
		case "description":
			skill.Description = value
		case "tags":
			skill.Tags = strings.Split(value, ",")
			for i := range skill.Tags {
				skill.Tags[i] = strings.TrimSpace(skill.Tags[i])
			}
		default:
			skill.Metadata[key] = value
		}
	}
}
