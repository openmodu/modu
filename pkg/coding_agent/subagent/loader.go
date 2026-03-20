package subagent

import (
	"os"
	"path/filepath"
	"strings"
)

// Loader discovers and holds subagent definitions from global and project directories.
type Loader struct {
	definitions map[string]*SubagentDefinition
}

// NewLoader creates an empty Loader.
func NewLoader() *Loader {
	return &Loader{definitions: make(map[string]*SubagentDefinition)}
}

// Discover loads subagent definitions from:
//   - {agentDir}/agents/       (global, "user" source)
//   - {cwd}/.coding_agent/agents/ (project, "project" source — overrides global)
//
// Missing directories are silently skipped.
func (l *Loader) Discover(agentDir, cwd string) {
	l.loadFromDir(filepath.Join(agentDir, "agents"), "user")
	l.loadFromDir(filepath.Join(cwd, ".coding_agent", "agents"), "project")
}

func (l *Loader) loadFromDir(dir, source string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // directory absent or unreadable — skip silently
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		def, err := ParseDefinition(filepath.Join(dir, entry.Name()), source)
		if err != nil {
			continue
		}
		// Later source (project) overwrites earlier (global).
		l.definitions[def.Name] = def
	}
}

// Get returns the definition for the given name, or (nil, false) if not found.
func (l *Loader) Get(name string) (*SubagentDefinition, bool) {
	def, ok := l.definitions[name]
	return def, ok
}

// List returns all discovered definitions.
func (l *Loader) List() []*SubagentDefinition {
	result := make([]*SubagentDefinition, 0, len(l.definitions))
	for _, def := range l.definitions {
		result = append(result, def)
	}
	return result
}

// Count returns the number of discovered definitions.
func (l *Loader) Count() int {
	return len(l.definitions)
}
