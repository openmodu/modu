// Package manager owns a pool of ACP agents. It reads AgentConfig entries
// from Config, lazily spawns a subprocess + client the first time a given
// (agentID, cwd) pair is requested, and hands back a ready-to-stream
// provider.Provider. Shutdown stops every process that was created.
//
// One (agentID, cwd) pair = one running subprocess. Callers that need
// different working directories for the same agent get different
// subprocesses; ACP agents treat cwd as part of session identity, so
// reusing one subprocess across cwds leads to cross-contamination.
package manager

import (
	"fmt"
	"sync"

	"github.com/openmodu/modu/pkg/acp/client"
	"github.com/openmodu/modu/pkg/acp/process"
	"github.com/openmodu/modu/pkg/acp/provider"
)

// Hooks are the reverse-RPC callbacks the manager injects into every
// client it creates. The gateway supplies real implementations; tests
// supply fakes.
//
// OnPermission receives the agent config and cwd alongside the request so
// callers can route approval prompts back to the right task (two sessions
// for different cwds share the same process-level handler otherwise).
type Hooks struct {
	OnPermission func(agent AgentConfig, cwd string, req *client.PermissionRequest) string
	FS           client.FSHandler
}

// Manager maps agent IDs to their running providers.
type Manager struct {
	cfg   *Config
	hooks Hooks

	mu        sync.Mutex
	providers map[string]*providerEntry
	shut      bool

	// newProcess lets tests substitute an in-memory transport for the real
	// subprocess. Nil means "use pkg/acp/process.New".
	newProcess func(cfg AgentConfig) client.Transport
}

// providerEntry groups a provider with the transport we may need to stop.
type providerEntry struct {
	prov *provider.Provider
	tx   client.Transport
}

// New builds a Manager. It does NOT spawn any subprocesses; that happens
// on first Provider() call.
func New(cfg *Config, hooks Hooks) *Manager {
	return &Manager{
		cfg:       cfg,
		hooks:     hooks,
		providers: map[string]*providerEntry{},
	}
}

// List returns the IDs of all configured agents.
func (m *Manager) List() []string {
	out := make([]string, 0, len(m.cfg.Agents))
	for _, a := range m.cfg.Agents {
		out = append(out, a.ID)
	}
	return out
}

// Provider returns (or lazily creates) a provider for the (agentID, cwd)
// pair. Two callers with the same pair get the same provider; different
// cwds get different providers, each with its own subprocess.
func (m *Manager) Provider(agentID, cwd string) (*provider.Provider, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.shut {
		return nil, fmt.Errorf("acp/manager: shutdown")
	}
	key := agentID + "|" + cwd
	if entry, ok := m.providers[key]; ok {
		return entry.prov, nil
	}

	agent, ok := m.cfg.Agent(agentID)
	if !ok {
		return nil, fmt.Errorf("acp/manager: unknown agent %q", agentID)
	}

	tx := m.makeTransport(agent)
	var onPerm client.PermissionHandler
	if m.hooks.OnPermission != nil {
		onPerm = func(req *client.PermissionRequest) string {
			return m.hooks.OnPermission(agent, cwd, req)
		}
	}
	c := client.New(client.Config{
		Transport:    tx,
		OnPermission: onPerm,
		FS:           m.hooks.FS,
	})
	p := provider.New(provider.Options{
		ID:     providerID(agentID),
		Client: c,
		Cwd:    cwd,
		Name:   agent.Name,
	})
	m.providers[key] = &providerEntry{prov: p, tx: tx}
	return p, nil
}

// Shutdown stops every transport the manager started. Idempotent.
func (m *Manager) Shutdown() error {
	m.mu.Lock()
	if m.shut {
		m.mu.Unlock()
		return nil
	}
	m.shut = true
	entries := m.providers
	m.providers = map[string]*providerEntry{}
	m.mu.Unlock()

	var firstErr error
	for _, entry := range entries {
		if err := entry.tx.Stop(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// SetNewProcess installs a transport factory that overrides the real
// subprocess path. Intended for tests (unit or integration) that want to
// swap in an in-memory fake agent. Callers must invoke it before the first
// Provider() call — providers cache their transport.
func (m *Manager) SetNewProcess(fn func(cfg AgentConfig) client.Transport) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.newProcess = fn
}

func (m *Manager) makeTransport(agent AgentConfig) client.Transport {
	if m.newProcess != nil {
		return m.newProcess(agent)
	}
	return process.New(process.Config{
		ID:      agent.ID,
		Command: agent.Command,
		Args:    agent.Args,
		Env:     agent.Env,
	})
}

// providerID is the conventional providers.Register identifier for an
// ACP-backed agent.
func providerID(agentID string) string { return "acp:" + agentID }
