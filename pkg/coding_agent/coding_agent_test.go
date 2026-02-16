package coding_agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/crosszan/modu/pkg/agent"
	"github.com/crosszan/modu/pkg/coding_agent/extension"
	"github.com/crosszan/modu/pkg/llm"
)

func TestNewCodingSession(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")

	model := &llm.Model{
		ID:            "test-model",
		Name:          "Test Model",
		Api:           "ollama",
		Provider:      "ollama",
		ContextWindow: 8192,
		MaxTokens:     2048,
	}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:      dir,
		AgentDir: agentDir,
		Model:    model,
		GetAPIKey: func(provider string) (string, error) {
			return "", nil
		},
	})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	// Check tools are initialized
	toolNames := session.GetActiveToolNames()
	if len(toolNames) == 0 {
		t.Fatal("expected tools to be initialized")
	}

	// Check config
	cfg := session.GetConfig()
	if cfg == nil {
		t.Fatal("config should not be nil")
	}
}

func TestCodingSessionRequiresCwd(t *testing.T) {
	_, err := NewCodingSession(CodingSessionOptions{
		Model: &llm.Model{ID: "test"},
	})
	if err == nil {
		t.Fatal("expected error when Cwd is empty")
	}
}

func TestCodingSessionRequiresModel(t *testing.T) {
	_, err := NewCodingSession(CodingSessionOptions{
		Cwd: "/tmp",
	})
	if err == nil {
		t.Fatal("expected error when Model is nil")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.ThinkingLevel == "" {
		t.Fatal("thinking level should have default")
	}
	if !cfg.AutoCompaction {
		t.Fatal("auto compaction should be on by default")
	}
}

func TestLoadConfigMissing(t *testing.T) {
	cfg, err := LoadConfig("/nonexistent", "/nonexistent")
	if err != nil {
		t.Fatalf("loading missing config should not error: %v", err)
	}
	if cfg.ThinkingLevel == "" {
		t.Fatal("should have defaults when files are missing")
	}
}

func TestLoadConfigFromFile(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agent")
	os.MkdirAll(agentDir, 0o755)

	configContent := `{"thinkingLevel":"high","autoCompaction":false}`
	os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(configContent), 0o644)

	cfg, err := LoadConfig(agentDir, dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ThinkingLevel != "high" {
		t.Fatalf("expected thinking level 'high', got %s", cfg.ThinkingLevel)
	}
	if cfg.AutoCompaction {
		t.Fatal("auto compaction should be false from config")
	}
}

func TestAPIKeyStore(t *testing.T) {
	dir := t.TempDir()
	store := NewAPIKeyStore(dir)

	// Set and get
	store.Set("test-provider", "test-key-123")

	key, ok := store.Get("test-provider")
	if !ok || key != "test-key-123" {
		t.Fatal("failed to get stored key")
	}

	// Missing key
	_, ok = store.Get("nonexistent")
	if ok {
		t.Fatal("should not find nonexistent key")
	}
}

// --- Fix 1: Hook Integration Tests ---

// testHookExtension is a test extension that registers a before-hook.
type testHookExtension struct {
	blocked    []string
	afterCalls []string
}

func (e *testHookExtension) Name() string { return "test-hook" }
func (e *testHookExtension) Init(api extension.ExtensionAPI) error {
	// Register a hook via the runner's AddHook (cast to *Runner)
	if runner, ok := api.(*extension.Runner); ok {
		runner.AddHook(extension.ToolHook{
			Before: func(toolName string, args map[string]any) bool {
				if toolName == "bash" {
					e.blocked = append(e.blocked, toolName)
					return false // block bash tool
				}
				return true
			},
			After: func(toolName string, args map[string]any, result agent.AgentToolResult) {
				e.afterCalls = append(e.afterCalls, toolName)
			},
		})
	}
	return nil
}

func TestExtensionHooksAreApplied(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")

	model := &llm.Model{
		ID: "test", Api: "ollama", Provider: "ollama",
		ContextWindow: 8192, MaxTokens: 2048,
	}

	hookExt := &testHookExtension{}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:      dir,
		AgentDir: agentDir,
		Model:    model,
		Extensions: []extension.Extension{hookExt},
		GetAPIKey: func(provider string) (string, error) {
			return "", nil
		},
	})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	// Verify tools are wrapped by checking that bash tool is present
	// (the wrapping is transparent—tools should still be there)
	names := session.GetActiveToolNames()
	hasBash := false
	for _, n := range names {
		if n == "bash" {
			hasBash = true
			break
		}
	}
	if !hasBash {
		t.Fatal("expected bash tool to be present (wrapped)")
	}

	// Execute the bash tool directly to verify the hook blocks it
	state := session.GetAgent().GetState()
	for _, tool := range state.Tools {
		if tool.Name() == "bash" {
			result, err := tool.Execute(context.Background(), "test", map[string]any{
				"command": "echo hi",
			}, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// Before hook returns false for bash, so result should be empty
			if len(result.Content) != 0 {
				t.Fatal("expected bash tool to be blocked by before hook")
			}
			break
		}
	}

	if len(hookExt.blocked) != 1 || hookExt.blocked[0] != "bash" {
		t.Fatalf("expected bash to be blocked, got: %v", hookExt.blocked)
	}
}

