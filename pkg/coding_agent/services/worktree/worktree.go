// Package worktree is the isolated-git-worktree service: it creates/removes a
// detached worktree and moves the session into and out of it. It owns the
// active-worktree state and reaches the kernel through the narrow Host
// interface, implementing worktreetool.WorktreeManager structurally so the
// kernel can register the worktree tools against it.
package worktree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Host is the set of kernel capabilities the worktree service needs.
type Host interface {
	Cwd() string
	AgentDir() string
	// SwitchCwd moves the session to newCwd: it sets the cwd, rebinds tools,
	// refreshes the system prompt, and emits a cwd-changed event.
	SwitchCwd(newCwd string)
	EmitWorktreeCreated(path string)
	EmitWorktreeRemoved(path string)
	WriteRuntimeState()
	WorktreeModeEnabled() bool
}

// Status describes the current isolated-worktree lifecycle state.
type Status struct {
	Active      bool
	Path        string
	OriginalCwd string
	Cwd         string
	Exists      bool
}

// Info describes one managed worktree under the agent dir.
type Info struct {
	Path   string
	Active bool
	Exists bool
}

// Diff describes the current active-worktree changes.
type Diff struct {
	Path       string
	Stat       string
	NameStatus string
	Patch      string
}

// Controller owns the active-worktree state and drives the session through host.
type Controller struct {
	host Host
	mu   sync.Mutex
	path string
	orig string
}

// New creates a worktree controller bound to a host.
func New(host Host) *Controller { return &Controller{host: host} }

// ActiveWorktree returns the currently active isolated worktree path, if any.
func (c *Controller) ActiveWorktree() string {
	if c == nil || c.host == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.path
}

func (c *Controller) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	status := Status{
		Active:      c.path != "",
		Path:        c.path,
		OriginalCwd: c.orig,
		Cwd:         c.host.Cwd(),
	}
	if status.Path != "" {
		if _, err := os.Stat(status.Path); err == nil {
			status.Exists = true
		}
	}
	return status
}

func (c *Controller) ListManaged() []Info {
	c.mu.Lock()
	activePath := c.path
	c.mu.Unlock()

	dir := filepath.Join(c.host.AgentDir(), "worktrees")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if activePath == "" {
			return nil
		}
		return []Info{{Path: activePath, Active: true, Exists: pathExists(activePath)}}
	}

	seen := make(map[string]struct{}, len(entries)+1)
	out := make([]Info, 0, len(entries)+1)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		seen[path] = struct{}{}
		out = append(out, Info{Path: path, Active: path == activePath, Exists: true})
	}
	if activePath != "" {
		if _, ok := seen[activePath]; !ok {
			out = append(out, Info{Path: activePath, Active: true, Exists: pathExists(activePath)})
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

func (c *Controller) Cleanup() ([]Info, error) {
	worktrees := c.ListManaged()
	removed := make([]Info, 0, len(worktrees))
	for _, wt := range worktrees {
		if wt.Active || !wt.Exists {
			continue
		}
		if !c.isManagedPath(wt.Path) {
			return removed, fmt.Errorf("refusing to cleanup unmanaged worktree path: %s", wt.Path)
		}
		if _, err := runGit(c.host.Cwd(), "worktree", "remove", "--force", wt.Path); err != nil {
			if err := os.RemoveAll(wt.Path); err != nil {
				return removed, err
			}
		}
		removed = append(removed, wt)
	}
	return removed, nil
}

func (c *Controller) ActiveDiff() (Diff, error) {
	status := c.Status()
	if !status.Active {
		return Diff{}, fmt.Errorf("no active worktree")
	}
	if !status.Exists {
		return Diff{}, fmt.Errorf("active worktree path does not exist: %s", status.Path)
	}
	stat, err := runGit(status.Path, "diff", "--stat")
	if err != nil {
		return Diff{}, err
	}
	nameStatus, err := runGit(status.Path, "diff", "--name-status")
	if err != nil {
		return Diff{}, err
	}
	patch, err := runGit(status.Path, "diff")
	if err != nil {
		return Diff{}, err
	}
	return Diff{
		Path:       status.Path,
		Stat:       strings.TrimSpace(stat),
		NameStatus: strings.TrimSpace(nameStatus),
		Patch:      strings.TrimSpace(patch),
	}, nil
}

func (c *Controller) isManagedPath(path string) bool {
	if path == "" {
		return false
	}
	base, err := filepath.Abs(filepath.Join(c.host.AgentDir(), "worktrees"))
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

// EnterWorktree creates a detached worktree and moves the session into it.
func (c *Controller) EnterWorktree() (string, error) {
	if c == nil || c.host == nil {
		return "", fmt.Errorf("worktree host is not configured")
	}
	if !c.host.WorktreeModeEnabled() {
		return "", fmt.Errorf("worktree mode is disabled by settings")
	}
	c.mu.Lock()
	if c.path != "" {
		path := c.path
		c.mu.Unlock()
		return path, nil
	}
	cwd := c.host.Cwd()
	root, err := gitOutput(cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		c.mu.Unlock()
		return "", fmt.Errorf("enter_worktree: not a git repository: %w", err)
	}
	baseDir := filepath.Join(c.host.AgentDir(), "worktrees")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		c.mu.Unlock()
		return "", err
	}
	path := filepath.Join(baseDir, fmt.Sprintf("wt-%d", time.Now().UnixMilli()))
	if _, err := runGit(root, "worktree", "add", "--detach", path, "HEAD"); err != nil {
		c.mu.Unlock()
		return "", fmt.Errorf("enter_worktree: %w", err)
	}
	c.orig = cwd
	c.path = path
	c.mu.Unlock()

	// SwitchCwd refreshes the prompt, which reads ActiveWorktree(); it must run
	// outside c.mu to avoid a re-entrant lock.
	c.host.SwitchCwd(path)
	c.host.EmitWorktreeCreated(path)
	c.host.WriteRuntimeState()
	return path, nil
}

// ExitWorktree removes the active worktree and restores the original cwd.
func (c *Controller) ExitWorktree() error {
	if c == nil || c.host == nil {
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
	c.orig = ""
	c.mu.Unlock()

	if restore != "" {
		c.host.SwitchCwd(restore)
	}
	c.host.EmitWorktreeRemoved(path)
	c.host.WriteRuntimeState()
	return nil
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
