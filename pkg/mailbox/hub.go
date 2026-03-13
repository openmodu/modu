package mailbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrAgentNotFound = errors.New("agent not found")
	ErrTaskNotFound  = errors.New("task not found")
)

// TaskStatus 表示任务的生命周期状态
type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
)

// AgentInfo 包含 Agent 的角色、状态和当前任务信息
type AgentInfo struct {
	ID          string    `json:"id"`
	Role        string    `json:"role"`
	Status      string    `json:"status"`      // "idle" | "busy"
	CurrentTask string    `json:"current_task"` // task ID，空表示空闲
	LastSeen    time.Time `json:"last_seen"`
}

// Task 表示一个可追踪的工作单元
type Task struct {
	ID          string     `json:"id"`
	Description string     `json:"description"`
	CreatedBy   string     `json:"created_by"`
	AssignedTo  string     `json:"assigned_to"`
	Status      TaskStatus `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	Result      string     `json:"result"`
	Error       string     `json:"error"`
}

// Hub 管理所有 Agent 的注册状态和消息队列
type Hub struct {
	mu            sync.RWMutex
	inboxes       map[string]chan string
	lastSeen      map[string]time.Time
	agentInfos    map[string]*AgentInfo
	tasks         map[string]*Task
	taskCounter   uint64
	subscribers   []chan Event
	store         Store
	// knownRoles 缓存从 store 加载的角色，在 agent 注册时自动应用
	knownRoles    map[string]string
	// conversations 按 task_id 存储对话记录
	conversations map[string][]ConversationEntry
}

// HubOption 是 NewHub 的函数式选项
type HubOption func(*Hub)

// WithStore 为 Hub 绑定一个持久化后端
func WithStore(s Store) HubOption {
	return func(h *Hub) { h.store = s }
}

func NewHub(opts ...HubOption) *Hub {
	h := &Hub{
		inboxes:       make(map[string]chan string),
		lastSeen:      make(map[string]time.Time),
		agentInfos:    make(map[string]*AgentInfo),
		tasks:         make(map[string]*Task),
		knownRoles:    make(map[string]string),
		conversations: make(map[string][]ConversationEntry),
		store:         noopStore{},
	}
	for _, opt := range opts {
		opt(h)
	}
	h.loadFromStore()
	go h.evictionLoop()
	return h
}

// loadFromStore 从持久化后端恢复任务和 agent 角色
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
			if _, err := fmt.Sscanf(t.ID, "task-%d", &n); err == nil && n > h.taskCounter {
				h.taskCounter = n
			}
		}
		if len(tasks) > 0 {
			log.Printf("[Hub] loaded %d tasks from store", len(tasks))
		}
	}

	roles, err := h.store.LoadAgentRoles()
	if err != nil {
		log.Printf("[Hub] load agent roles from store: %v", err)
	} else if roles != nil {
		h.knownRoles = roles
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

// Register 注册一个 Agent，为其分配一个容量为 100 的缓冲信箱并更新活跃时间
func (h *Hub) Register(agentID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.inboxes[agentID]; !exists {
		h.inboxes[agentID] = make(chan string, 100)
	}
	h.lastSeen[agentID] = time.Now()
	if _, exists := h.agentInfos[agentID]; !exists {
		role := h.knownRoles[agentID] // 恢复持久化的角色
		h.agentInfos[agentID] = &AgentInfo{
			ID:       agentID,
			Role:     role,
			Status:   "idle",
			LastSeen: time.Now(),
		}
	}
	info := *h.agentInfos[agentID]
	h.publishLocked(Event{Type: EventTypeAgentRegistered, AgentID: agentID, Data: info})
}

// Heartbeat 处理心跳请求，刷新最后在线时间
func (h *Hub) Heartbeat(agentID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.inboxes[agentID]; !ok {
		return ErrAgentNotFound
	}
	h.lastSeen[agentID] = time.Now()
	if info, ok := h.agentInfos[agentID]; ok {
		info.LastSeen = time.Now()
	}
	return nil
}

// Send 向指定 Agent 的信箱投递消息；若消息携带 task_id 则自动记录至对话日志
func (h *Hub) Send(targetID, message string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	ch, exists := h.inboxes[targetID]
	if !exists {
		return ErrAgentNotFound
	}

	select {
	case ch <- message:
	default:
		return errors.New("agent inbox is full")
	}

	// 尝试解析为结构化消息，有 task_id 则记录对话
	var msg Message
	if err := json.Unmarshal([]byte(message), &msg); err == nil && msg.TaskID != "" {
		h.appendConversationLocked(msg.From, targetID, msg)
	}
	return nil
}

// appendConversationLocked 追加一条对话记录（调用方持有写锁）
func (h *Hub) appendConversationLocked(from, to string, msg Message) {
	content := string(msg.Payload)
	// 对常见类型提取可读文本
	switch msg.Type {
	case MessageTypeTaskAssign:
		var p TaskAssignPayload
		if json.Unmarshal(msg.Payload, &p) == nil {
			content = p.Description
		}
	case MessageTypeTaskResult:
		var p TaskResultPayload
		if json.Unmarshal(msg.Payload, &p) == nil {
			if p.Error != "" {
				content = "error: " + p.Error
			} else {
				content = p.Result
			}
		}
	case MessageTypeChat:
		var p ChatPayload
		if json.Unmarshal(msg.Payload, &p) == nil {
			content = p.Text
		}
	}
	entry := ConversationEntry{
		At:      time.Now(),
		From:    from,
		To:      to,
		TaskID:  msg.TaskID,
		MsgType: msg.Type,
		Content: content,
	}
	h.conversations[msg.TaskID] = append(h.conversations[msg.TaskID], entry)
	h.publishLocked(Event{
		Type:   EventTypeConversationAdded,
		TaskID: msg.TaskID,
		Data:   entry,
	})
}

// Recv 尝试从信箱中非阻塞读取一条消息
func (h *Hub) Recv(agentID string) (string, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	ch, exists := h.inboxes[agentID]
	if !exists {
		return "", false
	}

	select {
	case msg, ok := <-ch:
		if !ok {
			return "", false
		}
		return msg, true
	default:
		return "", false
	}
}

// ListAgents 返回当前所有注册的 Agent 列表
func (h *Hub) ListAgents() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	agents := make([]string, 0, len(h.inboxes))
	for id := range h.inboxes {
		agents = append(agents, id)
	}
	return agents
}

// Broadcast 向所有注册的 Agent 的信箱投递消息
func (h *Hub) Broadcast(message string) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, ch := range h.inboxes {
		select {
		case ch <- message:
		default:
		}
	}
}

// --- AgentInfo 管理 ---

// SetAgentRole 设置 Agent 的角色
func (h *Hub) SetAgentRole(agentID, role string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	info, ok := h.agentInfos[agentID]
	if !ok {
		return ErrAgentNotFound
	}
	info.Role = role
	h.knownRoles[agentID] = role
	snapshot := *info
	h.publishLocked(Event{Type: EventTypeAgentUpdated, AgentID: agentID, Data: snapshot})
	if err := h.store.SaveAgentRole(agentID, role); err != nil {
		log.Printf("[Hub] SaveAgentRole %s: %v", agentID, err)
	}
	return nil
}

// SetAgentStatus 设置 Agent 的状态，taskID 为当前任务 ID（空闲时传空字符串）
func (h *Hub) SetAgentStatus(agentID, status, taskID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	info, ok := h.agentInfos[agentID]
	if !ok {
		return ErrAgentNotFound
	}
	info.Status = status
	info.CurrentTask = taskID
	snapshot := *info
	h.publishLocked(Event{Type: EventTypeAgentUpdated, AgentID: agentID, Data: snapshot})
	return nil
}

// GetAgentInfo 返回 Agent 的元数据快照
func (h *Hub) GetAgentInfo(agentID string) (AgentInfo, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	info, ok := h.agentInfos[agentID]
	if !ok {
		return AgentInfo{}, ErrAgentNotFound
	}
	return *info, nil
}

// ListAgentInfos 返回所有 Agent 的元数据列表
func (h *Hub) ListAgentInfos() []AgentInfo {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make([]AgentInfo, 0, len(h.agentInfos))
	for _, info := range h.agentInfos {
		result = append(result, *info)
	}
	return result
}

// --- Task 管理 ---

func (h *Hub) nextTaskID() string {
	n := atomic.AddUint64(&h.taskCounter, 1)
	return fmt.Sprintf("task-%d", n)
}

// CreateTask 创建一个新任务，返回 task ID
func (h *Hub) CreateTask(creatorID, description string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.agentInfos[creatorID]; !ok {
		return "", ErrAgentNotFound
	}
	id := h.nextTaskID()
	now := time.Now()
	task := &Task{
		ID:          id,
		Description: description,
		CreatedBy:   creatorID,
		Status:      TaskStatusPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	h.tasks[id] = task
	snapshot := *task
	h.publishLocked(Event{Type: EventTypeTaskCreated, TaskID: id, Data: snapshot})
	if err := h.store.SaveTask(snapshot); err != nil {
		log.Printf("[Hub] SaveTask %s (create): %v", id, err)
	}
	return id, nil
}

// AssignTask 将任务分配给指定 Agent
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
	task.AssignedTo = agentID
	task.UpdatedAt = time.Now()
	snapshot := *task
	h.publishLocked(Event{Type: EventTypeTaskUpdated, TaskID: taskID, Data: snapshot})
	if err := h.store.SaveTask(snapshot); err != nil {
		log.Printf("[Hub] SaveTask %s (assign): %v", taskID, err)
	}
	return nil
}

// StartTask 将任务状态设为 running
func (h *Hub) StartTask(taskID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	task, ok := h.tasks[taskID]
	if !ok {
		return ErrTaskNotFound
	}
	task.Status = TaskStatusRunning
	task.UpdatedAt = time.Now()
	snapshot := *task
	h.publishLocked(Event{Type: EventTypeTaskUpdated, TaskID: taskID, Data: snapshot})
	if err := h.store.SaveTask(snapshot); err != nil {
		log.Printf("[Hub] SaveTask %s (start): %v", taskID, err)
	}
	return nil
}

// CompleteTask 将任务标记为已完成，记录结果
func (h *Hub) CompleteTask(taskID, result string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	task, ok := h.tasks[taskID]
	if !ok {
		return ErrTaskNotFound
	}
	task.Status = TaskStatusCompleted
	task.Result = result
	task.UpdatedAt = time.Now()
	snapshot := *task
	h.publishLocked(Event{Type: EventTypeTaskUpdated, TaskID: taskID, Data: snapshot})
	if err := h.store.SaveTask(snapshot); err != nil {
		log.Printf("[Hub] SaveTask %s (complete): %v", taskID, err)
	}
	return nil
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
	snapshot := *task
	h.publishLocked(Event{Type: EventTypeTaskUpdated, TaskID: taskID, Data: snapshot})
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

// ListTasks 返回所有任务快照
func (h *Hub) ListTasks() []Task {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make([]Task, 0, len(h.tasks))
	for _, t := range h.tasks {
		result = append(result, *t)
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
