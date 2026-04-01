package main

import (
	"context"
	"fmt"
	"net"
	"sort"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/coding_agent/subagent"
	codingtools "github.com/openmodu/modu/pkg/coding_agent/tools"
	"github.com/openmodu/modu/pkg/mailbox"
	"github.com/openmodu/modu/pkg/mailbox/client"
	"github.com/openmodu/modu/pkg/mailbox/server"
	"github.com/openmodu/modu/pkg/types"
)

type moduCodeMailboxRuntime struct {
	addr         string
	orchestrator *client.MailboxClient
	cancel       context.CancelFunc
	agentIDs     []string
}

func startMailboxRuntime(
	agentDir string,
	extraAgentsDir string,
	cwd string,
	model *types.Model,
	getAPIKey func(string) (string, error),
) (*moduCodeMailboxRuntime, error) {
	addr, err := allocateLocalAddr()
	if err != nil {
		return nil, err
	}

	srv := server.NewMailboxServer()
	go func() {
		_ = srv.ListenAndServe(addr)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	orchestrator := client.NewMailboxClient("modu-code-main", addr)
	if err := registerMailboxClient(ctx, orchestrator, "orchestrator"); err != nil {
		cancel()
		return nil, err
	}

	loader := subagent.NewLoader()
	loader.Discover(agentDir, cwd)
	loader.DiscoverExtra(extraAgentsDir)
	defs := loader.List()
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })

	runtime := &moduCodeMailboxRuntime{
		addr:         addr,
		orchestrator: orchestrator,
		cancel:       cancel,
		agentIDs:     make([]string, 0, len(defs)),
	}
	for _, def := range defs {
		runtime.agentIDs = append(runtime.agentIDs, def.Name)
		go runMailboxWorker(ctx, addr, cwd, model, getAPIKey, def)
	}
	return runtime, nil
}

func (r *moduCodeMailboxRuntime) Client() *client.MailboxClient {
	if r == nil {
		return nil
	}
	return r.orchestrator
}

func (r *moduCodeMailboxRuntime) AgentIDs() []string {
	if r == nil {
		return nil
	}
	out := make([]string, len(r.agentIDs))
	copy(out, r.agentIDs)
	return out
}

func (r *moduCodeMailboxRuntime) Close() {
	if r == nil || r.cancel == nil {
		return
	}
	r.cancel()
}

func allocateLocalAddr() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr, nil
}

func registerMailboxClient(ctx context.Context, c *client.MailboxClient, role string) error {
	var lastErr error
	for attempt := 0; attempt < 30; attempt++ {
		if err := c.Register(ctx); err != nil {
			lastErr = err
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if role != "" {
			_ = c.SetRole(ctx, role)
			_ = c.SetStatus(ctx, "idle", "")
		}
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("unknown mailbox registration failure")
	}
	return lastErr
}

func runMailboxWorker(
	ctx context.Context,
	addr string,
	cwd string,
	model *types.Model,
	getAPIKey func(string) (string, error),
	def *subagent.SubagentDefinition,
) {
	worker := client.NewMailboxClient(def.Name, addr)
	if err := registerMailboxClient(ctx, worker, "worker"); err != nil {
		return
	}
	_ = worker.SetCapabilities(ctx, def.Name)

	poll := time.NewTicker(200 * time.Millisecond)
	defer poll.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
		}

		raw, err := worker.Recv(ctx)
		if err != nil || raw == "" {
			continue
		}
		msg, err := mailbox.ParseMessage(raw)
		if err != nil || msg.Type != mailbox.MessageTypeTaskAssign {
			continue
		}
		payload, err := mailbox.ParseTaskAssignPayload(msg)
		if err != nil {
			_ = worker.FailTask(ctx, msg.TaskID, err.Error())
			continue
		}

		_ = worker.SetStatus(ctx, "running", msg.TaskID)
		_ = worker.StartTask(ctx, msg.TaskID)

		result, runErr := subagent.Run(
			ctx,
			def,
			payload.Description,
			codingtools.AllTools(cwd),
			model,
			getAPIKey,
			agent.StreamDefault,
		)
		if runErr != nil {
			_ = worker.FailTask(ctx, msg.TaskID, runErr.Error())
			_ = worker.SetStatus(ctx, "idle", "")
			continue
		}

		_ = worker.CompleteTask(ctx, msg.TaskID, result)
		_ = worker.SetStatus(ctx, "idle", "")
	}
}
