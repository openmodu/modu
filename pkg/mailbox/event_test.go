package mailbox

import (
	"testing"
	"time"
)

func drainEvent(ch <-chan Event, timeout time.Duration) (Event, bool) {
	select {
	case e := <-ch:
		return e, true
	case <-time.After(timeout):
		return Event{}, false
	}
}

func TestSubscribeReceivesAgentRegistered(t *testing.T) {
	h := NewHub()
	sub := h.Subscribe()
	defer h.Unsubscribe(sub)

	h.Register("agent-x")

	e, ok := drainEvent(sub, 100*time.Millisecond)
	if !ok {
		t.Fatal("expected EventTypeAgentRegistered event")
	}
	if e.Type != EventTypeAgentRegistered {
		t.Errorf("expected agent.registered, got %s", e.Type)
	}
	if e.AgentID != "agent-x" {
		t.Errorf("expected AgentID=agent-x, got %s", e.AgentID)
	}
}

func TestSubscribeReceivesAgentUpdated(t *testing.T) {
	h := NewHub()
	h.Register("agent-y")

	sub := h.Subscribe()
	defer h.Unsubscribe(sub)

	_ = h.SetAgentRole("agent-y", "worker")

	e, ok := drainEvent(sub, 100*time.Millisecond)
	if !ok {
		t.Fatal("expected AgentUpdated event")
	}
	if e.Type != EventTypeAgentUpdated || e.AgentID != "agent-y" {
		t.Errorf("unexpected event: %+v", e)
	}
}

func TestSubscribeReceivesTaskEvents(t *testing.T) {
	h := NewHub()
	h.Register("creator")
	h.Register("worker")

	sub := h.Subscribe()
	defer h.Unsubscribe(sub)

	taskID, _ := h.CreateTask("creator", "test task")
	e, ok := drainEvent(sub, 100*time.Millisecond)
	if !ok {
		t.Fatal("expected task.created event")
	}
	if e.Type != EventTypeTaskCreated || e.TaskID != taskID {
		t.Errorf("unexpected event: %+v", e)
	}

	_ = h.AssignTask(taskID, "worker")
	e, ok = drainEvent(sub, 100*time.Millisecond)
	if !ok {
		t.Fatal("expected task.updated event after AssignTask")
	}
	if e.Type != EventTypeTaskUpdated {
		t.Errorf("expected task.updated, got %s", e.Type)
	}

	_ = h.StartTask(taskID)
	e, ok = drainEvent(sub, 100*time.Millisecond)
	if !ok {
		t.Fatal("expected task.updated event after StartTask")
	}
	if e.Type != EventTypeTaskUpdated {
		t.Errorf("expected task.updated, got %s", e.Type)
	}

	_ = h.CompleteTask(taskID, "", "done")
	e, ok = drainEvent(sub, 100*time.Millisecond)
	if !ok {
		t.Fatal("expected task.updated event after CompleteTask")
	}
	task, ok2 := e.Data.(Task)
	if !ok2 || task.Status != TaskStatusCompleted {
		t.Errorf("event data should be completed Task, got %+v", e.Data)
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	h := NewHub()
	h.Register("agent-z")

	sub := h.Subscribe()
	h.Unsubscribe(sub)

	// channel should be closed after unsubscribe
	select {
	case _, ok := <-sub:
		if ok {
			t.Error("expected closed channel after Unsubscribe")
		}
	default:
		t.Error("channel should be closed, not just empty")
	}
}

func TestMultipleSubscribers(t *testing.T) {
	h := NewHub()
	h.Register("agent-m")

	sub1 := h.Subscribe()
	sub2 := h.Subscribe()
	defer h.Unsubscribe(sub1)
	defer h.Unsubscribe(sub2)

	_ = h.SetAgentRole("agent-m", "tester")

	e1, ok1 := drainEvent(sub1, 100*time.Millisecond)
	e2, ok2 := drainEvent(sub2, 100*time.Millisecond)

	if !ok1 || !ok2 {
		t.Fatal("both subscribers should receive the event")
	}
	if e1.Type != EventTypeAgentUpdated || e2.Type != EventTypeAgentUpdated {
		t.Errorf("unexpected event types: %s, %s", e1.Type, e2.Type)
	}
}

func TestPublishNonBlockingOnSlowSubscriber(t *testing.T) {
	h := NewHub()
	h.Register("sender")

	// 订阅者但不消费，填满其 buffer
	sub := h.Subscribe()
	defer h.Unsubscribe(sub)

	// 触发大量事件，不应阻塞
	done := make(chan struct{})
	go func() {
		for range 300 {
			_ = h.SetAgentRole("sender", "role")
		}
		close(done)
	}()

	select {
	case <-done:
		// OK，非阻塞
	case <-time.After(2 * time.Second):
		t.Fatal("publish blocked on slow subscriber")
	}
}
