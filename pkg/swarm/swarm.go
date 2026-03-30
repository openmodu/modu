// Package swarm provides an auto-scaling Agent Swarm manager built on top of the mailbox
// infrastructure.
//
// Unlike Agent Teams, which rely on a fixed Orchestrator to assign tasks, a Swarm is
// decentralised: any caller can publish tasks to a shared queue, and agents compete to
// claim them based on their declared capabilities.
//
// Key concepts:
//   - No fixed orchestrator: tasks are published to a shared queue; any agent that has the
//     required capabilities can claim and execute them.
//   - Capability-based claiming: agents declare a capability list; tasks declare required
//     capabilities; ClaimTask only returns tasks whose requirements are satisfied.
//   - Auto-scaling: the Swarm monitors queue depth vs. idle agents and calls AgentFactory
//     to spawn or despawn agents as load changes.
package swarm

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/openmodu/modu/pkg/mailbox"
)

// AgentFactory creates and destroys agent instances on behalf of the Swarm.
// Implementations are free to launch goroutines, OS processes, containers, etc.
type AgentFactory interface {
	// Spawn starts a new agent identified by agentID with the given capability list.
	// The agent should run until ctx is cancelled.
	Spawn(ctx context.Context, agentID string, caps []string) error
}

// SpawnPolicy controls the auto-scaling behaviour of a Swarm.
type SpawnPolicy struct {
	// MinAgents is the minimum number of agents to keep alive (default: 1).
	MinAgents int
	// MaxAgents is the maximum number of agents allowed at any time (default: MinAgents).
	MaxAgents int
	// Capabilities is the capability list assigned to every agent spawned by this Swarm.
	Capabilities []string
	// ScaleUpRatio triggers a scale-up when queue_len / idle_agents exceeds this value
	// (default: 1.0). Example: ratio=2.0 means one idle agent per two queued tasks.
	ScaleUpRatio float64
	// CheckInterval is how often the scaling loop runs (default: 2s).
	CheckInterval time.Duration
	// AgentIDPrefix is the prefix used when generating agent IDs (default: "swarm-agent").
	AgentIDPrefix string
}

type agentEntry struct {
	id        string
	spawnedAt time.Time
	cancel    context.CancelFunc
}

// Swarm manages a dynamic pool of agents that autonomously claim tasks from the mailbox
// swarm queue. There is no fixed orchestrator — anyone can call PublishTask and agents
// will respond on their own.
type Swarm struct {
	hub     *mailbox.Hub
	factory AgentFactory
	policy  SpawnPolicy
	mu      sync.Mutex
	agents  map[string]*agentEntry
	counter uint64
	ctx     context.Context
	cancel  context.CancelFunc
}

// New creates a new Swarm. Call Start to begin auto-scaling.
func New(hub *mailbox.Hub, factory AgentFactory, policy SpawnPolicy) *Swarm {
	if policy.MinAgents < 1 {
		policy.MinAgents = 1
	}
	if policy.MaxAgents < policy.MinAgents {
		policy.MaxAgents = policy.MinAgents
	}
	if policy.ScaleUpRatio <= 0 {
		policy.ScaleUpRatio = 1.0
	}
	if policy.CheckInterval <= 0 {
		policy.CheckInterval = 2 * time.Second
	}
	if policy.AgentIDPrefix == "" {
		policy.AgentIDPrefix = "swarm-agent"
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Swarm{
		hub:     hub,
		factory: factory,
		policy:  policy,
		agents:  make(map[string]*agentEntry),
		ctx:     ctx,
		cancel:  cancel,
	}
}

// Start spawns the initial minimum number of agents and begins the background scaling loop.
func (s *Swarm) Start() {
	s.mu.Lock()
	for i := 0; i < s.policy.MinAgents; i++ {
		s.spawnLocked()
	}
	s.mu.Unlock()
	go s.scalingLoop()
}

// Stop cancels the context of all managed agents and shuts down the Swarm.
func (s *Swarm) Stop() {
	s.cancel()
}

// AgentCount returns the number of agents currently managed by this Swarm.
func (s *Swarm) AgentCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.agents)
}

// spawnLocked spawns a new agent. Caller must hold s.mu.
func (s *Swarm) spawnLocked() {
	s.counter++
	agentID := fmt.Sprintf("%s-%d", s.policy.AgentIDPrefix, s.counter)
	agentCtx, agentCancel := context.WithCancel(s.ctx)
	entry := &agentEntry{
		id:        agentID,
		spawnedAt: time.Now(),
		cancel:    agentCancel,
	}
	s.agents[agentID] = entry
	caps := s.policy.Capabilities
	if err := s.factory.Spawn(agentCtx, agentID, caps); err != nil {
		log.Printf("[Swarm] spawn %s failed: %v", agentID, err)
		agentCancel()
		delete(s.agents, agentID)
	} else {
		log.Printf("[Swarm] spawned %s (total: %d)", agentID, len(s.agents))
	}
}

// despawnLocked stops the given agent by cancelling its context. Caller must hold s.mu.
func (s *Swarm) despawnLocked(agentID string) {
	entry, ok := s.agents[agentID]
	if !ok {
		return
	}
	entry.cancel()
	delete(s.agents, agentID)
	log.Printf("[Swarm] despawned %s (total: %d)", agentID, len(s.agents))
}

func (s *Swarm) scalingLoop() {
	ticker := time.NewTicker(s.policy.CheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.scale()
		}
	}
}

// scale evaluates queue depth and idle agent count, then spawns or despawns as needed.
func (s *Swarm) scale() {
	queueLen := s.hub.SwarmQueueLen()
	allInfos := s.hub.ListAgentInfos()

	s.mu.Lock()
	defer s.mu.Unlock()

	current := len(s.agents)
	if current == 0 {
		return
	}

	// Classify agents managed by this Swarm as idle or busy.
	var idleAgents []string
	busyCount := 0
	for _, info := range allInfos {
		if _, mine := s.agents[info.ID]; !mine {
			continue
		}
		if info.Status == "idle" {
			idleAgents = append(idleAgents, info.ID)
		} else {
			busyCount++
		}
	}
	idleCount := len(idleAgents)

	// Scale up: tasks are waiting but no idle agent is available.
	if queueLen > 0 && idleCount == 0 && current < s.policy.MaxAgents {
		s.spawnLocked()
		return
	}

	// Scale up: queue-to-idle ratio exceeds the configured threshold.
	if queueLen > 0 && idleCount > 0 && s.policy.ScaleUpRatio > 0 {
		ratio := float64(queueLen) / float64(idleCount)
		if ratio > s.policy.ScaleUpRatio && current < s.policy.MaxAgents {
			s.spawnLocked()
			return
		}
	}

	// Scale down: queue is empty, all agents are idle, and we are above the minimum.
	if queueLen == 0 && busyCount == 0 && current > s.policy.MinAgents && len(idleAgents) > 0 {
		s.despawnLocked(idleAgents[0])
	}
}
