package coding_agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	worktreetool "github.com/openmodu/modu/pkg/coding_agent/tools/worktree"
)

// WorktreeStatus describes the current isolated worktree lifecycle state.
type WorktreeStatus struct {
	Active      bool
	Path        string
	OriginalCwd string
	Cwd         string
	Exists      bool
}

// WorktreeInfo describes one managed worktree under the session agent dir.
type WorktreeInfo struct {
	Path   string
	Active bool
	Exists bool
}

// WorktreeDiff describes the current active worktree changes.
type WorktreeDiff struct {
	Path       string
	Stat       string
	NameStatus string
	Patch      string
}

// worktreeController owns the isolated-worktree state (active path, the cwd to
// restore on exit, and their lock) and drives the session through s. It
// implements worktree.WorktreeManager directly, so the worktree tools need no
// separate adapter.
type worktreeController struct {
	s    *CodingSession
	mu   sync.Mutex
	path string
	orig string
}

func newWorktreeController(s *CodingSession) *worktreeController { return &worktreeController{s: s} }

// ActiveWorktree returns the currently active isolated worktree path, if any.
func (c *worktreeController) ActiveWorktree() string {
	if c == nil || c.s == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.path
}

func (c *worktreeController) status() WorktreeStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	status := WorktreeStatus{
		Active:      c.path != "",
		Path:        c.path,
		OriginalCwd: c.orig,
		Cwd:         c.s.cwd,
	}
	if status.Path != "" {
		if _, err := os.Stat(status.Path); err == nil {
			status.Exists = true
		}
	}
	return status
}

