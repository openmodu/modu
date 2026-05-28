package agent

import (
	"context"
	"fmt"
)

func (a *Agent) Continue(ctx context.Context) error {
	a.mu.Lock()
	if a.running != nil {
		a.mu.Unlock()
		return fmt.Errorf("agent is already processing")
	}
	messages := append([]AgentMessage{}, a.state.Messages...)
	if len(messages) == 0 {
		a.mu.Unlock()
		return fmt.Errorf("no messages to continue from")
	}
	if roleOf(messages[len(messages)-1]) == RoleAssistant {
		steering := a.dequeueSteeringLocked()
		if len(steering) > 0 {
			a.mu.Unlock()
			return a.run(ctx, steering)
		}
		followUps := a.dequeueFollowUpLocked()
		if len(followUps) > 0 {
			a.mu.Unlock()
			return a.run(ctx, followUps)
		}
		a.mu.Unlock()
		return fmt.Errorf("cannot continue from message role: assistant")
	}
	a.mu.Unlock()
	return a.run(ctx, nil)
}

func (a *Agent) Steer(message AgentMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.steering = append(a.steering, message)
}

func (a *Agent) FollowUp(message AgentMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.followUp = append(a.followUp, message)
}

func (a *Agent) ClearSteeringQueue() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.steering = []AgentMessage{}
}

func (a *Agent) ClearFollowUpQueue() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.followUp = []AgentMessage{}
}

func (a *Agent) ClearAllQueues() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.steering = []AgentMessage{}
	a.followUp = []AgentMessage{}
}

func (a *Agent) HasQueuedMessages() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.steering) > 0 || len(a.followUp) > 0
}

func (a *Agent) QueuedMessageCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.steering) + len(a.followUp)
}

func (a *Agent) QueuedMessageCounts() (steering, followUp int) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.steering), len(a.followUp)
}

func (a *Agent) QueuedMessages() (steering, followUp []AgentMessage) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return append([]AgentMessage{}, a.steering...), append([]AgentMessage{}, a.followUp...)
}

func (a *Agent) DropLastQueuedMessage() (kind string, ok bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.followUp) > 0 {
		a.followUp = a.followUp[:len(a.followUp)-1]
		return "follow-up", true
	}
	if len(a.steering) > 0 {
		a.steering = a.steering[:len(a.steering)-1]
		return "steer", true
	}
	return "", false
}

func (a *Agent) GetSteeringMode() ExecutionMode {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config.SteeringMode
}

func (a *Agent) SetSteeringMode(mode ExecutionMode) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.config.SteeringMode = mode
}

func (a *Agent) GetFollowUpMode() ExecutionMode {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config.FollowUpMode
}

func (a *Agent) SetFollowUpMode(mode ExecutionMode) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.config.FollowUpMode = mode
}

func (a *Agent) dequeueSteeringLocked() []AgentMessage {
	if a.config.SteeringMode == ExecutionModeOneAtATime {
		if len(a.steering) == 0 {
			return []AgentMessage{}
		}
		first := a.steering[0]
		a.steering = a.steering[1:]
		return []AgentMessage{first}
	}
	messages := append([]AgentMessage{}, a.steering...)
	a.steering = []AgentMessage{}
	return messages
}

func (a *Agent) dequeueFollowUpLocked() []AgentMessage {
	if a.config.FollowUpMode == ExecutionModeOneAtATime {
		if len(a.followUp) == 0 {
			return []AgentMessage{}
		}
		first := a.followUp[0]
		a.followUp = a.followUp[1:]
		return []AgentMessage{first}
	}
	messages := append([]AgentMessage{}, a.followUp...)
	a.followUp = []AgentMessage{}
	return messages
}
