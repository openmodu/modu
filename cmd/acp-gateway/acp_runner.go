package main

import (
	"context"

	"github.com/openmodu/modu/pkg/acp/manager"
	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/types"
)

// acpRunner adapts *manager.Manager to the Runner interface. Each configured
// ACP agent id gets its own acpRunner instance.
//
// Permissions are NOT routed through RunnerHooks.OnPermission — the manager
// installs a global Hooks.OnPermission at construction (see hooksFor) that
// looks up the active task via Store.ActiveTaskFor and calls AwaitPermission
// directly. The worker still has to SetActive/ClearActive around Run so that
// lookup succeeds.
type acpRunner struct {
	id  string
	mgr *manager.Manager
}

func newACPRunner(id string, mgr *manager.Manager) *acpRunner {
	return &acpRunner{id: id, mgr: mgr}
}

func (r *acpRunner) AgentID() string { return r.id }

func (r *acpRunner) Run(ctx context.Context, prompt, cwd string, hooks RunnerHooks) (*types.AssistantMessage, error) {
	// Use the gateway SessionID (injected by the worker) as the provider key
	// so each gateway session gets an independent ACP session and conversation
	// context, even when two sessions share the same (agent, cwd).
	sessionKey, _ := ctx.Value(sessionIDKey).(string)
	if sessionKey == "" {
		sessionKey = cwd
	}
	p, err := r.mgr.ProviderKeyed(r.id, sessionKey, cwd)
	if err != nil {
		return nil, err
	}
	req := &providers.ChatRequest{
		Model: "acp:" + r.id,
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: prompt},
		},
	}
	es, err := p.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	for ev := range es.Events() {
		if hooks.OnEvent != nil {
			hooks.OnEvent(ev)
		}
	}
	return es.Result()
}
