package coding_agent

import "github.com/openmodu/modu/pkg/coding_agent/tools"

func (s *CodingSession) replaceTaskOutputTool() {
	if !s.config.FeatureTaskOutputTool() {
		s.activeTools = removeAgentToolByName(s.activeTools, "task_output")
		s.agent.SetTools(removeAgentToolByName(s.agent.GetState().Tools, "task_output"))
		return
	}
	var store tools.BackgroundTaskStore
	if s.taskManager != nil {
		store = s.taskManager
	}
	taskTool := tools.NewTaskOutputTool(store)
	s.activeTools = replaceAgentTool(s.activeTools, taskTool)
	s.agent.SetTools(replaceAgentTool(s.agent.GetState().Tools, taskTool))
}
