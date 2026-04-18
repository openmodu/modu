package manager

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/openmodu/modu/pkg/acp/client"
)

// stubTransport is a minimal client.Transport that records lifecycle calls.
// Start/Stop are the only surface we need; read/write are never exercised
// because manager tests do not drive a full ACP session.
type stubTransport struct {
	started   atomic.Int32
	stopped   atomic.Int32
	stopErr   error
	lines     chan []byte
	doneCh    chan struct{}
	closeOnce sync.Once
}

func newStubTransport() *stubTransport {
	return &stubTransport{
		lines:  make(chan []byte),
		doneCh: make(chan struct{}),
	}
}

func (s *stubTransport) Start() error {
	s.started.Add(1)
	return nil
}

func (s *stubTransport) Stop() error {
	s.stopped.Add(1)
	s.closeOnce.Do(func() {
		close(s.lines)
		close(s.doneCh)
	})
	return s.stopErr
}

func (s *stubTransport) Write(line []byte) error    { return nil }
func (s *stubTransport) Lines() <-chan []byte       { return s.lines }
func (s *stubTransport) Done() <-chan struct{}      { return s.doneCh }

// sanity: ensure stubTransport satisfies the interface.
var _ client.Transport = (*stubTransport)(nil)

// ---------- LoadConfig tests ----------

func TestLoadConfig_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "acp.config.json")

	want := &Config{
		Version: 1,
		Agents: []AgentConfig{
			{
				ID:      "claude",
				Name:    "Claude Code",
				Command: "npx",
				Args:    []string{"-y", "@zed-industries/claude-code-acp"},
				Env:     map[string]string{"ANTHROPIC_API_KEY": "sk-test"},
				PermissionMode: "default",
			},
			{
				ID:      "codex",
				Name:    "Codex",
				Command: "codex",
			},
		},
		DefaultAgent: "claude",
	}
	data, err := json.MarshalIndent(want, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.DefaultAgent != "claude" {
		t.Errorf("DefaultAgent = %q", got.DefaultAgent)
	}
	if len(got.Agents) != 2 {
		t.Fatalf("agents = %d, want 2", len(got.Agents))
	}
	if got.Agents[0].ID != "claude" || got.Agents[0].Command != "npx" {
		t.Errorf("agents[0] = %+v", got.Agents[0])
	}
	if got.Agents[0].Env["ANTHROPIC_API_KEY"] != "sk-test" {
		t.Errorf("env missing: %+v", got.Agents[0].Env)
	}
	if len(got.Agents[0].Args) != 2 {
		t.Errorf("args = %v", got.Agents[0].Args)
	}
}

func TestLoadConfig_FirstMatchWins(t *testing.T) {
	dir := t.TempDir()
	second := filepath.Join(dir, "second.json")

	cfg := &Config{Agents: []AgentConfig{{ID: "a", Command: "echo"}}}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(second, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// First path doesn't exist; loader should skip it and pick `second`.
	missing := filepath.Join(dir, "missing.json")
	got, err := LoadConfig(missing, second)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Agents) != 1 || got.Agents[0].ID != "a" {
		t.Errorf("got = %+v", got)
	}
}

func TestLoadConfig_NoFileFound(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadConfig(filepath.Join(dir, "nope.json"))
	if err == nil {
		t.Fatal("expected error when no file matches")
	}
	if !strings.Contains(err.Error(), "no config file found") {
		t.Errorf("err = %v", err)
	}
}

func TestLoadConfig_DefaultAgent_Missing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "acp.config.json")
	cfg := &Config{
		Agents:       []AgentConfig{{ID: "claude", Command: "npx"}},
		DefaultAgent: "gemini",
	}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error when defaultAgent not in agents")
	}
	if !strings.Contains(err.Error(), "defaultAgent") {
		t.Errorf("err = %v", err)
	}
}

func TestLoadConfig_DuplicateID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "acp.config.json")
	cfg := &Config{Agents: []AgentConfig{
		{ID: "x", Command: "a"},
		{ID: "x", Command: "b"},
	}}
	data, _ := json.Marshal(cfg)
	_ = os.WriteFile(path, data, 0o644)
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected duplicate-id error")
	}
}

func TestConfigValidate_EmptyIDOrCommand(t *testing.T) {
	cases := []Config{
		{Agents: []AgentConfig{{ID: "", Command: "x"}}},
		{Agents: []AgentConfig{{ID: "y", Command: ""}}},
	}
	for i, c := range cases {
		if err := c.Validate(); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
}

// ---------- Manager tests ----------

func testManager(t *testing.T, cfg *Config) (*Manager, *txFactory) {
	t.Helper()
	factory := newTxFactory()
	m := New(cfg, Hooks{})
	m.newProcess = factory.make
	return m, factory
}

// txFactory hands out stubTransports and records each one for assertions.
type txFactory struct {
	mu    sync.Mutex
	made  []*stubTransport
}

func newTxFactory() *txFactory {
	return &txFactory{}
}

func (f *txFactory) make(cfg AgentConfig) client.Transport {
	tx := newStubTransport()
	f.mu.Lock()
	f.made = append(f.made, tx)
	f.mu.Unlock()
	return tx
}

func (f *txFactory) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.made)
}

