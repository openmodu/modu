package coding_agent

import backendtask "github.com/openmodu/modu/pkg/coding_agent/tools/backend_task"

func (s *CodingSession) replaceTaskOutputTool() {
	if !s.config.FeatureTaskOutputTool() {
		s.activeTools = removeToolByName(s.activeTools, "task_output")
		s.agent.SetTools(removeToolByName(s.agent.GetState().Tools, "task_output"))
		return
	}
	var store backendtask.BackgroundTaskStore
	if s.taskManager != nil {
		store = s.taskManager
	}
	taskTool := backendtask.NewTaskOutputTool(store)
	s.activeTools = replaceTool(s.activeTools, taskTool)
	s.agent.SetTools(replaceTool(s.agent.GetState().Tools, taskTool))
}
