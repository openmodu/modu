package coding_agent

import "github.com/openmodu/modu/pkg/coding_agent/tools"

type taskStoreAdapter struct {
	manager *backgroundTaskManager
}

func (a taskStoreAdapter) Create(kind, summary string) string {
	if a.manager == nil {
		return ""
	}
	return a.manager.Create(kind, summary)
}

func (a taskStoreAdapter) Complete(id, output string) {
	if a.manager == nil {
		return
	}
	a.manager.Complete(id, output)
}

func (a taskStoreAdapter) Fail(id, errMsg string) {
	if a.manager == nil {
		return
	}
	a.manager.Fail(id, errMsg)
}

func (a taskStoreAdapter) Get(id string) (tools.BackgroundTask, bool) {
	if a.manager == nil {
		return tools.BackgroundTask{}, false
	}
	task, ok := a.manager.Get(id)
	if !ok {
		return tools.BackgroundTask{}, false
	}
	return tools.BackgroundTask{
		ID:      task.ID,
		Kind:    task.Kind,
		Status:  task.Status,
		Summary: task.Summary,
		Output:  task.Output,
		Error:   task.Error,
	}, true
}

func (a taskStoreAdapter) List() []tools.BackgroundTask {
	if a.manager == nil {
		return nil
	}
	items := a.manager.List()
	out := make([]tools.BackgroundTask, len(items))
	for i, task := range items {
		out[i] = tools.BackgroundTask{
			ID:      task.ID,
			Kind:    task.Kind,
			Status:  task.Status,
			Summary: task.Summary,
			Output:  task.Output,
			Error:   task.Error,
		}
	}
	return out
}

func (s *CodingSession) replaceTaskOutputTool() {
	if !s.config.FeatureTaskOutputTool() {
		s.activeTools = removeAgentToolByName(s.activeTools, "task_output")
		s.agent.SetTools(removeAgentToolByName(s.agent.GetState().Tools, "task_output"))
		return
	}
	taskTool := tools.NewTaskOutputTool(taskStoreAdapter{manager: s.taskManager})
	s.activeTools = replaceAgentTool(s.activeTools, taskTool)
	s.agent.SetTools(replaceAgentTool(s.agent.GetState().Tools, taskTool))
}
