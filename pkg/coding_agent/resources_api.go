package coding_agent

import (
	"github.com/openmodu/modu/pkg/coding_agent/foundation/resource"
	"github.com/openmodu/modu/pkg/skills"
	"github.com/openmodu/modu/pkg/types"
)

// SetActiveTools sets which tools are active by name.
func (s *engine) SetActiveTools(names []string) {
	nameSet := make(map[string]bool)
	for _, n := range names {
		nameSet[n] = true
	}

	var active []types.Tool
	for _, tool := range s.activeTools {
		if nameSet[tool.Name()] {
			active = append(active, tool)
		}
	}

	s.agent.SetTools(active)
	s.refreshDynamicSystemPrompt()
	s.writeRuntimeState()
}

func (s *engine) refreshResourcePaths() resource.ResourceSnapshot {
	if s.resources == nil {
		return resource.ResourceSnapshot{}
	}
	snapshot := s.resources.LoadResources()
	if s.skillManager != nil {
		s.skillManager.SetExtraPaths(skillPathRefs(snapshot.SkillPaths))
	}
	if s.promptManager != nil {
		s.promptManager.SetExtraPaths(snapshot.PromptPaths)
	}
	return snapshot
}

// skillPathRefs converts resource package refs into skill discovery refs.
func skillPathRefs(refs []resource.ResourceRef) []skills.PathRef {
	out := make([]skills.PathRef, len(refs))
	for i, r := range refs {
		out[i] = skills.PathRef{Path: r.Path, Source: r.Source}
	}
	return out
}

// SkillInfo is a minimal view of a skill for display purposes.
type SkillInfo struct {
	Name        string
	Description string
	Source      string // "user" or "project"
}

// SubagentInfo is a minimal view of a discovered subagent definition.
type SubagentInfo struct {
	Name        string
	Description string
	Source      string // "user" or "project"
	FilePath    string
}

// GetSkills returns all discovered skills.
func (s *engine) GetSkills() []SkillInfo {
	if s.skillManager == nil {
		return nil
	}
	s.refreshResourcePaths()
	list := s.skillManager.List()
	out := make([]SkillInfo, len(list))
	for i, sk := range list {
		out[i] = SkillInfo{Name: sk.Name, Description: sk.Description, Source: sk.Source}
	}
	return out
}

// GetSubagents returns all discovered subagent definitions.
func (s *engine) GetSubagents() []SubagentInfo {
	if s.subagentLoader == nil {
		return nil
	}
	list := s.subagentLoader.List()
	out := make([]SubagentInfo, len(list))
	for i, def := range list {
		out[i] = SubagentInfo{
			Name:        def.Name,
			Description: def.Description,
			Source:      def.Source,
			FilePath:    def.FilePath,
		}
	}
	return out
}

// GetActiveToolNames returns the names of currently active tools.
func (s *engine) GetActiveToolNames() []string {
	state := s.agent.GetState()
	names := make([]string, len(state.Tools))
	for i, t := range state.Tools {
		names[i] = t.Name()
	}
	return names
}

// ReloadResources reloads dynamic resources and refreshes the prompt.
func (s *engine) ReloadResources() {
	s.refreshResourcePaths()
	s.refreshDynamicSystemPrompt()
	s.writeRuntimeState()
}
