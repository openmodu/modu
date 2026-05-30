package prompts

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/openmodu/modu/pkg/coding_agent/foundation/resource"
	"github.com/openmodu/modu/pkg/mdloader"
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
// The discovery skeleton (roots, extra paths, locking, re-scan) is provided by
// the embedded mdloader.Manager; this type adds the prompt-specific accessors.
type Manager struct {
	*mdloader.Manager[Template]
}

// NewManager creates a prompt-template manager. User templates live under
// {agentDir}/prompts and project templates under {cwd}/.coding_agent/prompts.
// Project templates are scanned after user ones so they win on name conflicts
// (the parser overwrites).
func NewManager(agentDir, cwd string) *Manager {
	roots := []mdloader.Ref{
		{Path: filepath.Join(agentDir, "prompts"), Source: "user"},
		{Path: filepath.Join(cwd, ".coding_agent", "prompts"), Source: "project"},
	}
	return &Manager{mdloader.New(roots, promptParser{})}
}

// SetExtraPaths registers additional template files or directories (e.g. from
// resource packages) to include in discovery.
func (m *Manager) SetExtraPaths(paths []resource.ResourceRef) {
	refs := make([]mdloader.Ref, len(paths))
	for i, p := range paths {
		refs[i] = mdloader.Ref{Path: p.Path, Source: p.Source}
	}
	m.SetExtraRefs(refs)
}

func (m *Manager) Get(name string) (*Template, bool) {
	return m.Lookup(name)
}

func (m *Manager) List() []*Template {
	result := m.Snapshot()
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

// promptParser plugs template scanning into the mdloader skeleton. Both methods
// overwrite on name conflict, so later roots (project) and extra paths win.
type promptParser struct{}

func (promptParser) ParseDir(dst map[string]*Template, dir, source string) error {
	return loadPathIntoMap(dst, dir, source)
}

func (promptParser) ParsePath(dst map[string]*Template, path, source string) error {
	return loadPathIntoMap(dst, path, source)
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
