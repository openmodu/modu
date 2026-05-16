package coding_agent

// ContextFileInfo describes one context file currently discoverable by the
// session.
type ContextFileInfo struct {
	Name  string
	Path  string
	Bytes int
}

// ContextInfo describes the runtime context sources that can affect prompts.
type ContextInfo struct {
	Cwd             string
	AgentDir        string
	ModelName       string
	ModelProvider   string
	ModelID         string
	MessageCount    int
	MemoryBytes     int
	ContextFiles    []ContextFileInfo
	Skills          []SkillInfo
	PlanMode        bool
	ActiveWorktree  string
	PromptByteCount int
}

// GetContextInfo returns a read-only snapshot of prompt/context sources.
func (s *CodingSession) GetContextInfo() ContextInfo {
	state := s.agent.GetState()
	info := ContextInfo{
		Cwd:             s.cwd,
		AgentDir:        s.agentDir,
		MessageCount:    len(state.Messages),
		Skills:          s.GetSkills(),
		PlanMode:        s.IsPlanMode(),
		ActiveWorktree:  s.ActiveWorktree(),
		PromptByteCount: len(state.SystemPrompt),
	}
	if state.Model != nil {
		info.ModelName = state.Model.Name
		info.ModelProvider = state.Model.ProviderID
		info.ModelID = state.Model.ID
	}
	if s.memoryStore != nil {
		info.MemoryBytes = len(s.memoryStore.GetMemoryContext())
	}
	if s.resources != nil {
		files := s.resources.LoadContextFiles()
		info.ContextFiles = make([]ContextFileInfo, 0, len(files))
		for _, file := range files {
			info.ContextFiles = append(info.ContextFiles, ContextFileInfo{
				Name:  file.Name,
				Path:  file.Path,
				Bytes: len(file.Content),
			})
		}
	}
	return info
}
