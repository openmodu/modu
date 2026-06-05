package prompts

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/openmodu/modu/pkg/coding_agent/foundation/resource"
	"github.com/openmodu/modu/pkg/mdloader"
	"github.com/openmodu/modu/pkg/utils"
)

var (
	positionalArgRe = regexp.MustCompile(`\$(\d+)`)
	shellSubRe      = regexp.MustCompile("!`([^`]*)`")
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

// Expand substitutes a prompt template's argument placeholders with the user
// input. It supports both the legacy `{{input}}`/`{{args}}` forms and the
// Claude Code custom-command forms: `$ARGUMENTS` (all args) and positional
// `$1`, `$2`, ... (whitespace-split). When the template contains no
// placeholder at all, non-empty input is appended so bare templates still
// receive the user's text.
func (t *Template) Expand(input string) string {
	input = strings.TrimSpace(input)
	text := t.Content

	hasPlaceholder := false

	if strings.Contains(text, "$ARGUMENTS") {
		hasPlaceholder = true
		text = strings.ReplaceAll(text, "$ARGUMENTS", input)
	}

	if indices := positionalIndices(text); len(indices) > 0 {
		hasPlaceholder = true
		args := strings.Fields(input)
		// Replace larger indices first so `$1` does not match the prefix of
		// `$12`.
		for _, n := range indices {
			val := ""
			if n >= 1 && n <= len(args) {
				val = args[n-1]
			}
			text = strings.ReplaceAll(text, "$"+strconv.Itoa(n), val)
		}
	}

	if strings.Contains(text, "{{input}}") || strings.Contains(text, "{{args}}") {
		hasPlaceholder = true
		text = strings.ReplaceAll(text, "{{input}}", input)
		text = strings.ReplaceAll(text, "{{args}}", input)
	}

	if !hasPlaceholder && input != "" {
		text = strings.TrimSpace(text) + "\n\n" + input
	}
	return strings.TrimSpace(text)
}

// positionalIndices returns the distinct positional argument numbers ($1, $2,
// ...) referenced in text, sorted descending so callers can substitute longer
// tokens before their prefixes.
func positionalIndices(text string) []int {
	matches := positionalArgRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[int]bool, len(matches))
	for _, m := range matches {
		n, err := strconv.Atoi(m[1])
		if err == nil {
			seen[n] = true
		}
	}
	out := make([]int, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(out)))
	return out
}

// SubstituteShell replaces Claude Code-style inline shell substitutions of the
// form !`command` with the command's output, using run to execute each
// command. A nil run leaves the text unchanged; a failing command is replaced
// with a short inline error marker so the prompt stays readable.
func SubstituteShell(text string, run func(command string) (string, error)) string {
	if run == nil {
		return text
	}
	return shellSubRe.ReplaceAllStringFunc(text, func(match string) string {
		sub := shellSubRe.FindStringSubmatch(match)
		command := strings.TrimSpace(sub[1])
		if command == "" {
			return match
		}
		out, err := run(command)
		if err != nil {
			return fmt.Sprintf("[command failed: %v]", err)
		}
		return strings.TrimRight(out, "\n")
	})
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
