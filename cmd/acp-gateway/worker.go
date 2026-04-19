package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/openmodu/modu/pkg/types"
)

// runWorker is the main loop of one gateway worker goroutine. It blocks on
// the store's work queue, runs matching tasks end-to-end via the Registry,
// and returns when ctx is cancelled.
func runWorker(ctx context.Context, agentID string, store *Store, reg *Registry) {
	for {
		select {
		case <-ctx.Done():
			return
		case id, ok := <-store.Queue():
			if !ok {
				return
			}
			t, found := store.Get(id)
			if !found {
				continue
			}
			if t.Agent != agentID {
				// Not for this worker — re-queue. A different worker picks it up.
				// (Best-effort; if the queue is backed up this can spin, but the
				// realistic load is N workers for M agents at low QPS.)
				go func() { store.queue <- id }()
				continue
			}
			runner, ok := reg.Get(t.Agent)
			if !ok {
				store.Fail(t.ID, fmt.Sprintf("no runner registered for agent %q", t.Agent))
				continue
			}
			runTask(ctx, t, store, runner)
		}
	}
}

// runTask executes one Task against a Runner.
func runTask(parent context.Context, t *Task, store *Store, runner Runner) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	store.Start(t.ID, cancel)
	// SetActive/ClearActive remain useful for any Runner whose permissions
	// are routed out-of-band (today: the ACP manager's global hook).
	store.SetActive(t.Agent, t.Cwd, t.ID)
	defer store.ClearActive(t.Agent, t.Cwd)

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
			store.Fail(t.ID, "cancelled")
			return
		}
		log.Printf("[acp-gateway] task %s failed: %v", t.ID, err)
		store.Fail(t.ID, err.Error())
		return
	}

	result := ""
	if msg != nil {
		result = assistantText(msg)
	}
	store.Complete(t.ID, result)
}
