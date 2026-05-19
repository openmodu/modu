package prompts

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/openmodu/modu/pkg/coding_agent/resource"
	"github.com/openmodu/modu/pkg/utils"
)

// Template represents a prompt template loaded from disk.
type Template struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Content     string            `json:"content"`
	FilePath    string            `json:"filePath"`
	Source      string            `json:"source"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// Expand applies the minimal built-in variables supported by prompt templates.
func (t *Template) Expand(input string) string {
	input = strings.TrimSpace(input)
	text := t.Content
	text = strings.ReplaceAll(text, "{{input}}", input)
	text = strings.ReplaceAll(text, "{{args}}", input)
	if !strings.Contains(t.Content, "{{input}}") && !strings.Contains(t.Content, "{{args}}") && input != "" {
		text = strings.TrimSpace(text) + "\n\n" + input
	}
	return strings.TrimSpace(text)
}

// Manager discovers prompt templates from global, project, and package paths.
type Manager struct {
	agentDir string
	cwd      string

	mu         sync.RWMutex
	extraPaths []resource.ResourceRef
	templates  map[string]*Template
}

func NewManager(agentDir, cwd string) *Manager {
	return &Manager{
		agentDir:  agentDir,
		cwd:       cwd,
		templates: make(map[string]*Template),
	}
}

func (m *Manager) SetExtraPaths(paths []resource.ResourceRef) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.extraPaths = append([]resource.ResourceRef(nil), paths...)
}

func (m *Manager) Discover() error {
	fresh := make(map[string]*Template)

	if err := loadIntoMap(fresh, filepath.Join(m.agentDir, "prompts"), "user"); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := loadIntoMap(fresh, filepath.Join(m.cwd, ".coding_agent", "prompts"), "project"); err != nil && !os.IsNotExist(err) {
		return err
	}

	m.mu.RLock()
	extraPaths := append([]resource.ResourceRef(nil), m.extraPaths...)
	m.mu.RUnlock()
	for _, ref := range extraPaths {
		_ = loadPathIntoMap(fresh, ref.Path, ref.Source)
	}

	m.mu.Lock()
	m.templates = fresh
	m.mu.Unlock()
	return nil
}

func (m *Manager) Get(name string) (*Template, bool) {
	_ = m.Discover()
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.templates[name]
	return t, ok
}

func (m *Manager) List() []*Template {
	_ = m.Discover()
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*Template, 0, len(m.templates))
	for _, t := range m.templates {
		result = append(result, t)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func loadIntoMap(dst map[string]*Template, dir, source string) error {
	return loadPathIntoMap(dst, dir, source)
}

func loadPathIntoMap(dst map[string]*Template, path, source string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			name := d.Name()
			if d.IsDir() {
				if strings.HasPrefix(name, ".") || name == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(name, ".md") {
				return nil
			}
			template, err := loadTemplateFile(p, source)
			if err == nil {
				dst[template.Name] = template
			}
			return nil
		})
	}
	if strings.HasSuffix(filepath.Base(path), ".md") {
		template, err := loadTemplateFile(path, source)
		if err != nil {
			return err
		}
		dst[template.Name] = template
	}
	return nil
}

func loadTemplateFile(path, source string) (*Template, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	content := string(data)
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	template := &Template{
		Name:     name,
		Content:  content,
		FilePath: path,
		Source:   source,
		Metadata: make(map[string]string),
	}
	fields, body, ok := utils.ParseFrontmatter(content)
	if ok {
		template.Content = body
		for key, value := range fields {
			switch key {
			case "name":
				template.Name = strings.TrimSpace(value)
			case "description":
				template.Description = strings.TrimSpace(value)
			default:
				template.Metadata[key] = value
			}
		}
	}
	if template.Name == "" {
		return nil, fmt.Errorf("prompt template %q: missing name", path)
	}
	return template, nil
}
