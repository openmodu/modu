package agent

import (
	"context"
	"fmt"

	"github.com/openmodu/modu/pkg/types"
)

func (a *Agent) Continue(ctx context.Context) error {
	a.mu.Lock()
	if a.running != nil {
		a.mu.Unlock()
		return fmt.Errorf("agent is already processing")
	}
	messages := append([]types.AgentMessage{}, a.state.Messages...)
	if len(messages) == 0 {
		a.mu.Unlock()
		return fmt.Errorf("no messages to continue from")
	}
	if roleOf(messages[len(messages)-1]) == types.RoleAssistant {
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

func (a *Agent) Steer(message types.AgentMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.steering = append(a.steering, message)
}

func (a *Agent) FollowUp(message types.AgentMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.followUp = append(a.followUp, message)
}

func (a *Agent) ClearSteeringQueue() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.steering = []types.AgentMessage{}
}

func (a *Agent) ClearFollowUpQueue() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.followUp = []types.AgentMessage{}
}

func (a *Agent) ClearAllQueues() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.steering = []types.AgentMessage{}
	a.followUp = []types.AgentMessage{}
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

func (a *Agent) QueuedMessages() (steering, followUp []types.AgentMessage) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return append([]types.AgentMessage{}, a.steering...), append([]types.AgentMessage{}, a.followUp...)
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

func (a *Agent) GetSteeringMode() types.ExecutionMode {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config.SteeringMode
}

func (a *Agent) SetSteeringMode(mode types.ExecutionMode) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.config.SteeringMode = mode
}

func (a *Agent) GetFollowUpMode() types.ExecutionMode {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config.FollowUpMode
}

func (a *Agent) SetFollowUpMode(mode types.ExecutionMode) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.config.FollowUpMode = mode
}

func (a *Agent) dequeueSteeringLocked() []types.AgentMessage {
	if a.config.SteeringMode == types.ExecutionModeOneAtATime {
		if len(a.steering) == 0 {
			return []types.AgentMessage{}
		}
		first := a.steering[0]
		a.steering = a.steering[1:]
		return []types.AgentMessage{first}
	}
	messages := append([]types.AgentMessage{}, a.steering...)
	a.steering = []types.AgentMessage{}
	return messages
}

func (a *Agent) dequeueFollowUpLocked() []types.AgentMessage {
	if a.config.FollowUpMode == types.ExecutionModeOneAtATime {
		if len(a.followUp) == 0 {
			return []types.AgentMessage{}
		}
		first := a.followUp[0]
		a.followUp = a.followUp[1:]
		return []types.AgentMessage{first}
	}
	messages := append([]types.AgentMessage{}, a.followUp...)
	a.followUp = []types.AgentMessage{}
	return messages
}
