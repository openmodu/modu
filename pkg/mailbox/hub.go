package mailbox

import (
	"errors"
	"log"
	"sync"
	"time"
)

var ErrAgentNotFound = errors.New("agent not found")

// Hub 管理所有 Agent 的注册状态和消息队列
type Hub struct {
	mu       sync.RWMutex
	inboxes  map[string]chan string
	lastSeen map[string]time.Time
}

func NewHub() *Hub {
	h := &Hub{
		inboxes:  make(map[string]chan string),
		lastSeen: make(map[string]time.Time),
	}
	// 启动后台定时器处理驱逐
	go h.evictionLoop()
	return h
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
			// 关闭并清理对应的 inbox channel
			if ch, ok := h.inboxes[id]; ok {
				close(ch)
			}
			delete(h.inboxes, id)
			delete(h.lastSeen, id)
			log.Printf("[Hub] Agent %s evicted due to timeout", id)
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
}

// Heartbeat 处理心跳请求，刷新最后在线时间
func (h *Hub) Heartbeat(agentID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.inboxes[agentID]; !ok {
		return ErrAgentNotFound // 如果已经被驱逐，返回错误通知它重新注册
	}
	h.lastSeen[agentID] = time.Now()
	return nil
}

// Send 向指定 Agent 的信箱投递消息
func (h *Hub) Send(targetID, message string) error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	
	ch, exists := h.inboxes[targetID]
	if !exists {
		return ErrAgentNotFound
	}

	// 非阻塞写入，如果信箱满了直接丢弃或返回错误
	select {
	case ch <- message:
		return nil
	default:
		return errors.New("agent inbox is full")
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
			return "", false // channel is closed
		}
		return msg, true
	default:
		return "", false // 没有新消息
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
			// 避免阻塞，满了就丢弃
		}
	}
}
