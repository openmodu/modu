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
	// swarmQueue holds task IDs waiting to be claimed by an agent (FIFO).
	swarmQueue []string
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
