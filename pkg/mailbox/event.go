package mailbox

// EventType 表示 Hub 内部事件类型
type EventType string

const (
	EventTypeAgentRegistered EventType = "agent.registered"
	EventTypeAgentEvicted    EventType = "agent.evicted"
	EventTypeAgentUpdated    EventType = "agent.updated"
	EventTypeTaskCreated          EventType = "task.created"
	EventTypeTaskUpdated          EventType = "task.updated"
	EventTypeConversationAdded    EventType = "conversation.added"
)

// Event 是 Hub 向订阅者推送的状态变更通知
type Event struct {
	Type    EventType `json:"type"`
	AgentID string    `json:"agent_id,omitempty"`
	TaskID  string    `json:"task_id,omitempty"`
	Data    any       `json:"data,omitempty"`
}

// Subscribe 返回一个只读事件 channel，Hub 状态变更时会向其推送事件。
// 调用方负责在不再需要时调用 Unsubscribe 以避免资源泄漏。
func (h *Hub) Subscribe() <-chan Event {
	ch := make(chan Event, 256)
	h.mu.Lock()
	h.subscribers = append(h.subscribers, ch)
	h.mu.Unlock()
	return ch
}

// Unsubscribe 取消订阅并关闭对应的事件 channel
func (h *Hub) Unsubscribe(sub <-chan Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, ch := range h.subscribers {
		if ch == sub {
			h.subscribers = append(h.subscribers[:i], h.subscribers[i+1:]...)
			close(ch)
			return
		}
	}
}

// publishLocked 向所有订阅者非阻塞推送事件（调用方持有写锁）
func (h *Hub) publishLocked(e Event) {
	for _, ch := range h.subscribers {
		select {
		case ch <- e:
		default:
			// 订阅者消费过慢，丢弃该事件
		}
	}
}
