package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/openmodu/modu/pkg/acp/manager"
	"github.com/openmodu/modu/pkg/providers"
)

// runWorker is the main loop of one ACP worker goroutine. It blocks on the
// store's work queue, runs matching tasks end-to-end, and returns when ctx
// is cancelled.
func runWorker(ctx context.Context, agentID string, store *Store, mgr *manager.Manager) {
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
			runTask(ctx, t, store, mgr)
		}
	}
}

// runTask executes one Task against its configured ACP provider.
func runTask(parent context.Context, t *Task, store *Store, mgr *manager.Manager) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	store.Start(t.ID, cancel)
	store.SetActive(t.Agent, t.Cwd, t.ID)
	defer store.ClearActive(t.Agent, t.Cwd)

	p, err := mgr.Provider(t.Agent, t.Cwd)
	if err != nil {
		store.Fail(t.ID, fmt.Sprintf("provider: %v", err))
		return
	}

	req := &providers.ChatRequest{
		Model: "acp:" + t.Agent,
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: t.Prompt},
		},
	}
	es, err := p.Stream(ctx, req)
	if err != nil {
		store.Fail(t.ID, fmt.Sprintf("stream: %v", err))
		return
	}

	for ev := range es.Events() {
		store.PushEvent(t.ID, ev)
	}
	msg, err := es.Result()
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
