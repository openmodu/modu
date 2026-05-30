// Package skills provides local skill discovery and loading.
//
// Skills are Markdown files (with optional YAML frontmatter) discovered from
// configurable roots. Discovered skill metadata is injected into the agent
// system prompt so the model knows what specialized instructions are available.
package skills

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/openmodu/modu/pkg/mdloader"
	"github.com/openmodu/modu/pkg/utils"
)

const (
	// MaxNameLength caps a skill name; longer names are rejected.
	MaxNameLength = 64
	// MaxDescriptionLength caps a skill description; longer ones are rejected.
	MaxDescriptionLength = 1024
	// MaxMetadataBytes caps the prefix read during discovery. Skill bodies are
	// loaded separately when a skill is invoked.
	MaxMetadataBytes = 64 * 1024
)

// namePattern validates skill names: alphanumeric segments joined by hyphens.
var namePattern = regexp.MustCompile(`^[a-zA-Z0-9]+(-[a-zA-Z0-9]+)*$`)

var ignoreFileNames = []string{".gitignore", ".ignore", ".fdignore"}

// PathRef points to an extra skill file or directory to discover, tagged with
// a source label for provenance.
type PathRef struct {
	Path   string
	Source string
}

// Skill represents a discovered skill. Discovery populates metadata and path
// fields only; Content is loaded lazily by Manager.Get.
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

// Manager handles skill discovery and loading. All public accessors rediscover
// from disk before reading the skill map, so changes to skill files are picked
// up without restarting the session. The discovery skeleton (roots, extra
// paths, locking, re-scan) is provided by the embedded mdloader.Manager; this
// type adds skill-specific lazy content loading and prompt formatting.
type Manager struct {
	*mdloader.Manager[Skill]
}

// NewManager creates a new skill manager. Global skills live under
// {agentDir}/skills and project skills under {cwd}/.coding_agent/skills.
// Project skills are scanned first so they win over global ones of the same
// name (the parser keeps the first registration).
func NewManager(agentDir, cwd string) *Manager {
	roots := []mdloader.Ref{
		{Path: filepath.Join(cwd, ".coding_agent", "skills"), Source: "project"},
		{Path: filepath.Join(agentDir, "skills"), Source: "user"},
	}
	return &Manager{mdloader.New(roots, skillParser{})}
}

// SetExtraPaths registers additional skill files or directories (e.g. from
// resource packages) to include in discovery.
func (m *Manager) SetExtraPaths(paths []PathRef) {
	refs := make([]mdloader.Ref, len(paths))
	for i, p := range paths {
		refs[i] = mdloader.Ref{Path: p.Path, Source: p.Source}
	}
	m.SetExtraRefs(refs)
}

// skillParser plugs skill-specific scanning into the mdloader skeleton.
// Both methods keep the first registration of a name, so earlier roots
// (project) win over later ones (global).
type skillParser struct{}

func (skillParser) ParseDir(dst map[string]*Skill, dir, source string) error {
	return loadIntoMap(dst, dir, source)
}

func (skillParser) ParsePath(dst map[string]*Skill, path, source string) error {
	return loadPathIntoMap(dst, path, source)
}

// Get returns a skill by name. Triggers a Discover() on each call so renamed
// or newly added skills resolve without a session restart.
func (m *Manager) Get(name string) (*Skill, bool) {
	stored, ok := m.Lookup(name)
	if !ok {
		return nil, false
	}
	s := cloneSkill(stored)
	if err := loadSkillContent(s); err != nil {
		slog.Warn("failed to load skill content", "source", s.Source, "path", s.FilePath, "name", s.Name, "error", err)
		return nil, false
	}
	return s, true
}

// List returns all discovered skills (re-scanning disk first).
func (m *Manager) List() []*Skill {
	stored := m.Snapshot()
	result := make([]*Skill, 0, len(stored))
	for _, s := range stored {
		result = append(result, cloneSkill(s))
	}
	return result
}

// GetDescriptions returns formatted descriptions for system prompt inclusion.
func (m *Manager) GetDescriptions() []string {
	var descs []string
	for _, s := range m.Snapshot() {
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
	for _, s := range m.Snapshot() {
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
	skill, err := loadSkillMetadataFile(path, source)
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

		skill, err := loadSkillMetadataFile(fullPath, source)
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

func loadSkillMetadataFile(path, source string) (*Skill, error) {
	fields, ok, err := readSkillMetadataFields(path)
	if err != nil {
		return nil, err
	}

	skillDir := filepath.Dir(path)
	isSkillMd := filepath.Base(path) == "SKILL.md"
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	// For SKILL.md, use parent directory name
	if isSkillMd {
		name = filepath.Base(skillDir)
	}

	skill := &Skill{
		Name:     name,
		FilePath: path,
		BaseDir:  skillDir,
		Source:   source,
		Metadata: make(map[string]string),
	}

	if ok {
		applyFrontmatter(fields, skill)
	}

	// SKILL.md skills must declare a description per the Agent Skills spec.
	// Flat .md files without frontmatter remain valid for backward compat.
	if skill.Description == "" && isSkillMd {
		return nil, fmt.Errorf("skill %q: missing description", name)
	}

	if err := validateSkill(skill); err != nil {
		slog.Warn("invalid skill", "source", source, "path", path, "name", skill.Name, "error", err)
		return nil, err
	}

	return skill, nil
}

func readSkillMetadataFields(path string) (map[string]string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, MaxMetadataBytes))
	if err != nil {
		return nil, false, err
	}
	fields, _, ok := utils.ParseFrontmatter(string(data))
	return fields, ok, nil
}

func loadSkillContent(skill *Skill) error {
	if skill == nil {
		return fmt.Errorf("skill is nil")
	}
	data, err := os.ReadFile(skill.FilePath)
	if err != nil {
		return err
	}
	content := string(data)
	if _, body, ok := utils.ParseFrontmatter(content); ok {
		content = body
	}
	// Replace relative paths that exist on disk with absolute paths,
	// so the LLM never needs to resolve them manually.
	skill.Content = resolveRelativePaths(content, skill.BaseDir)
	return nil
}

func cloneSkill(s *Skill) *Skill {
	if s == nil {
		return nil
	}
	clone := *s
	if len(s.Tags) > 0 {
		clone.Tags = append([]string(nil), s.Tags...)
	}
	if len(s.Metadata) > 0 {
		clone.Metadata = make(map[string]string, len(s.Metadata))
		for k, v := range s.Metadata {
			clone.Metadata[k] = v
		}
	}
	return &clone
}

// validateSkill enforces name and description limits. An empty description is
// tolerated here (flat .md backward-compat); the SKILL.md description-required
// rule is checked separately in loadSkillMetadataFile.
func validateSkill(s *Skill) error {
	if s.Name == "" {
		return fmt.Errorf("name is required")
	}
	if len(s.Name) > MaxNameLength {
		return fmt.Errorf("name exceeds %d characters", MaxNameLength)
	}
	if !namePattern.MatchString(s.Name) {
		return fmt.Errorf("name %q must be alphanumeric with hyphens", s.Name)
	}
	if len(s.Description) > MaxDescriptionLength {
		return fmt.Errorf("description exceeds %d characters", MaxDescriptionLength)
	}
	return nil
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