func (f *txFactory) all() []*stubTransport {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]*stubTransport, len(f.made))
	copy(cp, f.made)
	return cp
}

func TestManager_List(t *testing.T) {
	cfg := &Config{Agents: []AgentConfig{
		{ID: "claude", Command: "a"},
		{ID: "codex", Command: "b"},
	}}
	m := New(cfg, Hooks{})
	got := m.List()
	if len(got) != 2 || got[0] != "claude" || got[1] != "codex" {
		t.Errorf("list = %v", got)
	}
}

func TestProvider_LazyInit(t *testing.T) {
	cfg := &Config{Agents: []AgentConfig{{ID: "claude", Command: "npx"}}}
	m, factory := testManager(t, cfg)

	// Construction alone must not create a transport.
	if n := factory.count(); n != 0 {
		t.Fatalf("after New: transports = %d, want 0", n)
	}

	p1, err := m.Provider("claude", "/tmp/repo")
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	if n := factory.count(); n != 1 {
		t.Errorf("after 1st call: transports = %d, want 1", n)
	}
	if p1.ID() != "acp:claude" {
		t.Errorf("provider id = %q", p1.ID())
	}

	// Second call with same (agentID, cwd) must reuse, not spawn.
	p2, err := m.Provider("claude", "/tmp/repo")
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	if p1 != p2 {
		t.Error("expected same provider instance for same key")
	}
	if n := factory.count(); n != 1 {
		t.Errorf("after 2nd call: transports = %d, want 1", n)
	}

	// Different cwd must get its own transport.
	if _, err := m.Provider("claude", "/tmp/other"); err != nil {
		t.Fatalf("provider other: %v", err)
	}
	if n := factory.count(); n != 2 {
		t.Errorf("after different-cwd call: transports = %d, want 2", n)
	}
}

func TestProvider_IDNotFound(t *testing.T) {
	cfg := &Config{Agents: []AgentConfig{{ID: "claude", Command: "npx"}}}
	m, factory := testManager(t, cfg)

	_, err := m.Provider("ghost", "/tmp")
	if err == nil {
		t.Fatal("expected error for unknown agent id")
	}
	if !strings.Contains(err.Error(), "unknown agent") {
		t.Errorf("err = %v", err)
	}
	if n := factory.count(); n != 0 {
		t.Errorf("transports created on error = %d, want 0", n)
	}
}

func TestShutdown_StopsAll(t *testing.T) {
	cfg := &Config{Agents: []AgentConfig{
		{ID: "claude", Command: "a"},
		{ID: "codex", Command: "b"},
	}}
	m, factory := testManager(t, cfg)

	if _, err := m.Provider("claude", "/r1"); err != nil {
		t.Fatalf("provider: %v", err)
	}
	if _, err := m.Provider("claude", "/r2"); err != nil {
		t.Fatalf("provider: %v", err)
	}
	if _, err := m.Provider("codex", "/r1"); err != nil {
		t.Fatalf("provider: %v", err)
	}
	if factory.count() != 3 {
		t.Fatalf("transports = %d, want 3", factory.count())
	}

	if err := m.Shutdown(); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	for i, tx := range factory.all() {
		if tx.stopped.Load() != 1 {
			t.Errorf("tx[%d] stopped = %d, want 1", i, tx.stopped.Load())
		}
	}

	// Idempotent.
	if err := m.Shutdown(); err != nil {
		t.Errorf("second shutdown: %v", err)
	}
	// No extra Stop calls on the transports.
	for i, tx := range factory.all() {
		if tx.stopped.Load() != 1 {
			t.Errorf("tx[%d] stopped after 2nd shutdown = %d, want 1", i, tx.stopped.Load())
		}
	}

	// Provider() after shutdown must error and must not create new transports.
	if _, err := m.Provider("claude", "/r3"); err == nil {
		t.Error("expected error after shutdown")
	}
	if factory.count() != 3 {
		t.Errorf("transports after post-shutdown call = %d, want 3", factory.count())
	}
}

func TestShutdown_ReturnsFirstError(t *testing.T) {
	cfg := &Config{Agents: []AgentConfig{{ID: "claude", Command: "a"}}}
	m, factory := testManager(t, cfg)

	if _, err := m.Provider("claude", "/r1"); err != nil {
		t.Fatalf("provider: %v", err)
	}
	boom := errors.New("boom")
	factory.all()[0].stopErr = boom

	err := m.Shutdown()
	if !errors.Is(err, boom) {
		t.Errorf("shutdown err = %v, want boom", err)
	}
}
