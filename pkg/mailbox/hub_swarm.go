package mailbox

import (
	"fmt"
	"log"
	"sync/atomic"
	"time"
)

// ── Swarm queue ───────────────────────────────────────────────────────────────

// SetCapabilities sets the capability list for an agent (used for swarm task matching).
func (h *Hub) SetCapabilities(agentID string, caps []string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	info, ok := h.agentInfos[agentID]
	if !ok {
		return ErrAgentNotFound
	}
	info.Capabilities = caps
	snapshot := *info
	h.publishLocked(Event{Type: EventTypeAgentUpdated, AgentID: agentID, Data: snapshot})
	return nil
}

// agentHasCaps 检查 agent 的能力是否满足任务要求
func agentHasCaps(agentCaps, required []string) bool {
	if len(required) == 0 {
		return true
	}
	capSet := make(map[string]bool, len(agentCaps))
	for _, c := range agentCaps {
		capSet[c] = true
	}
	for _, req := range required {
		if !capSet[req] {
			return false
		}
	}
	return true
}

// PublishTask adds a task to the shared swarm queue with no pre-assigned agent.
// Unlike CreateTask, creatorID does not need to be a registered agent, so external
// systems can inject tasks directly.
func (h *Hub) PublishTask(creatorID, description string, requiredCaps ...string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	id := h.nextTaskID()
	now := time.Now()
	task := &Task{
		ID:           id,
		Description:  description,
		CreatedBy:    creatorID,
		Assignees:    []string{},
		AgentResults: make(map[string]string),
		Status:       TaskStatusPending,
		SwarmOrigin:  true,
		RequiredCaps: requiredCaps,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	h.tasks[id] = task
	h.swarmQueue = append(h.swarmQueue, id)
	snapshot := *task
	h.publishLocked(Event{Type: EventTypeSwarmTaskPublished, TaskID: id, Data: snapshot})
	if err := h.store.SaveTask(snapshot); err != nil {
		log.Printf("[Hub] SaveTask %s (publish): %v", id, err)
	}
	return id, nil
}

// ClaimTask atomically claims the first task in the swarm queue whose required
// capabilities are satisfied by the given agent. Returns (task, true) on success,
// or (Task{}, false) when the queue is empty or no task matches the agent's capabilities.
func (h *Hub) ClaimTask(agentID string) (Task, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	info, ok := h.agentInfos[agentID]
	if !ok {
		return Task{}, false
	}
	// Reject claim attempts from agents that are already handling a task.
	if info.Status == "busy" {
		return Task{}, false
	}
	agentCaps := info.Capabilities

	// 在队列中找第一个匹配的任务
	claimIdx := -1
	for i, taskID := range h.swarmQueue {
		task, exists := h.tasks[taskID]
		if !exists {
			continue
		}
		if agentHasCaps(agentCaps, task.RequiredCaps) {
			claimIdx = i
			break
		}
	}
	if claimIdx == -1 {
		return Task{}, false
	}

	// 从队列中移除
	taskID := h.swarmQueue[claimIdx]
	h.swarmQueue = append(h.swarmQueue[:claimIdx], h.swarmQueue[claimIdx+1:]...)

	task := h.tasks[taskID]
	// 分配给 agent
	task.Assignees = []string{agentID}
	task.OwnerID = agentID
	task.AssignedTo = agentID
	task.Status = TaskStatusRunning
	task.UpdatedAt = time.Now()
	// 同步更新 agent 状态
	info.Status = "busy"
	info.CurrentTask = taskID

	snapshot := *task
	h.publishLocked(Event{Type: EventTypeSwarmTaskClaimed, TaskID: taskID, AgentID: agentID, Data: snapshot})
	h.publishLocked(Event{Type: EventTypeAgentUpdated, AgentID: agentID, Data: *info})
	if err := h.store.SaveTask(snapshot); err != nil {
		log.Printf("[Hub] SaveTask %s (claim): %v", taskID, err)
	}
	return snapshot, true
}

// SwarmQueueLen returns the number of tasks currently waiting in the swarm queue.
func (h *Hub) SwarmQueueLen() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.swarmQueue)
}

// ListSwarmQueue returns a snapshot of all tasks currently waiting in the swarm queue.
func (h *Hub) ListSwarmQueue() []Task {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make([]Task, 0, len(h.swarmQueue))
	for _, id := range h.swarmQueue {
		if task, ok := h.tasks[id]; ok {
			result = append(result, *task)
		}
	}
	return result
}

// ── Pipeline ──────────────────────────────────────────────────────────────────

func (h *Hub) nextPipelineID() string {
	n := atomic.AddUint64(&h.pipelineCounter, 1)
	return fmt.Sprintf("pipe-%d", n)
}

// PublishPipeline creates a Pipeline from the given steps and enqueues the first
// step as a swarm task. Each step's DescriptionTemplate may reference
// {{.PrevResult}} which is replaced at runtime with the preceding step's result.
// Requires at least 2 steps.
func (h *Hub) PublishPipeline(creatorID string, steps []PipelineStep) (string, error) {
	if len(steps) < 2 {
		return "", fmt.Errorf("pipeline requires at least 2 steps, got %d", len(steps))
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	pipelineID := h.nextPipelineID()
	now := time.Now()

	pipeline := &Pipeline{
		ID:        pipelineID,
		CreatorID: creatorID,
		Steps:     steps,
		Status:    "running",
		Results:   []string{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	h.pipelines[pipelineID] = pipeline

	// Build the first task.
	taskID := h.nextTaskID()
	var nextTemplate string
	var nextCaps []string
	if len(steps) > 1 {
		nextTemplate = steps[1].DescriptionTemplate
		nextCaps = steps[1].RequiredCaps
	}
	task := &Task{
		ID:               taskID,
		Description:      steps[0].DescriptionTemplate,
		CreatedBy:        pipelineID,
		Assignees:        []string{},
		AgentResults:     make(map[string]string),
		Status:           TaskStatusPending,
		SwarmOrigin:      true,
		RequiredCaps:     steps[0].RequiredCaps,
		PipelineID:       pipelineID,
		PipelineStepIdx:  0,
		NextStepTemplate: nextTemplate,
		NextStepCaps:     nextCaps,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	h.tasks[taskID] = task
	h.swarmQueue = append(h.swarmQueue, taskID)

	h.publishLocked(Event{
		Type:       EventTypePipelineStarted,
		PipelineID: pipelineID,
		TaskID:     taskID,
		Data:       *pipeline,
	})
	if err := h.store.SaveTask(*task); err != nil {
		log.Printf("[Hub] SaveTask %s (pipeline step 0): %v", taskID, err)
	}
	log.Printf("[Hub] Pipeline %s started (%d steps), first task %s enqueued", pipelineID, len(steps), taskID)
	return pipelineID, nil
}

// GetPipeline returns a snapshot of the Pipeline with the given ID.
func (h *Hub) GetPipeline(pipelineID string) (Pipeline, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	p, ok := h.pipelines[pipelineID]
	if !ok {
		return Pipeline{}, false
	}
	return *p, true
}

// ListPipelines returns a snapshot of all known pipelines.
func (h *Hub) ListPipelines() []Pipeline {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make([]Pipeline, 0, len(h.pipelines))
	for _, p := range h.pipelines {
		result = append(result, *p)
	}
	return result
}
