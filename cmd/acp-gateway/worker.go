package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/openmodu/modu/pkg/types"
)

type ctxKey int

// sessionIDKey carries the gateway SessionID through the context so runners
// can scope their ACP provider to the session rather than just (agent, cwd).
const sessionIDKey ctxKey = 0

// runWorker is the main loop for one gateway worker goroutine.
// It pulls turn IDs from the store queue and executes them.
func runWorker(ctx context.Context, agentID string, store *Store, reg *Registry) {
	for {
		select {
		case <-ctx.Done():
			return
		case id, ok := <-store.Queue():
			if !ok {
				return
			}
			t, found := store.GetTurn(id)
			if !found {
				continue
			}
			if t.Agent != agentID {
				// Re-queue for the matching worker.
				go func() { store.queue <- id }()
				continue
			}
			runner, ok := reg.Get(t.Agent)
			if !ok {
				store.FailTurn(t.ID, fmt.Sprintf("no runner registered for agent %q", t.Agent))
				continue
			}
			runTurn(ctx, t, store, runner)
		}
	}
}

// runTurn executes one Turn against a Runner.
func runTurn(parent context.Context, t *Turn, store *Store, runner Runner) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	store.StartTurn(t.ID, cancel)
	store.SetActive(t.Agent, t.Cwd, t.ID)
	defer store.ClearActive(t.Agent, t.Cwd)

	// Inject the gateway SessionID so ACP runners can scope their provider
	// per session rather than per (agent, cwd), preventing context bleed
	// between different sessions that share the same working directory.
	ctx = context.WithValue(ctx, sessionIDKey, t.SessionID)

	hooks := RunnerHooks{
		OnEvent: func(ev types.StreamEvent) {
			store.PushEvent(t.ID, ev)
		},
		OnPermission: func(p PermissionPrompt) string {
			return store.AwaitPermission(t.ID, p)
		},
	}

	msg, err := runner.Run(ctx, t.Prompt, t.Cwd, hooks)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			store.FailTurn(t.ID, "cancelled")
			return
		}
		log.Printf("[acp-gateway] turn %s failed: %v", t.ID, err)
		store.FailTurn(t.ID, err.Error())
		return
	}

	result := ""
	if msg != nil {
		result = assistantText(msg)
	}
	store.CompleteTurn(t.ID, result)
}
