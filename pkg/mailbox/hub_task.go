package mailbox

import (
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"
)

// --- Project 管理 ---

func (h *Hub) nextProjectID() string {
	n := atomic.AddUint64(&h.projectCounter, 1)
	return fmt.Sprintf("proj-%d", n)
}

// CreateProject 创建一个新项目，返回 project ID
func (h *Hub) CreateProject(creatorID, name string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.agentInfos[creatorID]; !ok {
		return "", ErrAgentNotFound
	}
	id := h.nextProjectID()
	now := time.Now()
	proj := &Project{
		ID:        id,
		Name:      name,
		CreatedBy: creatorID,
		TaskIDs:   []string{},
		Status:    "active",
		CreatedAt: now,
		UpdatedAt: now,
	}
	h.projects[id] = proj
	snapshot := *proj
	h.publishLocked(Event{Type: EventTypeProjectCreated, ProjectID: id, Data: snapshot})
	if err := h.store.SaveProject(snapshot); err != nil {
		log.Printf("[Hub] SaveProject %s: %v", id, err)
	}
	return id, nil
}

// GetProject 返回项目快照
func (h *Hub) GetProject(projectID string) (Project, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	proj, ok := h.projects[projectID]
	if !ok {
		return Project{}, ErrProjectNotFound
	}
	return *proj, nil
}

// CompleteProject 将项目标记为已完成
func (h *Hub) CompleteProject(projectID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	proj, ok := h.projects[projectID]
	if !ok {
		return ErrProjectNotFound
	}
	proj.Status = "completed"
	proj.UpdatedAt = time.Now()
	snapshot := *proj
	h.publishLocked(Event{Type: EventTypeProjectUpdated, ProjectID: projectID, Data: snapshot})
	if err := h.store.SaveProject(snapshot); err != nil {
		log.Printf("[Hub] SaveProject %s (complete): %v", projectID, err)
	}
	return nil
}

// ListProjects 返回所有项目快照
func (h *Hub) ListProjects() []Project {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make([]Project, 0, len(h.projects))
	for _, p := range h.projects {
		result = append(result, *p)
	}
	return result
}

// --- Task 管理 ---

func (h *Hub) nextTaskID() string {
	n := atomic.AddUint64(&h.taskCounter, 1)
	return fmt.Sprintf("task-%d", n)
}

