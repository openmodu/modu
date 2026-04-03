package resource

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Loader handles unified loading of extensions, skills, prompts, and context files.
type Loader struct {
	agentDir        string
	cwd             string
	projectRootOnce sync.Once
	projectRoot     string
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
	seen := make(map[string]struct{})

	files = append(files, l.loadContextFilesFromDirs(l.contextSearchDirs(), seen)...)
	files = append(files, l.loadGlobalContextFiles(seen)...)
	return files
}

// LoadContextFilesForPath discovers context files relevant to a specific file path.
// This lets callers lazily inject deeper nested instructions when the agent first
// touches a file below the current working directory.
func (l *Loader) LoadContextFilesForPath(targetPath string) []ContextFile {
	var files []ContextFile
	seen := make(map[string]struct{})
	files = append(files, l.loadContextFilesFromDirs(l.contextSearchDirsForPath(targetPath), seen)...)
	files = append(files, l.loadGlobalContextFiles(seen)...)
	return files
}

func (l *Loader) loadContextFilesFromDirs(dirs []string, seen map[string]struct{}) []ContextFile {
	var files []ContextFile
	for _, dir := range dirs {
		for _, name := range []string{"AGENTS.md", ".agents.md", "CLAUDE.md", ".claude.md"} {
			path := filepath.Join(dir, name)
			if _, ok := seen[path]; ok {
				continue
			}
			content, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			seen[path] = struct{}{}
			files = append(files, ContextFile{
				Name:    relativeContextName(l.cwd, path),
				Path:    path,
				Content: strings.TrimSpace(string(content)),
			})
		}
	}
	return files
}

func (l *Loader) loadGlobalContextFiles(seen map[string]struct{}) []ContextFile {
	var files []ContextFile
	for _, name := range []string{"context.md"} {
		path := filepath.Join(l.agentDir, name)
		if _, ok := seen[path]; ok {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		seen[path] = struct{}{}
		files = append(files, ContextFile{
			Name:    "global/" + name,
			Path:    path,
			Content: strings.TrimSpace(string(content)),
		})
	}
	return files
}

func (l *Loader) contextSearchDirs() []string {
	root := l.findProjectRoot()
	if root == "" {
		root = l.cwd
	}

	return buildPathChain(root, l.cwd)
}

func (l *Loader) contextSearchDirsForPath(targetPath string) []string {
	root := l.findProjectRoot()
	if root == "" {
		root = l.cwd
	}

	dir := targetPath
	if info, err := os.Stat(targetPath); err == nil && !info.IsDir() {
		dir = filepath.Dir(targetPath)
	}
	return buildPathChain(root, dir)
}

func buildPathChain(root, target string) []string {
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return []string{target}
	}

	var dirs []string
	dirs = append(dirs, root)
	if rel == "." {
		return dirs
	}

	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		dirs = append(dirs, current)
	}
	return dirs
}

func (l *Loader) findProjectRoot() string {
	l.projectRootOnce.Do(func() {
		cmd := exec.Command("git", "rev-parse", "--show-toplevel")
		cmd.Dir = l.cwd
		out, err := cmd.Output()
		if err == nil {
			root := strings.TrimSpace(string(out))
			if root != "" {
				l.projectRoot = root
				return
			}
		}

		// Walk upward looking for a .git directory. candidates are already
		// in deepest-first order so no sort is needed.
		current := l.cwd
		for {
			if info, err := os.Stat(filepath.Join(current, ".git")); err == nil && info != nil {
				l.projectRoot = current
				return
			}
			parent := filepath.Dir(current)
			if parent == current {
				break
			}
			current = parent
		}
		l.projectRoot = l.cwd
	})
	return l.projectRoot
}

func relativeContextName(base, path string) string {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return filepath.Base(path)
	}
	return rel
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
		filepath.Join(l.agentDir, "plans"),
		filepath.Join(l.agentDir, "skills"),
		filepath.Join(l.agentDir, "agents"),
		filepath.Join(l.agentDir, "prompts"),
		filepath.Join(l.agentDir, "tool-results"),
		filepath.Join(l.agentDir, "worktrees"),
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
