package mailbox

import (
	"log"
	"time"
)

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
	h.PostForumMessageKind(fromID, taskID, ConversationKindGeneral, text)
}

// PostForumMessageKind appends a categorized forum message to a task thread.
func (h *Hub) PostForumMessageKind(fromID, taskID string, kind ConversationKind, text string) {
	h.postForumMessageKind(fromID, taskID, kind, text, false)
}

// ForcePostForumMessageKind appends a forum message even after discussion closes.
// It should only be used for final delivery/system tail messages.
func (h *Hub) ForcePostForumMessageKind(fromID, taskID string, kind ConversationKind, text string) {
	h.postForumMessageKind(fromID, taskID, kind, text, true)
}

func (h *Hub) postForumMessageKind(fromID, taskID string, kind ConversationKind, text string, allowClosed bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	task, ok := h.tasks[taskID]
	if !ok || (!allowClosed && !taskDiscussionOpen(task)) {
		return
	}
	if kind == "" {
		kind = ConversationKindGeneral
	}
	entry := ConversationEntry{
		At:      time.Now(),
		From:    fromID,
		To:      "",
		TaskID:  taskID,
		MsgType: MessageTypeChat,
		Kind:    kind,
		Content: text,
	}
	h.conversations[taskID] = append(h.conversations[taskID], entry)
	h.publishLocked(Event{
		Type:   EventTypeConversationAdded,
		TaskID: taskID,
		Data:   entry,
	})
	if err := h.store.SaveConversation(entry); err != nil {
		log.Printf("[Hub] SaveConversation (forum) task=%s: %v", taskID, err)
	}
}
