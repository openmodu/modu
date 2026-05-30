package coding_agent

import (
	"github.com/openmodu/modu/pkg/coding_agent/services/worktree"
	worktreetool "github.com/openmodu/modu/pkg/coding_agent/tools/worktree"
)

// Worktree types alias the worktree service so existing callers keep working.
type (
	WorktreeStatus = worktree.Status
	WorktreeInfo   = worktree.Info
	WorktreeDiff   = worktree.Diff
)

// CodingSession implements worktree.Host.
var _ worktree.Host = (*CodingSession)(nil)

// replaceWorktreeTools registers (or removes) the worktree tools. Tool
// registration is a kernel concern; the worktree controller is supplied as the
// tools' manager.
func (s *CodingSession) replaceWorktreeTools() {
	if !s.config.FeatureWorktreeMode() {
		s.activeTools = removeToolByName(s.activeTools, "enter_worktree")
		s.activeTools = removeToolByName(s.activeTools, "exit_worktree")
		stateTools := removeToolByName(s.agent.GetState().Tools, "enter_worktree")
		stateTools = removeToolByName(stateTools, "exit_worktree")
		s.agent.SetTools(stateTools)
		return
	}
	enter := worktreetool.NewEnterWorktreeTool(s.worktree)
	exit := worktreetool.NewExitWorktreeTool(s.worktree)
	s.activeTools = replaceTool(s.activeTools, enter)
	s.activeTools = replaceTool(s.activeTools, exit)
	stateTools := replaceTool(s.agent.GetState().Tools, enter)
	stateTools = replaceTool(stateTools, exit)
	s.agent.SetTools(stateTools)
}

// --- delegates (preserve the public API surface) ---

// ActiveWorktree returns the currently active isolated worktree path, if any.
func (s *CodingSession) ActiveWorktree() string { return s.worktree.ActiveWorktree() }

// WorktreeStatus returns the current isolated worktree state.
func (s *CodingSession) WorktreeStatus() WorktreeStatus { return s.worktree.Status() }

// ListManagedWorktrees returns managed worktree directories, active one marked.
func (s *CodingSession) ListManagedWorktrees() []WorktreeInfo { return s.worktree.ListManaged() }

// CleanupManagedWorktrees removes inactive managed worktree directories.
func (s *CodingSession) CleanupManagedWorktrees() ([]WorktreeInfo, error) {
	return s.worktree.Cleanup()
}

// ActiveWorktreeDiff returns a read-only diff for the active isolated worktree.
func (s *CodingSession) ActiveWorktreeDiff() (WorktreeDiff, error) { return s.worktree.ActiveDiff() }

// EnterWorktree moves the session into a fresh isolated git worktree.
func (s *CodingSession) EnterWorktree() (string, error) { return s.worktree.EnterWorktree() }

// ExitWorktree leaves the active worktree and restores the original cwd.
func (s *CodingSession) ExitWorktree() error { return s.worktree.ExitWorktree() }
