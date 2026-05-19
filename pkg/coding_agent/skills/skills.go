package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/openmodu/modu/pkg/coding_agent/resource"
	"github.com/openmodu/modu/pkg/utils"
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

// Manager handles skill discovery and loading. All public accessors rediscover
// from disk before reading the skill map, so changes to skill files are picked
// up without restarting the session.
type Manager struct {
	agentDir string
	cwd      string

	mu         sync.RWMutex
	skills     map[string]*Skill
	extraPaths []resource.ResourceRef
}

// NewManager creates a new skill manager.
func NewManager(agentDir, cwd string) *Manager {
	return &Manager{
		agentDir: agentDir,
		cwd:      cwd,
		skills:   make(map[string]*Skill),
	}
}

func (m *Manager) SetExtraPaths(paths []resource.ResourceRef) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.extraPaths = append([]resource.ResourceRef(nil), paths...)
}

// Discover scans the global and project skill directories and atomically
// replaces the in-memory map with the result. Safe to call repeatedly — each
// call reflects the current filesystem state, so removed/edited/added skills
// are picked up.
//
// Discovery rules:
//   - Direct .md files at the skills directory root
//   - Recursive SKILL.md files under subdirectories
//   - Project skills override global skills with the same name
func (m *Manager) Discover() error {
	fresh := make(map[string]*Skill)

	globalDir := filepath.Join(m.agentDir, "skills")
	if err := loadIntoMap(fresh, globalDir, "user"); err != nil && !os.IsNotExist(err) {
		return err
	}

	projectDir := filepath.Join(m.cwd, ".coding_agent", "skills")
	if err := loadIntoMap(fresh, projectDir, "project"); err != nil && !os.IsNotExist(err) {
		return err
	}

	m.mu.RLock()
	extraPaths := append([]resource.ResourceRef(nil), m.extraPaths...)
	m.mu.RUnlock()
	for _, ref := range extraPaths {
		_ = loadPathIntoMap(fresh, ref.Path, ref.Source)
	}

	m.mu.Lock()
	m.skills = fresh
	m.mu.Unlock()
	return nil
}

// Get returns a skill by name. Triggers a Discover() on each call so renamed
// or newly added skills resolve without a session restart.
func (m *Manager) Get(name string) (*Skill, bool) {
	_ = m.Discover()
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.skills[name]
	return s, ok
}

// List returns all discovered skills (re-scanning disk first).
func (m *Manager) List() []*Skill {
	_ = m.Discover()
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*Skill, 0, len(m.skills))
	for _, s := range m.skills {
		result = append(result, s)
	}
	return result
}

// GetDescriptions returns formatted descriptions for system prompt inclusion.
func (m *Manager) GetDescriptions() []string {
	_ = m.Discover()
	m.mu.RLock()
	defer m.mu.RUnlock()
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
	_ = m.Discover()
	m.mu.RLock()
	defer m.mu.RUnlock()
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
	lines = append(lines, "IMPORTANT: Each skill has a <base_dir>. ALL relative paths inside a skill file must be")
	lines = append(lines, "resolved against that <base_dir>. Always use the resulting absolute path in tool calls.")
	lines = append(lines, "Never search for skill scripts with find/glob from the project cwd.")
	lines = append(lines, "")
	lines = append(lines, "<available_skills>")
	for _, s := range visible {
		lines = append(lines, "  <skill>")
		lines = append(lines, fmt.Sprintf("    <name>%s</name>", escapeXML(s.Name)))
		lines = append(lines, fmt.Sprintf("    <description>%s</description>", escapeXML(s.Description)))
		lines = append(lines, fmt.Sprintf("    <location>%s</location>", escapeXML(s.FilePath)))
		lines = append(lines, fmt.Sprintf("    <base_dir>%s</base_dir>", escapeXML(s.BaseDir)))
		lines = append(lines, "  </skill>")
	}
	lines = append(lines, "</available_skills>")

	return strings.Join(lines, "\n")
}

// loadIntoMap is the package-level entry that Discover uses to populate a
// fresh skills map without touching Manager state.
func loadIntoMap(dst map[string]*Skill, dir, source string) error {
	return loadFromDirInternal(dst, dir, source, true, nil, "")
}

func loadPathIntoMap(dst map[string]*Skill, path, source string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return loadIntoMap(dst, path, source)
	}
	if !info.Mode().IsRegular() || !strings.HasSuffix(filepath.Base(path), ".md") {
		return nil
	}
	skill, err := loadSkillFile(path, source)
	if err != nil {
		return err
	}
	if _, exists := dst[skill.Name]; !exists {
		dst[skill.Name] = skill
	}
	return nil
}

// loadFromDirInternal recursively discovers skills.
// At the root level (includeRootFiles=true), any .md file is treated as a skill.
// In subdirectories (includeRootFiles=false), only SKILL.md files are loaded.
func loadFromDirInternal(dst map[string]*Skill, dir, source string, includeRootFiles bool, ignorePatterns []string, rootDir string) error {
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
			_ = loadFromDirInternal(dst, fullPath, source, false, ignorePatterns, rootDir)
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

		skill, err := loadSkillFile(fullPath, source)
		if err != nil {
			continue // Skip malformed skills
		}

		// First registered wins (project overrides global via call order)
		if _, exists := dst[skill.Name]; !exists {
			dst[skill.Name] = skill
		}
	}

	return nil
}

func loadSkillFile(path, source string) (*Skill, error) {
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

	fields, body, ok := utils.ParseFrontmatter(content)
	if ok {
		skill.Content = body
		applyFrontmatter(fields, skill)
	}

	// Replace relative paths that exist on disk with absolute paths,
	// so the LLM never needs to resolve them manually.
	skill.Content = resolveRelativePaths(skill.Content, skillDir)

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

// relativePathRe matches relative path-like tokens: optional "./" prefix,
// at least one path component, and a file extension.
// Examples: scripts/node_1.sh  ./config/settings.json  src/main.go
var relativePathRe = regexp.MustCompile(`(?:\.\/|[a-zA-Z0-9_])[a-zA-Z0-9_./-]*\.[a-zA-Z0-9]+`)

// resolveRelativePaths replaces relative path tokens in content with
// their absolute equivalents when the path exists under baseDir.
func resolveRelativePaths(content, baseDir string) string {
	return relativePathRe.ReplaceAllStringFunc(content, func(token string) string {
		// Already absolute.
		if filepath.IsAbs(token) {
			return token
		}
		abs := filepath.Join(baseDir, token)
		if _, err := os.Stat(abs); err == nil {
			return abs
		}
		return token
	})
}

func applyFrontmatter(fields map[string]string, skill *Skill) {
	for key, value := range fields {
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
