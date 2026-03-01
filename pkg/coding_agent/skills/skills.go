package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Skill represents a discovered skill with its metadata and content.
type Skill struct {
	Name                   string            `json:"name"`
	Description            string            `json:"description"`
	Tags                   []string          `json:"tags,omitempty"`
	Metadata               map[string]string `json:"metadata,omitempty"`
	Content                string            `json:"content"`
	FilePath               string            `json:"filePath"`
	BaseDir                string            `json:"baseDir"`
	Source                 string            `json:"source"`
	DisableModelInvocation bool              `json:"disableModelInvocation"`
}

var ignoreFileNames = []string{".gitignore", ".ignore", ".fdignore"}

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
// Discovery rules:
//   - Direct .md files in the skills directory root
//   - Recursive SKILL.md files under subdirectories
func (m *Manager) Discover() error {
	// Global skills
	globalDir := filepath.Join(m.agentDir, "skills")
	if err := m.loadFromDir(globalDir, "user"); err != nil {
		// Non-fatal if dir doesn't exist
		if !os.IsNotExist(err) {
			return err
		}
	}

	// Project skills (override global)
	projectDir := filepath.Join(m.cwd, ".coding_agent", "skills")
	if err := m.loadFromDir(projectDir, "project"); err != nil {
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

// FormatForPrompt returns an XML-formatted listing of all available skills,
// suitable for injection into a system prompt per the Agent Skills spec.
// Skills with DisableModelInvocation=true are excluded (they can only be
// invoked explicitly via /skill:name commands).
func (m *Manager) FormatForPrompt() string {
	var visible []*Skill
	for _, s := range m.skills {
		if !s.DisableModelInvocation {
			visible = append(visible, s)
		}
	}

	if len(visible) == 0 {
		return ""
	}

	var lines []string
	lines = append(lines, "")
	lines = append(lines, "The following skills provide specialized instructions for specific tasks.")
	lines = append(lines, "Use the read tool to load a skill's file when the task matches its description.")
	lines = append(lines, "When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.")
	lines = append(lines, "")
	lines = append(lines, "<available_skills>")
	for _, s := range visible {
		lines = append(lines, "  <skill>")
		lines = append(lines, fmt.Sprintf("    <name>%s</name>", escapeXML(s.Name)))
		lines = append(lines, fmt.Sprintf("    <description>%s</description>", escapeXML(s.Description)))
		lines = append(lines, fmt.Sprintf("    <location>%s</location>", escapeXML(s.FilePath)))
		lines = append(lines, "  </skill>")
	}
	lines = append(lines, "</available_skills>")

	return strings.Join(lines, "\n")
}

func (m *Manager) loadFromDir(dir, source string) error {
	return m.loadFromDirInternal(dir, source, true, nil, "")
}

// loadFromDirInternal recursively discovers skills.
// At the root level (includeRootFiles=true), any .md file is treated as a skill.
// In subdirectories (includeRootFiles=false), only SKILL.md files are loaded.
func (m *Manager) loadFromDirInternal(dir, source string, includeRootFiles bool, ignorePatterns []string, rootDir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	if rootDir == "" {
		rootDir = dir
	}

	// Collect ignore patterns from this directory
	ignorePatterns = append([]string{}, ignorePatterns...) // copy to avoid mutation
	for _, ignoreFileName := range ignoreFileNames {
		ignorePath := filepath.Join(dir, ignoreFileName)
		data, err := os.ReadFile(ignorePath)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			ignorePatterns = append(ignorePatterns, line)
		}
	}

	for _, entry := range entries {
		name := entry.Name()

		// Skip dotfiles
		if strings.HasPrefix(name, ".") {
			continue
		}

		// Skip node_modules
		if name == "node_modules" {
			continue
		}

		fullPath := filepath.Join(dir, name)

		// Check ignore patterns (simple glob matching)
		if isIgnored(name, ignorePatterns) {
			continue
		}

		// Handle symlinks
		info, err := os.Stat(fullPath)
		if err != nil {
			continue // broken symlink or permission error
		}

		if info.IsDir() {
			// Recurse into subdirectories
			_ = m.loadFromDirInternal(fullPath, source, false, ignorePatterns, rootDir)
			continue
		}

		if !info.Mode().IsRegular() {
			continue
		}

		isRootMd := includeRootFiles && strings.HasSuffix(name, ".md")
		isSkillMd := !includeRootFiles && name == "SKILL.md"
		if !isRootMd && !isSkillMd {
			continue
		}

		skill, err := m.loadSkillFile(fullPath, source)
		if err != nil {
			continue // Skip malformed skills
		}

		// First registered wins (project overrides global via call order)
		if _, exists := m.skills[skill.Name]; !exists {
			m.skills[skill.Name] = skill
		}
	}

	return nil
}

func (m *Manager) loadSkillFile(path, source string) (*Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	content := string(data)
	skillDir := filepath.Dir(path)
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	// For SKILL.md, use parent directory name
	if filepath.Base(path) == "SKILL.md" {
		name = filepath.Base(skillDir)
	}

	skill := &Skill{
		Name:     name,
		Content:  content,
		FilePath: path,
		BaseDir:  skillDir,
		Source:   source,
		Metadata: make(map[string]string),
	}

	// Parse YAML frontmatter if present
	if strings.HasPrefix(content, "---\n") || strings.HasPrefix(content, "---\r\n") {
		// Normalize line endings
		normalized := strings.ReplaceAll(content, "\r\n", "\n")
		normalized = strings.ReplaceAll(normalized, "\r", "\n")

		end := strings.Index(normalized[4:], "\n---")
		if end >= 0 {
			frontmatter := normalized[4 : 4+end]
			skill.Content = strings.TrimSpace(normalized[4+end+4:])
			parseFrontmatter(frontmatter, skill)
		}
	}

	// Skills without description are not loaded (per Agent Skills spec)
	if skill.Description == "" {
		// Still allow loading for backward compat with flat .md files
		// that don't have frontmatter — they use their content as the skill
		if filepath.Base(path) == "SKILL.md" {
			return nil, fmt.Errorf("skill %q: missing description", name)
		}
	}

	return skill, nil
}

// isIgnored checks if a filename matches any of the simple ignore patterns.
func isIgnored(name string, patterns []string) bool {
	for _, pattern := range patterns {
		negated := false
		p := pattern
		if strings.HasPrefix(p, "!") {
			negated = true
			p = p[1:]
		}

		matched, err := filepath.Match(p, name)
		if err != nil {
			continue
		}
		if matched {
			if negated {
				return false
			}
			return true
		}
	}
	return false
}

func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
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
		case "disable-model-invocation":
			skill.DisableModelInvocation = value == "true"
		default:
			skill.Metadata[key] = value
		}
	}
}
