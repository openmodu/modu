package mailbox

import (
	"errors"
	"sync"
)

var ErrAgentNotFound = errors.New("agent not found")

// Hub 管理所有 Agent 的注册状态和消息队列
type Hub struct {
	mu      sync.RWMutex
	inboxes map[string]chan string
}

func NewHub() *Hub {
	return &Hub{
		inboxes: make(map[string]chan string),
	}
}

// Register 注册一个 Agent，为其分配一个容量为 100 的缓冲信箱
func (h *Hub) Register(agentID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.inboxes[agentID]; !exists {
		h.inboxes[agentID] = make(chan string, 100)
	}
}

// Send 向指定 Agent 的信箱投递消息
func (h *Hub) Send(targetID, message string) error {
	h.mu.RLock()
	ch, exists := h.inboxes[targetID]
	h.mu.RUnlock()

	if !exists {
		return ErrAgentNotFound
	}

	// 非阻塞写入，如果信箱满了直接丢弃或返回错误（这里做简单的防阻塞处理）
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
	ch, exists := h.inboxes[agentID]
	h.mu.RUnlock()

	if !exists {
		return "", false
	}

	select {
	case msg := <-ch:
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

