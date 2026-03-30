package mailbox

import (
	"encoding/json"
	"errors"
	"log"
	"time"
)

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
	kind := ConversationKindGeneral
	// 对常见类型提取可读文本
	switch msg.Type {
	case MessageTypeTaskAssign:
		var p TaskAssignPayload
		if json.Unmarshal(msg.Payload, &p) == nil {
			content = p.Description
		}
		kind = ConversationKindDecision
	case MessageTypeTaskResult:
		var p TaskResultPayload
		if json.Unmarshal(msg.Payload, &p) == nil {
			if p.Error != "" {
				content = "error: " + p.Error
				kind = ConversationKindRisk
			} else {
				content = p.Result
				kind = ConversationKindDeliverable
			}
		}
	case MessageTypeChat:
		var p ChatPayload
		if json.Unmarshal(msg.Payload, &p) == nil {
			content = p.Text
		}
		kind = ConversationKindGeneral
	}
	entry := ConversationEntry{
		At:      time.Now(),
		From:    from,
		To:      to,
		TaskID:  msg.TaskID,
		MsgType: msg.Type,
		Kind:    kind,
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
