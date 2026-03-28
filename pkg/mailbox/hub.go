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
	ErrAgentNotFound   = errors.New("agent not found")
	ErrTaskNotFound    = errors.New("task not found")
	ErrProjectNotFound = errors.New("project not found")
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
	Status      string    `json:"status"`       // "idle" | "busy"
	CurrentTask string    `json:"current_task"` // task ID，空表示空闲
	LastSeen    time.Time `json:"last_seen"`
}

// Project 表示一次多任务协作的集合（一次创作运行）
type Project struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedBy string    `json:"created_by"`
	TaskIDs   []string  `json:"task_ids"`
	Status    string    `json:"status"` // "active" | "completed"
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Task 表示一个可追踪的工作单元，支持多 Agent 并行执行
type Task struct {
	ID           string            `json:"id"`
	ProjectID    string            `json:"project_id,omitempty"`
	Description  string            `json:"description"`
	CreatedBy    string            `json:"created_by"`
	AssignedTo   string            `json:"assigned_to"`          // 向后兼容，= Assignees[0]
	Assignees    []string          `json:"assignees"`            // 全部指派的 agent
	Status       TaskStatus        `json:"status"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
	Result       string            `json:"result"`
	AgentResults map[string]string `json:"agent_results,omitempty"` // 每个 agent 的成果
	Error        string            `json:"error"`
}

// Hub 管理所有 Agent 的注册状态和消息队列
type Hub struct {
	mu             sync.RWMutex
	inboxes        map[string]chan string
	lastSeen       map[string]time.Time
	agentInfos     map[string]*AgentInfo
	tasks          map[string]*Task
	taskCounter    uint64
	projects       map[string]*Project
	projectCounter uint64
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
}

// HubOption 是 NewHub 的函数式选项
type HubOption func(*Hub)

// WithStore 为 Hub 绑定一个持久化后端
func WithStore(s Store) HubOption {
	return func(h *Hub) { h.store = s }
}

func NewHub(opts ...HubOption) *Hub {
	h := &Hub{
		inboxes:         make(map[string]chan string),
		lastSeen:        make(map[string]time.Time),
		agentInfos:      make(map[string]*AgentInfo),
		tasks:           make(map[string]*Task),
		projects:        make(map[string]*Project),
		knownRoles:      make(map[string]string),
		conversations:   make(map[string][]ConversationEntry),
		delegateWaiters: make(map[string]chan string),
		store:           noopStore{},
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
	// 在锁外解析 JSON，避免持写锁期间做 CPU/内存密集操作
	var msg Message
	// delegate 消息由 PostForumMessage 以可读形式单独记录，不在此重复记录原始 payload
	hasConv := json.Unmarshal([]byte(message), &msg) == nil &&
		msg.TaskID != "" &&
		msg.Type != MessageTypeDelegate

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

	if hasConv {
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
	if err := h.store.SaveConversation(entry); err != nil {
		log.Printf("[Hub] SaveConversation task=%s: %v", msg.TaskID, err)
	}
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
	task.AssignedTo = task.Assignees[0] // 向后兼容：始终指向第一个 agent
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

// CompleteTask 记录某 agent 对任务的完成成果。
//
// 当 agentID 非空时，记录该 agent 的成果；当所有指派的 agent 均完成时任务标记为 completed。
// 当 agentID 为空时（向后兼容旧单 agent 调用方式），若任务有多个 assignee 则返回错误，
// 否则立即将任务标记为 completed。
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
	task.UpdatedAt = time.Now()

	if agentID != "" {
		task.AgentResults[agentID] = result
		// 所有指派 agent 均完成，或尚未指派任何 agent，才标记 completed
		if len(task.Assignees) == 0 || len(task.AgentResults) >= len(task.Assignees) {
			task.Status = TaskStatusCompleted
		}
	} else {
		// 旧调用方式（无 agentID）：多 assignee 任务必须用带 agentID 的新接口
		if len(task.Assignees) > 1 {
			return fmt.Errorf("task %s has %d assignees: use CompleteTask with agentID", taskID, len(task.Assignees))
		}
		task.Status = TaskStatusCompleted
	}

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

// ── Delegate 信令 ─────────────────────────────────────────────────────────────

// delegateKey 生成委托等待的唯一键
func delegateKey(taskID, workerID, delegatorID string) string {
	return taskID + "::" + workerID + "::" + delegatorID
}

// RegisterDelegate 为一次委托注册结果等待通道，返回供调用方阻塞读取的 channel。
// workerID 是被委托方，delegatorID 是发起委托方。
func (h *Hub) RegisterDelegate(taskID, workerID, delegatorID string) chan string {
	ch := make(chan string, 1)
	h.delegateMu.Lock()
	h.delegateWaiters[delegateKey(taskID, workerID, delegatorID)] = ch
	h.delegateMu.Unlock()
	return ch
}

// PostDelegateResult 将委托结果写入等待通道，唤醒 RegisterDelegate 的调用方。
// 返回 true 表示找到了等待的调用方；false 表示无人等待（已超时或键不存在）。
func (h *Hub) PostDelegateResult(taskID, workerID, delegatorID, result string) bool {
	key := delegateKey(taskID, workerID, delegatorID)
	h.delegateMu.Lock()
	ch, ok := h.delegateWaiters[key]
	if ok {
		delete(h.delegateWaiters, key)
	}
	h.delegateMu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- result:
	default:
	}
	return true
}

// PostForumMessage 向指定任务的论坛发布一条消息（不发往任何 agent 信箱，仅记录在对话日志）。
// 用于系统通知、阶段分隔线等不需要路由给任何 agent 的内容。
func (h *Hub) PostForumMessage(fromID, taskID, text string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	entry := ConversationEntry{
		At:      time.Now(),
		From:    fromID,
		To:      "",
		TaskID:  taskID,
		MsgType: MessageTypeChat,
		Content: text,
	}
	h.conversations[taskID] = append(h.conversations[taskID], entry)
	h.publishLocked(Event{
		Type:   EventTypeConversationAdded,
		TaskID: taskID,
		Data:   entry,
	})
	_ = h.store.SaveConversation(entry)
}
