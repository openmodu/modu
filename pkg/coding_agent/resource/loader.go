package resource

import (
	"os"
	"path/filepath"
	"strings"
)

// Loader handles unified loading of extensions, skills, prompts, and context files.
type Loader struct {
	agentDir string
	cwd      string
}

// NewLoader creates a new resource loader.
func NewLoader(agentDir, cwd string) *Loader {
	return &Loader{
		agentDir: agentDir,
		cwd:      cwd,
	}
}

// ContextFile represents a loaded context file.
type ContextFile struct {
	Name    string
	Path    string
	Content string
}

// LoadContextFiles discovers and loads all context files
// (AGENTS.md, .agents.md, CLAUDE.md, .claude.md) from the project directory.
func (l *Loader) LoadContextFiles() []ContextFile {
	var files []ContextFile

	names := []string{"AGENTS.md", ".agents.md", "CLAUDE.md", ".claude.md"}
	for _, name := range names {
		path := filepath.Join(l.cwd, name)
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		files = append(files, ContextFile{
			Name:    name,
			Path:    path,
			Content: strings.TrimSpace(string(content)),
		})
	}

	// Also check global context files
	for _, name := range []string{"context.md"} {
		path := filepath.Join(l.agentDir, name)
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		files = append(files, ContextFile{
			Name:    "global/" + name,
			Path:    path,
			Content: strings.TrimSpace(string(content)),
		})
	}

	return files
}

// AgentDir returns the agent configuration directory.
func (l *Loader) AgentDir() string {
	return l.agentDir
}

// Cwd returns the current working directory.
func (l *Loader) Cwd() string {
	return l.cwd
}

// EnsureAgentDir creates the agent directory structure if it doesn't exist.
func (l *Loader) EnsureAgentDir() error {
	dirs := []string{
		l.agentDir,
		filepath.Join(l.agentDir, "sessions"),
		filepath.Join(l.agentDir, "skills"),
		filepath.Join(l.agentDir, "prompts"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	return nil
}

// DefaultAgentDir returns the default agent directory path (~/.coding_agent/).
func DefaultAgentDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".coding_agent"
	}
	return filepath.Join(home, ".coding_agent")
}
