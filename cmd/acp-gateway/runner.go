package main

import (
	"context"
	"sync"

	"github.com/openmodu/modu/pkg/types"
)

// Runner executes one prompt on behalf of a task and streams events/permission
// prompts back via RunnerHooks. The gateway holds a Registry of Runners keyed
// by agentID; the worker picks a Runner without knowing whether it wraps an
// ACP subprocess, a Tier-1 model SDK, or anything else.
type Runner interface {
	AgentID() string
	Run(ctx context.Context, prompt, cwd string, hooks RunnerHooks) (*types.AssistantMessage, error)
}

// RunnerHooks are the callbacks a Runner uses to surface activity to the
// gateway. OnEvent is always safe to call; OnPermission blocks until the HTTP
// /approve lands (or the task is cancelled) and returns the chosen optionID.
//
// Runners that route permissions through an out-of-band path (e.g. the ACP
// runner, which relies on the manager's global hook + store.ActiveTaskFor)
// may simply not invoke OnPermission.
type RunnerHooks struct {
	OnEvent      func(types.StreamEvent)
	OnPermission func(PermissionPrompt) string
}

// Registry is the lookup table of agentID → Runner. It is populated once at
// server startup and read-only afterwards, but guarded by RWMutex so future
// dynamic registration stays safe.
type Registry struct {
	mu      sync.RWMutex
	runners map[string]Runner
}

func NewRegistry() *Registry {
	return &Registry{runners: make(map[string]Runner)}
}

// Register adds a Runner. A second Register with the same AgentID overwrites
// the previous entry — callers are expected to avoid that.
func (r *Registry) Register(rn Runner) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runners[rn.AgentID()] = rn
}

func (r *Registry) Get(id string) (Runner, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rn, ok := r.runners[id]
	return rn, ok
}

func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.runners))
	for id := range r.runners {
		out = append(out, id)
	}
	return out
}
