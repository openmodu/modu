package coding_agent

import "github.com/openmodu/modu/pkg/coding_agent/tools"

func (s *CodingSession) replaceTaskOutputTool() {
	if !s.config.FeatureTaskOutputTool() {
		s.activeTools = removeToolByName(s.activeTools, "task_output")
		s.agent.SetTools(removeToolByName(s.agent.GetState().Tools, "task_output"))
		return
	}
	var store tools.BackgroundTaskStore
	if s.taskManager != nil {
		store = s.taskManager
	}
	taskTool := tools.NewTaskOutputTool(store)
	s.activeTools = replaceTool(s.activeTools, taskTool)
	s.agent.SetTools(replaceTool(s.agent.GetState().Tools, taskTool))
}
