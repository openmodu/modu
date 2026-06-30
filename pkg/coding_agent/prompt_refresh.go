package coding_agent

import "github.com/openmodu/modu/pkg/coding_agent/services/systemprompt"

// refreshDynamicSystemPrompt rebuilds the system prompt from scratch every
// turn. The skills XML block, context files, and memory are all regenerated
// against the current filesystem so edits to skill files (or new skills
// dropped into the skills dir) are reflected without restarting the session.
// Active-mode blocks (plan mode, worktree) are routed through the builder so
// the whole prompt is produced by a single Build path.
func (s *engine) refreshDynamicSystemPrompt() {
	s.refreshResourcePaths()
	if s.promptBuilder == nil {
		return
	}
	if s.skillManager != nil {
		// FormatForPrompt rediscovers under the hood, so the XML block
		// always reflects what's on disk right now.
		s.promptBuilder.SetSkillsPrompt(s.skillManager.FormatForPrompt())
	}
	if s.agent != nil {
		s.promptBuilder.SetTools(s.agent.GetState().Tools)
	}
	s.applyMemoryProvider()
	s.promptBuilder.SetModeBlocks(s.currentModeBlocks())
	s.agent.SetSystemPrompt(s.promptBuilder.Build())
}

func (s *engine) applyMemoryProvider() {
	if s.promptBuilder == nil {
		return
	}
	if memoryFeatureEnabled(s.config) && s.memoryStore != nil {
		s.promptBuilder.SetMemoryProvider(s.memoryStore)
		return
	}
	s.promptBuilder.SetMemoryProvider(nil)
}

// currentModeBlocks returns the active-mode prompt blocks for the current
// session state, in the order they should appear.
func (s *engine) currentModeBlocks() []string {
	planMode := s.plan.IsPlanMode()
	worktreePath := s.worktree.ActiveWorktree()

	var blocks []string
	if s.ultracode && s.activeToolNamed("workflow") {
		blocks = append(blocks, systemprompt.UltracodeBlock)
	}
	if planMode {
		blocks = append(blocks, systemprompt.PlanModeBlock)
	}
	if worktreePath != "" {
		blocks = append(blocks, systemprompt.WorktreeBlock(worktreePath))
	}
	return blocks
}

func (s *engine) activeToolNamed(name string) bool {
	if s == nil || s.agent == nil {
		return false
	}
	for _, tool := range s.agent.GetState().Tools {
		if tool != nil && tool.Name() == name {
			return true
		}
	}
	return false
}