// CreateTask 创建一个新任务，返回 task ID。可选传入 projectID 将任务归入项目。
func (h *Hub) CreateTask(creatorID, description string, projectID ...string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.agentInfos[creatorID]; !ok {
		return "", ErrAgentNotFound
	}
	pid := ""
	if len(projectID) > 0 {
		pid = projectID[0]
	}
	id := h.nextTaskID()
	now := time.Now()
	task := &Task{
		ID:           id,
		ProjectID:    pid,
		Description:  description,
		CreatedBy:    creatorID,
		Assignees:    []string{},
		AgentResults: make(map[string]string),
		Status:       TaskStatusPending,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	h.tasks[id] = task
	// 将任务 ID 添加到所属项目
	if pid != "" {
		if proj, ok := h.projects[pid]; ok {
			proj.TaskIDs = append(proj.TaskIDs, id)
			proj.UpdatedAt = now
			ps := *proj
			if err := h.store.SaveProject(ps); err != nil {
				log.Printf("[Hub] SaveProject %s (add task): %v", pid, err)
			}
		}
	}
	snapshot := *task
	h.publishLocked(Event{Type: EventTypeTaskCreated, TaskID: id, Data: snapshot})
	if err := h.store.SaveTask(snapshot); err != nil {
		log.Printf("[Hub] SaveTask %s (create): %v", id, err)
	}
	return id, nil
}

// AssignTask 将任务追加指派给指定 Agent（可多次调用以支持多执行者）
func (h *Hub) AssignTask(taskID, agentID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	task, ok := h.tasks[taskID]
	if !ok {
		return ErrTaskNotFound
	}
	if _, ok := h.agentInfos[agentID]; !ok {
		return ErrAgentNotFound
	}
	// 避免重复添加
	for _, a := range task.Assignees {
		if a == agentID {
			return nil
		}
	}
	task.Assignees = append(task.Assignees, agentID)
	if task.OwnerID == "" {
		task.OwnerID = agentID
		task.AssignedTo = agentID // 向后兼容：始终指向 owner
	} else {
		task.Collaborators = append(task.Collaborators, agentID)
		task.AssignedTo = task.OwnerID
	}
	task.UpdatedAt = time.Now()
	snapshot := *task
	h.publishLocked(Event{Type: EventTypeTaskUpdated, TaskID: taskID, Data: snapshot})
	if err := h.store.SaveTask(snapshot); err != nil {
		log.Printf("[Hub] SaveTask %s (assign): %v", taskID, err)
	}
	return nil
}

// releaseOwnerLocked resets the task owner's agent status to idle when that agent is
// still recorded as handling this specific task. Must be called with h.mu held.
func (h *Hub) releaseOwnerLocked(task *Task) {
	ownerID := task.OwnerID
	if ownerID == "" {
		ownerID = task.AssignedTo
	}
	if ownerID == "" {
		return
	}
	info, ok := h.agentInfos[ownerID]
	if !ok || info.CurrentTask != task.ID {
		return
	}
	info.Status = "idle"
	info.CurrentTask = ""
	h.publishLocked(Event{Type: EventTypeAgentUpdated, AgentID: ownerID, Data: *info})
}

// StartTask 将任务状态设为 running，并将 owner agent 的状态更新为 busy
func (h *Hub) StartTask(taskID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	task, ok := h.tasks[taskID]
	if !ok {
		return ErrTaskNotFound
	}
	task.Status = TaskStatusRunning
	task.UpdatedAt = time.Now()
	// Track which task the owner agent is working on so that eviction can
	// recover or fail the task if the agent goes offline.
	ownerID := task.OwnerID
	if ownerID == "" {
		ownerID = task.AssignedTo
	}
	if ownerID != "" {
		if info, ok := h.agentInfos[ownerID]; ok {
			info.Status = "busy"
			info.CurrentTask = taskID
		}
	}
	snapshot := *task
	h.publishLocked(Event{Type: EventTypeTaskUpdated, TaskID: taskID, Data: snapshot})
	if err := h.store.SaveTask(snapshot); err != nil {
		log.Printf("[Hub] SaveTask %s (start): %v", taskID, err)
	}
	return nil
}

// CompleteTask 记录任务完成成果。
//
// 在当前"单任务、多协作者"模型下，任务由 owner 统一完成：
//   - 当 agentID 为 ownerID（或兼容旧数据时等于 AssignedTo）时，立即完成整个任务
//   - 协作者可以通过 mailbox_reply 提交工作，但不会直接驱动 task 状态完成
//   - 当 agentID 为空时（向后兼容旧单 agent 调用方式），若任务有多个 assignee 则返回错误
func (h *Hub) CompleteTask(taskID, agentID, result string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	task, ok := h.tasks[taskID]
	if !ok {
		return ErrTaskNotFound
	}
	if task.AgentResults == nil {
		task.AgentResults = make(map[string]string)
	}
	task.Result = result
	task.Resolution = result
	task.UpdatedAt = time.Now()

	if agentID != "" {
		task.AgentResults[agentID] = result
		isOwner := task.OwnerID != "" && agentID == task.OwnerID
		isLegacyOwner := task.OwnerID == "" && task.AssignedTo != "" && agentID == task.AssignedTo
		if isOwner || isLegacyOwner || len(task.Assignees) == 0 {
			task.Status = TaskStatusCompleted
			now := time.Now()
			task.DiscussionClosedAt = &now
		} else {
			return fmt.Errorf("task %s can only be completed by owner %s", taskID, task.OwnerID)
		}
	} else {
		// 旧调用方式（无 agentID）：多 assignee 任务必须用带 agentID 的新接口
		if len(task.Assignees) > 1 {
			return fmt.Errorf("task %s has %d assignees: use CompleteTask with agentID", taskID, len(task.Assignees))
		}
		task.Status = TaskStatusCompleted
		now := time.Now()
		task.DiscussionClosedAt = &now
	}

	snapshot := *task
	h.publishLocked(Event{Type: EventTypeTaskUpdated, TaskID: taskID, Data: snapshot})
	if task.Status == TaskStatusCompleted {
		h.releaseOwnerLocked(task)
		h.advancePipelineLocked(task, result)
	}
	if err := h.store.SaveTask(snapshot); err != nil {
		log.Printf("[Hub] SaveTask %s (complete): %v", taskID, err)
	}
	return nil
}

// advancePipelineLocked is called after a task completes. If the task belongs
// to a Pipeline it either enqueues the next step or marks the pipeline complete.
// Caller must hold h.mu write lock.
func (h *Hub) advancePipelineLocked(task *Task, result string) {
	if task.PipelineID == "" {
		return
	}
	pipeline, ok := h.pipelines[task.PipelineID]
	if !ok {
		return
	}
	now := time.Now()
	pipeline.Results = append(pipeline.Results, result)
	pipeline.UpdatedAt = now

	if task.NextStepTemplate == "" {
		// This was the last step.
		pipeline.Status = "completed"
		h.publishLocked(Event{
			Type:       EventTypePipelineCompleted,
			PipelineID: task.PipelineID,
			StepIdx:    task.PipelineStepIdx,
			Data:       *pipeline,
		})
		log.Printf("[Hub] Pipeline %s completed (%d steps)", task.PipelineID, len(pipeline.Steps))
		return
	}

	// Render the next step's description by injecting the current result.
	nextDesc := strings.ReplaceAll(task.NextStepTemplate, "{{.PrevResult}}", result)
	nextStepIdx := task.PipelineStepIdx + 1

	// Determine the step after next (if any).
	var nextNextTemplate string
	var nextNextCaps []string
	if nextStepIdx+1 < len(pipeline.Steps) {
		nextNextTemplate = pipeline.Steps[nextStepIdx+1].DescriptionTemplate
		nextNextCaps = pipeline.Steps[nextStepIdx+1].RequiredCaps
	}

	nextTaskID := h.nextTaskID()
	nextTask := &Task{
		ID:               nextTaskID,
		Description:      nextDesc,
		CreatedBy:        task.PipelineID,
		Assignees:        []string{},
		AgentResults:     make(map[string]string),
		Status:           TaskStatusPending,
		SwarmOrigin:      true,
		RequiredCaps:     task.NextStepCaps,
		PipelineID:       task.PipelineID,
		PipelineStepIdx:  nextStepIdx,
		NextStepTemplate: nextNextTemplate,
		NextStepCaps:     nextNextCaps,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	h.tasks[nextTaskID] = nextTask
	h.swarmQueue = append(h.swarmQueue, nextTaskID)
	pipeline.CurrentStep = nextStepIdx

	h.publishLocked(Event{
		Type:       EventTypePipelineStepCompleted,
		PipelineID: task.PipelineID,
		TaskID:     nextTaskID,
		StepIdx:    task.PipelineStepIdx,
		Data:       *pipeline,
	})
	if err := h.store.SaveTask(*nextTask); err != nil {
		log.Printf("[Hub] SaveTask %s (pipeline step %d): %v", nextTaskID, nextStepIdx, err)
	}
	log.Printf("[Hub] Pipeline %s step %d completed; step %d task %s enqueued",
		task.PipelineID, task.PipelineStepIdx, nextStepIdx, nextTaskID)
}

// FailTask 将任务标记为失败，记录错误信息
func (h *Hub) FailTask(taskID, errMsg string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	task, ok := h.tasks[taskID]
	if !ok {
		return ErrTaskNotFound
	}
	task.Status = TaskStatusFailed
	task.Error = errMsg
	task.UpdatedAt = time.Now()
	now := time.Now()
	task.DiscussionClosedAt = &now
	snapshot := *task
	h.publishLocked(Event{Type: EventTypeTaskUpdated, TaskID: taskID, Data: snapshot})
	h.releaseOwnerLocked(task)
	if err := h.store.SaveTask(snapshot); err != nil {
		log.Printf("[Hub] SaveTask %s (fail): %v", taskID, err)
	}
	return nil
}

// GetTask 返回任务快照
func (h *Hub) GetTask(taskID string) (Task, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	task, ok := h.tasks[taskID]
	if !ok {
		return Task{}, ErrTaskNotFound
	}
	return *task, nil
}

// ListTasks 返回任务快照列表，可选按 projectID 过滤
func (h *Hub) ListTasks(projectID ...string) []Task {
	h.mu.RLock()
	defer h.mu.RUnlock()
	pid := ""
	if len(projectID) > 0 {
		pid = projectID[0]
	}
	result := make([]Task, 0, len(h.tasks))
	for _, t := range h.tasks {
		if pid == "" || t.ProjectID == pid {
			result = append(result, *t)
		}
	}
	return result
}

// GetConversation 返回指定任务的对话记录（按时间顺序）
func (h *Hub) GetConversation(taskID string) []ConversationEntry {
	h.mu.RLock()
	defer h.mu.RUnlock()
	src := h.conversations[taskID]
	result := make([]ConversationEntry, len(src))
	copy(result, src)
	return result
}

// UpdateTaskSummary updates the pinned summary for a task while it is still open.
func (h *Hub) UpdateTaskSummary(taskID, summary string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	task, ok := h.tasks[taskID]
	if !ok {
		return ErrTaskNotFound
	}
	if !taskDiscussionOpen(task) {
		return fmt.Errorf("task %s discussion is closed", taskID)
	}
	task.Summary = summary
	task.UpdatedAt = time.Now()
	snapshot := *task
	h.publishLocked(Event{Type: EventTypeTaskUpdated, TaskID: taskID, Data: snapshot})
	if err := h.store.SaveTask(snapshot); err != nil {
		log.Printf("[Hub] SaveTask %s (summary): %v", taskID, err)
	}
	return nil
}

// UpdateTaskArtifact records the final artifact path for a task.
func (h *Hub) UpdateTaskArtifact(taskID, artifactPath string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	task, ok := h.tasks[taskID]
	if !ok {
		return ErrTaskNotFound
	}
	task.ArtifactPath = artifactPath
	task.UpdatedAt = time.Now()
	snapshot := *task
	h.publishLocked(Event{Type: EventTypeTaskUpdated, TaskID: taskID, Data: snapshot})
	if err := h.store.SaveTask(snapshot); err != nil {
		log.Printf("[Hub] SaveTask %s (artifact): %v", taskID, err)
	}
	return nil
}

func taskDiscussionOpen(task *Task) bool {
	switch task.Status {
	case TaskStatusCompleted, TaskStatusFailed, TaskStatusValidated:
		return false
	default:
		return true
	}
}

// EnsureTaskOpen returns an error when the task is already completed or failed.
func (h *Hub) EnsureTaskOpen(taskID string) error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	task, ok := h.tasks[taskID]
	if !ok {
		return ErrTaskNotFound
	}
	if !taskDiscussionOpen(task) {
		return fmt.Errorf("task %s discussion is closed", taskID)
	}
	return nil
}
