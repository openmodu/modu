package coding_agent

import "github.com/openmodu/modu/pkg/coding_agent/foundation/taskoutput"

// BackgroundTask and BackgroundTaskStore alias the taskoutput contract types
// used by the host API and the background-task tool. The implementation lives
// in services/bgtask.
type (
	BackgroundTask      = taskoutput.Task
	BackgroundTaskStore = taskoutput.Store
)

// GetBackgroundTasks returns a snapshot of session background tasks.
func (s *engine) GetBackgroundTasks() []BackgroundTask {
	if s.taskManager == nil {
		return nil
	}
	return s.taskManager.List()
}
