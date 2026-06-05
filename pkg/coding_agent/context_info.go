package coding_agent

// ContextFileInfo describes one context file currently discoverable by the
// session.
type ContextFileInfo struct {
	Name  string
	Path  string
	Bytes int
}

type PromptTemplateInfo struct {
	Name         string
	Description  string
	ArgumentHint string
	Source       string
	FilePath     string
}

type PackageResourceInfo struct {
	Name    string
	Source  string
	Path    string
	Enabled bool
	Skills  int
	Prompts int
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
	PromptTemplates []PromptTemplateInfo
	Packages        []PackageResourceInfo
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
		resources := s.refreshResourcePaths()
		info.Skills = s.GetSkills()
		files := resources.ContextFiles
		info.ContextFiles = make([]ContextFileInfo, 0, len(files))
		for _, file := range files {
			info.ContextFiles = append(info.ContextFiles, ContextFileInfo{
				Name:  file.Name,
				Path:  file.Path,
				Bytes: len(file.Content),
			})
		}
		for _, pkg := range resources.Packages {
			info.Packages = append(info.Packages, PackageResourceInfo{
				Name:    pkg.Name,
				Source:  pkg.Source,
				Path:    pkg.Path,
				Enabled: pkg.Enabled,
				Skills:  len(pkg.Skills),
				Prompts: len(pkg.Prompts),
			})
		}
	}
	info.PromptTemplates = s.GetPromptTemplates()
	return info
}

// GetPromptTemplates returns discovered prompt templates.
func (s *CodingSession) GetPromptTemplates() []PromptTemplateInfo {
	if s.promptManager == nil {
		return nil
	}
	s.refreshResourcePaths()
	list := s.promptManager.List()
	out := make([]PromptTemplateInfo, 0, len(list))
	for _, t := range list {
		out = append(out, PromptTemplateInfo{
			Name:         t.Name,
			Description:  t.Description,
			ArgumentHint: t.Metadata["argument-hint"],
			Source:       t.Source,
			FilePath:     t.FilePath,
		})
	}
	return out
}