// --- Fix 2: Auto Compaction Tests ---

func TestMaybeAutoCompact_BelowThreshold(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")

	model := &llm.Model{
		ID: "test", Api: "ollama", Provider: "ollama",
		ContextWindow: 10000, MaxTokens: 2048,
	}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:      dir,
		AgentDir: agentDir,
		Model:    model,
		GetAPIKey: func(provider string) (string, error) {
			return "", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Set totalTokens below threshold (80% of 10000 = 8000)
	session.totalTokens = 5000

	// Add some messages to agent so we can check count before/after
	session.agent.AppendMessage(llm.UserMessage{Role: "user", Content: "msg1"})
	session.agent.AppendMessage(llm.UserMessage{Role: "user", Content: "msg2"})
	msgsBefore := len(session.agent.GetState().Messages)

	session.maybeAutoCompact(context.Background())

	msgsAfter := len(session.agent.GetState().Messages)
	// Should NOT compact since we're below threshold
	if msgsAfter != msgsBefore {
		t.Fatalf("should not compact below threshold: before=%d after=%d", msgsBefore, msgsAfter)
	}
}

func TestMaybeAutoCompact_AboveThreshold(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")

	model := &llm.Model{
		ID: "test", Api: "ollama", Provider: "ollama",
		ContextWindow: 10000, MaxTokens: 2048,
	}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:      dir,
		AgentDir: agentDir,
		Model:    model,
		GetAPIKey: func(provider string) (string, error) {
			return "", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Set streamFn to nil so compaction uses the fallback (no LLM) path
	session.streamFn = nil

	// Add enough messages to make compaction meaningful (more than preserve count of 4)
	for i := 0; i < 10; i++ {
		session.agent.AppendMessage(llm.UserMessage{Role: "user", Content: "msg"})
	}

	// Set totalTokens above threshold (80% of 10000 = 8000)
	session.totalTokens = 9000

	msgsBefore := len(session.agent.GetState().Messages)

	session.maybeAutoCompact(context.Background())

	msgsAfter := len(session.agent.GetState().Messages)
	// Should compact: summary (1) + preserved (4) = 5, which is less than 10
	if msgsAfter >= msgsBefore {
		t.Fatalf("should compact above threshold: before=%d after=%d", msgsBefore, msgsAfter)
	}

	// Token counter should be reset
	if session.totalTokens != 0 {
		t.Fatalf("expected totalTokens reset to 0, got %d", session.totalTokens)
	}
}

func TestMaybeAutoCompact_DisabledByConfig(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")

	model := &llm.Model{
		ID: "test", Api: "ollama", Provider: "ollama",
		ContextWindow: 10000, MaxTokens: 2048,
	}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:      dir,
		AgentDir: agentDir,
		Model:    model,
		GetAPIKey: func(provider string) (string, error) {
			return "", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Disable auto compaction
	session.config.AutoCompaction = false
	session.totalTokens = 9000

	for i := 0; i < 10; i++ {
		session.agent.AppendMessage(llm.UserMessage{Role: "user", Content: "msg"})
	}
	msgsBefore := len(session.agent.GetState().Messages)

	session.maybeAutoCompact(context.Background())

	msgsAfter := len(session.agent.GetState().Messages)
	if msgsAfter != msgsBefore {
		t.Fatal("should not compact when AutoCompaction is disabled")
	}
}
