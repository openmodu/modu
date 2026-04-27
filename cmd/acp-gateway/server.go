package main

import (
	"context"
	"net/http"
	"sync"

	"github.com/openmodu/modu/pkg/acp/client"
	"github.com/openmodu/modu/pkg/acp/manager"
)

// Server bundles the HTTP routing layer with the gateway's dependencies.
type Server struct {
	mgr      *manager.Manager
	store    *Store
	registry *Registry
	token    string
	workdir  string
	handler  http.Handler

	workers sync.WaitGroup
	cancel  context.CancelFunc
}

// Options configures NewServer.
type Options struct {
	Manager      *manager.Manager
	Store        *Store
	Token        string   // empty = auth disabled (dev/test)
	WorkersEach  int      // workers per agent id (default 1)
	ExtraRunners []Runner // native (non-ACP) runners to register alongside ACP agents
	Workdir      string   // default cwd for tasks; empty = process cwd
}

// NewServer wires the router and starts worker goroutines. Call Close to
// tear them down along with the underlying ACP subprocesses.
func NewServer(opts Options) *Server {
	if opts.WorkersEach <= 0 {
		opts.WorkersEach = 1
	}

	registry := NewRegistry()
	for _, id := range opts.Manager.List() {
		registry.Register(newACPRunner(id, opts.Manager))
	}
	for _, rn := range opts.ExtraRunners {
		registry.Register(rn)
	}

	s := &Server{
		mgr:      opts.Manager,
		store:    opts.Store,
		registry: registry,
		token:    opts.Token,
		workdir:  opts.Workdir,
	}
	s.handler = s.buildRouter()

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	for _, id := range registry.List() {
		for i := 0; i < opts.WorkersEach; i++ {
			s.workers.Add(1)
			go func(agentID string) {
				defer s.workers.Done()
				runWorker(ctx, agentID, s.store, s.registry)
			}(id)
		}
	}
	return s
}

// ServeHTTP makes Server itself an http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// Close stops workers and ACP subprocesses. The HTTP listener must be
// stopped separately by the caller (main uses http.Server.Shutdown).
func (s *Server) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	s.workers.Wait()
	return s.mgr.Shutdown()
}

// hooksFor returns the manager.Hooks the gateway installs on every client.
// Permission prompts are routed through Store.AwaitPermission so HTTP
// subscribers can respond via /approve.
func hooksFor(store *Store) manager.Hooks {
	return manager.Hooks{
		OnPermission: func(agent manager.AgentConfig, cwd string, req *client.PermissionRequest) string {
			tid := store.ActiveTurnFor(agent.ID, cwd)
			if tid == "" {
				// No task claims this (agent, cwd) — deny.
				for _, o := range req.Options {
					if o.Kind == "reject_once" || o.Kind == "reject_always" {
						return o.OptionID
					}
				}
				return ""
			}
			return store.AwaitPermission(tid, PermissionPrompt{
				ToolCallID: req.ToolCall.ToolCallID,
				Title:      req.ToolCall.Title,
				Kind:       req.ToolCall.Kind,
				Options:    req.Options,
			})
		},
	}
}
