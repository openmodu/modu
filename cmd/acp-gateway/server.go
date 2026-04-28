package main

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/openmodu/modu/pkg/acp/client"
	"github.com/openmodu/modu/pkg/acp/manager"
)

// Version is the gateway build version. Override with -ldflags "-X main.Version=x.y.z".
var Version = "dev"

// Server bundles the HTTP routing layer with the gateway's dependencies.
type Server struct {
	mgr        *manager.Manager
	store      *Store
	registry   *Registry
	token      string
	workdir    string
	configPath string // path to acp.config.json (for dynamic agent management)

	connections atomic.Int64 // active SSE connections
	startTime   time.Time

	handler http.Handler
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
	ConfigPath   string   // path to the loaded config file (for dynamic updates)
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
		mgr:        opts.Manager,
		store:      opts.Store,
		registry:   registry,
		token:      opts.Token,
		workdir:    opts.Workdir,
		configPath: opts.ConfigPath,
		startTime:  time.Now(),
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

// Close stops workers and ACP subprocesses.
func (s *Server) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	s.workers.Wait()
	return s.mgr.Shutdown()
}

// hooksFor returns the manager.Hooks the gateway installs on every client.
func hooksFor(store *Store) manager.Hooks {
	return manager.Hooks{
		OnPermission: func(agent manager.AgentConfig, cwd string, req *client.PermissionRequest) string {
			tid := store.ActiveTurnFor(agent.ID, cwd)
			if tid == "" {
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

// saveConfig persists the manager's current config to disk (no-op if no path).
func (s *Server) saveConfig() error {
	if s.configPath == "" {
		return nil
	}
	cfg := s.mgr.Config()
	return manager.SaveConfig(&cfg, s.configPath)
}
