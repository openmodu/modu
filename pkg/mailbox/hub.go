package mailbox

import (
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// Hub 管理所有 Agent 的注册状态和消息队列
type Hub struct {
	mu             sync.RWMutex
	inboxes        map[string]chan string
	lastSeen       map[string]time.Time
	agentInfos     map[string]*AgentInfo
	tasks            map[string]*Task
	taskCounter      uint64
	projects         map[string]*Project
	projectCounter   uint64
	pipelineCounter  uint64
	subscribers    []chan Event
	store          Store
	// knownRoles 缓存从 store 加载的角色，在 agent 注册时自动应用
	knownRoles map[string]string
	// conversations 按 task_id 存储对话记录
	conversations map[string][]ConversationEntry
	// delegateWaiters 支持 peer-to-peer 委托的结果回传
	// key: "taskID::workerID::delegatorID"
	delegateWaiters map[string]chan string
	delegateMu      sync.Mutex
	// swarmQueue holds task IDs waiting to be claimed by an agent (FIFO).
	swarmQueue []string
	// maxTaskRecoveries is the maximum number of times a swarm task will be
	// automatically re-queued after its owning agent is evicted (default 3).
	maxTaskRecoveries int
	// pipelines tracks active and completed Pipeline chains by ID.
	pipelines map[string]*Pipeline
}

// HubOption 是 NewHub 的函数式选项
type HubOption func(*Hub)

// WithStore 为 Hub 绑定一个持久化后端
func WithStore(s Store) HubOption {
	return func(h *Hub) { h.store = s }
}

// WithMaxTaskRecoveries sets the maximum number of times a swarm task will be
// automatically re-queued after its owning agent is evicted (default 3).
func WithMaxTaskRecoveries(n int) HubOption {
	return func(h *Hub) { h.maxTaskRecoveries = n }
}

func NewHub(opts ...HubOption) *Hub {
	h := &Hub{
		inboxes:           make(map[string]chan string),
		lastSeen:          make(map[string]time.Time),
		agentInfos:        make(map[string]*AgentInfo),
		tasks:             make(map[string]*Task),
		projects:          make(map[string]*Project),
		knownRoles:        make(map[string]string),
		conversations:     make(map[string][]ConversationEntry),
		delegateWaiters:   make(map[string]chan string),
		store:             noopStore{},
		maxTaskRecoveries: 3,
		pipelines:         make(map[string]*Pipeline),
	}
	for _, opt := range opts {
		opt(h)
	}
	h.loadFromStore()
	go h.evictionLoop()
	return h
}

// loadFromStore 从持久化后端恢复任务、项目和 agent 角色
func (h *Hub) loadFromStore() {
	tasks, err := h.store.LoadTasks()
	if err != nil {
		log.Printf("[Hub] load tasks from store: %v", err)
	} else {
		for _, t := range tasks {
			tc := t
			h.tasks[t.ID] = &tc
			// 修正 taskCounter，确保新 ID 不冲突
			var n uint64
			if _, err := fmt.Sscanf(t.ID, "task-%d", &n); err == nil {
				if n > atomic.LoadUint64(&h.taskCounter) {
					atomic.StoreUint64(&h.taskCounter, n)
				}
			}
		}
		if len(tasks) > 0 {
			log.Printf("[Hub] loaded %d tasks from store", len(tasks))
		}
	}

	projects, err := h.store.LoadProjects()
	if err != nil {
		log.Printf("[Hub] load projects from store: %v", err)
	} else {
		for _, p := range projects {
			pc := p
			h.projects[p.ID] = &pc
			var n uint64
			if _, err := fmt.Sscanf(p.ID, "proj-%d", &n); err == nil {
				if n > atomic.LoadUint64(&h.projectCounter) {
					atomic.StoreUint64(&h.projectCounter, n)
				}
			}
		}
		if len(projects) > 0 {
			log.Printf("[Hub] loaded %d projects from store", len(projects))
		}
	}

	roles, err := h.store.LoadAgentRoles()
	if err != nil {
		log.Printf("[Hub] load agent roles from store: %v", err)
	} else if roles != nil {
		h.knownRoles = roles
	}

	// Rebuild the swarm queue: only restore tasks that were originally published via
	// PublishTask / PublishValidatedTask (SwarmOrigin=true).  Regular CreateTask tasks
	// must not be silently promoted to swarm tasks across a restart.
	for _, t := range h.tasks {
		if t.SwarmOrigin && t.Status == TaskStatusPending && len(t.Assignees) == 0 {
			h.swarmQueue = append(h.swarmQueue, t.ID)
		}
	}

	convs, err := h.store.LoadConversations()
	if err != nil {
		log.Printf("[Hub] load conversations from store: %v", err)
	} else if convs != nil {
		h.conversations = convs
		total := 0
		for _, v := range convs {
			total += len(v)
		}
		if total > 0 {
			log.Printf("[Hub] loaded %d conversation entries from store", total)
		}
	}
}

func (h *Hub) evictionLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		h.evictOfflineAgents()
	}
}

