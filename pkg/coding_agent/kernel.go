package coding_agent

import "github.com/openmodu/modu/pkg/agent"

// This file is the kernel capability surface: small exported methods that let
// L2 service packages (plan, worktree, …) reach the kernel through their narrow
// Host interfaces without depending on CodingSession's internals. Each wraps an
// unexported field or method; the names are specific so one CodingSession can
// satisfy every service's role interface unambiguously.

// WriteRuntimeState persists the session runtime-state snapshot.
func (s *CodingSession) WriteRuntimeState() { s.writeRuntimeState() }

// RefreshSystemPrompt rebuilds the system prompt for the current state.
func (s *CodingSession) RefreshSystemPrompt() { s.refreshDynamicSystemPrompt() }

// PlanFile returns the path of the latest persisted plan.
func (s *CodingSession) PlanFile() string { return s.RuntimePaths().PlanFile }

// PlansDir returns the directory holding plan revisions.
func (s *CodingSession) PlansDir() string { return s.RuntimePaths().PlansDir }

// PlanModeEnabled reports whether plan mode is enabled by config.
func (s *CodingSession) PlanModeEnabled() bool { return s.config.FeaturePlanMode() }

// AgentDir returns the agent configuration directory.
func (s *CodingSession) AgentDir() string { return s.agentDir }

// WorktreeModeEnabled reports whether worktree mode is enabled by config.
func (s *CodingSession) WorktreeModeEnabled() bool { return s.config.FeatureWorktreeMode() }

// EmitWorktreeCreated / EmitWorktreeRemoved surface worktree lifecycle events.
func (s *CodingSession) EmitWorktreeCreated(path string) { s.runHarnessWorktreeCreate(path) }
func (s *CodingSession) EmitWorktreeRemoved(path string) { s.runHarnessWorktreeRemove(path) }

// SwitchCwd moves the session to newCwd: it sets the working directory, rebinds
// every tool to it, refreshes the system prompt, and emits a cwd-changed event.
// It is the single capability worktree enter/exit use to relocate the session.
func (s *CodingSession) SwitchCwd(newCwd string) {
	oldCwd := s.cwd
	s.cwd = newCwd
	s.refreshToolsForCwd(newCwd)
	s.refreshDynamicSystemPrompt()
	s.runHarnessCwdChanged(oldCwd, newCwd)
}

// refreshToolsForCwd rebinds every active tool to a new working directory and
// re-wraps them in the harness layer.
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