func (c *worktreeController) listManaged() []WorktreeInfo {
	c.mu.Lock()
	activePath := c.path
	c.mu.Unlock()

	dir := filepath.Join(c.s.agentDir, "worktrees")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if activePath == "" {
			return nil
		}
		return []WorktreeInfo{{Path: activePath, Active: true, Exists: pathExists(activePath)}}
	}

	seen := make(map[string]struct{}, len(entries)+1)
	out := make([]WorktreeInfo, 0, len(entries)+1)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		seen[path] = struct{}{}
		out = append(out, WorktreeInfo{Path: path, Active: path == activePath, Exists: true})
	}
	if activePath != "" {
		if _, ok := seen[activePath]; !ok {
			out = append(out, WorktreeInfo{Path: activePath, Active: true, Exists: pathExists(activePath)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Active != out[j].Active {
			return out[i].Active
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func (c *worktreeController) cleanup() ([]WorktreeInfo, error) {
	worktrees := c.listManaged()
	removed := make([]WorktreeInfo, 0, len(worktrees))
	for _, wt := range worktrees {
		if wt.Active || !wt.Exists {
			continue
		}
		if !c.isManagedPath(wt.Path) {
			return removed, fmt.Errorf("refusing to cleanup unmanaged worktree path: %s", wt.Path)
		}
		if _, err := runGit(c.s.cwd, "worktree", "remove", "--force", wt.Path); err != nil {
			if err := os.RemoveAll(wt.Path); err != nil {
				return removed, err
			}
		}
		removed = append(removed, wt)
	}
	return removed, nil
}

func (c *worktreeController) activeDiff() (WorktreeDiff, error) {
	status := c.status()
	if !status.Active {
		return WorktreeDiff{}, fmt.Errorf("no active worktree")
	}
	if !status.Exists {
		return WorktreeDiff{}, fmt.Errorf("active worktree path does not exist: %s", status.Path)
	}
	stat, err := runGit(status.Path, "diff", "--stat")
	if err != nil {
		return WorktreeDiff{}, err
	}
	nameStatus, err := runGit(status.Path, "diff", "--name-status")
	if err != nil {
		return WorktreeDiff{}, err
	}
	patch, err := runGit(status.Path, "diff")
	if err != nil {
		return WorktreeDiff{}, err
	}
	return WorktreeDiff{
		Path:       status.Path,
		Stat:       strings.TrimSpace(stat),
		NameStatus: strings.TrimSpace(nameStatus),
		Patch:      strings.TrimSpace(patch),
	}, nil
}

func (c *worktreeController) isManagedPath(path string) bool {
	if path == "" {
		return false
	}
	base, err := filepath.Abs(filepath.Join(c.s.agentDir, "worktrees"))
	if err != nil {
		return false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(base, abs)
	if err != nil {
		return false
	}
	return rel != "." && rel != "" && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != ".."
}

func (c *worktreeController) EnterWorktree() (string, error) {
	s := c.s
	if s == nil {
		return "", fmt.Errorf("worktree session is not configured")
	}
	if !s.config.FeatureWorktreeMode() {
		return "", fmt.Errorf("worktree mode is disabled by settings")
	}
	c.mu.Lock()
	if c.path != "" {
		path := c.path
		c.mu.Unlock()
		return path, nil
	}

	root, err := gitOutput(s.cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		c.mu.Unlock()
		return "", fmt.Errorf("enter_worktree: not a git repository: %w", err)
	}

	baseDir := filepath.Join(s.agentDir, "worktrees")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		c.mu.Unlock()
		return "", err
	}
	path := filepath.Join(baseDir, fmt.Sprintf("wt-%d", time.Now().UnixMilli()))
	if _, err := runGit(root, "worktree", "add", "--detach", path, "HEAD"); err != nil {
		c.mu.Unlock()
		return "", fmt.Errorf("enter_worktree: %w", err)
	}

	c.orig = s.cwd
	oldCwd := s.cwd
	c.path = path
	s.cwd = path
	s.refreshToolsForCwd(path)
	c.mu.Unlock()
	s.refreshDynamicSystemPrompt()
	s.runHarnessWorktreeCreate(path)
	s.runHarnessCwdChanged(oldCwd, path)
	s.writeRuntimeState()
	return path, nil
}

func (c *worktreeController) ExitWorktree() error {
	s := c.s
	if s == nil {
		return nil
	}
	c.mu.Lock()
	if c.path == "" {
		c.mu.Unlock()
		return nil
	}
	path := c.path
	restore := c.orig
	root, err := gitOutput(path, "rev-parse", "--show-toplevel")
	if err == nil {
		_, _ = runGit(root, "worktree", "remove", "--force", path)
	}
	c.path = ""
	if restore != "" {
		oldCwd := s.cwd
		s.cwd = restore
		c.orig = ""
		s.refreshToolsForCwd(restore)
		s.runHarnessCwdChanged(oldCwd, restore)
	}
	c.mu.Unlock()
	s.refreshDynamicSystemPrompt()
	s.runHarnessWorktreeRemove(path)
	s.writeRuntimeState()
	return nil
}

func (c *worktreeController) replaceTools() {
	s := c.s
	if !s.config.FeatureWorktreeMode() {
		s.activeTools = removeToolByName(s.activeTools, "enter_worktree")
		s.activeTools = removeToolByName(s.activeTools, "exit_worktree")
		stateTools := removeToolByName(s.agent.GetState().Tools, "enter_worktree")
		stateTools = removeToolByName(stateTools, "exit_worktree")
		s.agent.SetTools(stateTools)
		return
	}
	enter := worktreetool.NewEnterWorktreeTool(c)
	exit := worktreetool.NewExitWorktreeTool(c)
	s.activeTools = replaceTool(s.activeTools, enter)
	s.activeTools = replaceTool(s.activeTools, exit)
	stateTools := replaceTool(s.agent.GetState().Tools, enter)
	stateTools = replaceTool(stateTools, exit)
	s.agent.SetTools(stateTools)
}

func pathExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func gitOutput(dir string, args ...string) (string, error) {
	out, err := runGit(dir, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// refreshToolsForCwd rebinds every active tool to a new working directory and
// re-wraps them in the harness layer. Used by the worktree controller on
// enter/exit and once at construction.
func (s *CodingSession) refreshToolsForCwd(cwd string) {
	var updated []agent.Tool
	for _, tool := range s.activeTools {
		if rebound, ok := s.toolProvider.Rebind(tool, agent.ToolContext{Cwd: cwd}); ok {
			updated = append(updated, rebound)
			continue
		}
		updated = append(updated, tool)
	}
	updated = wrapHarnessTools(updated, s)
	s.activeTools = updated
	s.agent.SetTools(updated)
}

// --- CodingSession delegates (preserve the public API surface) ---

// ActiveWorktree returns the currently active isolated worktree path, if any.
func (s *CodingSession) ActiveWorktree() string { return s.worktree.ActiveWorktree() }

// WorktreeStatus returns the current isolated worktree state without mutating it.
func (s *CodingSession) WorktreeStatus() WorktreeStatus { return s.worktree.status() }

// ListManagedWorktrees returns managed worktree directories under the agent
// runtime root and marks the currently active one.
func (s *CodingSession) ListManagedWorktrees() []WorktreeInfo { return s.worktree.listManaged() }

// CleanupManagedWorktrees removes inactive managed worktree directories. The
// active worktree is never removed.
func (s *CodingSession) CleanupManagedWorktrees() ([]WorktreeInfo, error) {
	return s.worktree.cleanup()
}

// ActiveWorktreeDiff returns a read-only diff for the active isolated worktree.
func (s *CodingSession) ActiveWorktreeDiff() (WorktreeDiff, error) { return s.worktree.activeDiff() }

// EnterWorktree moves the session into a fresh isolated git worktree.
func (s *CodingSession) EnterWorktree() (string, error) { return s.worktree.EnterWorktree() }

// ExitWorktree leaves the active worktree and restores the original cwd.
func (s *CodingSession) ExitWorktree() error { return s.worktree.ExitWorktree() }