func (h *Hub) evictOfflineAgents() {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	for id, last := range h.lastSeen {
		if now.Sub(last) > 30*time.Second {
			// Recover any running task before removing the agent.
			if info, ok := h.agentInfos[id]; ok && info.CurrentTask != "" {
				h.recoverTaskLocked(info.CurrentTask, id)
			}
			if ch, ok := h.inboxes[id]; ok {
				close(ch)
			}
			delete(h.inboxes, id)
			delete(h.lastSeen, id)
			delete(h.agentInfos, id)
			log.Printf("[Hub] Agent %s evicted due to timeout", id)
			h.publishLocked(Event{Type: EventTypeAgentEvicted, AgentID: id})
		}
	}
}

// recoverTaskLocked handles a running task whose owner agent was just evicted.
// Caller must hold h.mu write lock.
func (h *Hub) recoverTaskLocked(taskID, evictedAgentID string) {
	task, ok := h.tasks[taskID]
	if !ok {
		return
	}
	if task.Status != TaskStatusRunning {
		return // already completed or otherwise settled; nothing to do
	}

	now := time.Now()

	// Clear assignment fields so the task can be re-assigned.
	task.OwnerID = ""
	task.AssignedTo = ""
	task.Assignees = nil

	if task.SwarmOrigin && task.RecoveryCount < h.maxTaskRecoveries {
		task.RecoveryCount++
		task.Status = TaskStatusPending
		task.UpdatedAt = now
		h.swarmQueue = append(h.swarmQueue, taskID)
		h.tasks[taskID] = task
		if err := h.store.SaveTask(*task); err != nil {
			log.Printf("[Hub] recoverTask: save task %s: %v", taskID, err)
		}
		h.publishLocked(Event{
			Type:    EventTypeTaskRecovered,
			TaskID:  taskID,
			AgentID: evictedAgentID,
			Data: map[string]int{
				"recovery_count": task.RecoveryCount,
				"max_recoveries": h.maxTaskRecoveries,
			},
		})
		log.Printf("[Hub] Task %s recovered after agent %s evicted (attempt %d/%d)",
			taskID, evictedAgentID, task.RecoveryCount, h.maxTaskRecoveries)
	} else {
		reason := "agent evicted"
		if task.SwarmOrigin {
			reason = "max recoveries exceeded"
		}
		task.Status = TaskStatusFailed
		task.Error = fmt.Sprintf("task failed: %s (agent: %s)", reason, evictedAgentID)
		task.UpdatedAt = now
		h.tasks[taskID] = task
		if err := h.store.SaveTask(*task); err != nil {
			log.Printf("[Hub] recoverTask: save task %s: %v", taskID, err)
		}
		h.publishLocked(Event{Type: EventTypeTaskUpdated, TaskID: taskID})
		log.Printf("[Hub] Task %s failed: %s", taskID, reason)

		// If this task belongs to a pipeline, mark the pipeline failed too so
		// callers polling GetPipeline don't wait forever.
		if task.PipelineID != "" {
			if pipeline, ok := h.pipelines[task.PipelineID]; ok && pipeline.Status == "running" {
				pipeline.Status = "failed"
				pipeline.UpdatedAt = now
				h.publishLocked(Event{
					Type:       EventTypePipelineFailed,
					PipelineID: task.PipelineID,
					TaskID:     taskID,
					StepIdx:    task.PipelineStepIdx,
					Data:       *pipeline,
				})
				log.Printf("[Hub] Pipeline %s failed: step %d task %s could not be recovered",
					task.PipelineID, task.PipelineStepIdx, taskID)
			}
		}
	}
}
