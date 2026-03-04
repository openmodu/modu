package coding_agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crosszan/modu/pkg/agent"
	"github.com/crosszan/modu/pkg/coding_agent/extension"
	"github.com/crosszan/modu/pkg/providers"
)

func TestNewCodingSession(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")

	model := &providers.Model{
		ID:            "test-model",
		Name:          "Test Model",
		Api:           "ollama",
		ProviderID:    "ollama",
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
		Model: &providers.Model{ID: "test"},
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

	model := &providers.Model{
		ID: "test", Api: "ollama", ProviderID: "ollama",
		ContextWindow: 8192, MaxTokens: 2048,
	}

	hookExt := &testHookExtension{}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:        dir,
		AgentDir:   agentDir,
		Model:      model,
		Extensions: []extension.Extension{hookExt},
		GetAPIKey: func(provider string) (string, error) {
			return "", nil
		},
	})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	// Verify tools are wrapped by checking that bash tool is present
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

func newTestModel() *providers.Model {
	return &providers.Model{
		ID: "test", Api: "ollama", ProviderID: "ollama",
		ContextWindow: 8192, MaxTokens: 2048,
	}
}

func newTestModelWithContext(contextWindow int) *providers.Model {
	return &providers.Model{
		ID: "test", Api: "ollama", ProviderID: "ollama",
		ContextWindow: contextWindow, MaxTokens: 2048,
	}
}

func newTestSession(t *testing.T, model *providers.Model) *CodingSession {
	t.Helper()
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")
	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:       dir,
		AgentDir:  agentDir,
		Model:     model,
		GetAPIKey: func(p string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func TestMaybeAutoCompact_BelowThreshold(t *testing.T) {
	session := newTestSession(t, newTestModelWithContext(10000))

	// Set totalTokens below threshold (80% of 10000 = 8000)
	session.totalTokens = 5000

	session.agent.AppendMessage(providers.UserMessage{Role: "user", Content: "msg1"})
	session.agent.AppendMessage(providers.UserMessage{Role: "user", Content: "msg2"})
	msgsBefore := len(session.agent.GetState().Messages)

	session.maybeAutoCompact(context.Background())

	msgsAfter := len(session.agent.GetState().Messages)
	if msgsAfter != msgsBefore {
		t.Fatalf("should not compact below threshold: before=%d after=%d", msgsBefore, msgsAfter)
	}
}

func TestMaybeAutoCompact_AboveThreshold(t *testing.T) {
	session := newTestSession(t, newTestModelWithContext(10000))

	// Set streamFn to a mock so compaction uses it and succeeds without LLM
	session.streamFn = func(ctx context.Context, model *providers.Model, llmCtx *providers.LLMContext, opts *providers.SimpleStreamOptions) (providers.EventStream, error) {
		stream := providers.NewEventStream()
		go func() {
			msg := &providers.AssistantMessage{
				Role:       "assistant",
				ProviderID: model.ProviderID,
				Model:      model.ID,
				Content:    []providers.ContentBlock{&providers.TextContent{Type: "text", Text: "mock summary"}},
			}
			stream.Push(providers.StreamEvent{Type: "done", Reason: "stop", Message: msg})
			stream.Close()
		}()
		return stream, nil
	}

	for i := 0; i < 10; i++ {
		session.agent.AppendMessage(providers.UserMessage{Role: "user", Content: "msg"})
	}

	// Set totalTokens above threshold (80% of 128000 = 102400)
	session.totalTokens = 105000

	msgsBefore := len(session.agent.GetState().Messages)

	session.maybeAutoCompact(context.Background())

	msgsAfter := len(session.agent.GetState().Messages)
	if msgsAfter >= msgsBefore {
		t.Fatalf("should compact above threshold: before=%d after=%d", msgsBefore, msgsAfter)
	}

	if session.totalTokens != 0 {
		t.Fatalf("expected totalTokens reset to 0, got %d", session.totalTokens)
	}
}

// --- Retry Manager Tests ---

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		msg      string
		expected bool
	}{
		{"server overloaded", true},
		{"rate limit exceeded", true},
		{"429 too many requests", true},
		{"HTTP 502 bad gateway", true},
		{"HTTP 503 service unavailable", true},
		{"normal error", false},
		{"invalid input", false},
		{"temporarily unavailable", true},
	}
	for _, tt := range tests {
		if got := IsRetryableError(tt.msg); got != tt.expected {
			t.Errorf("IsRetryableError(%q) = %v, want %v", tt.msg, got, tt.expected)
		}
	}
}

func TestRetryManagerReset(t *testing.T) {
	rm := NewRetryManager(RetryConfig{MaxRetries: 2, BaseDelayMs: 10, MaxDelayMs: 100}, true)
	rm.Reset()
	if !rm.IsEnabled() {
		t.Fatal("should be enabled")
	}
}

func TestRetryManagerDisabled(t *testing.T) {
	rm := NewRetryManager(RetryConfig{}, false)
	if rm.IsEnabled() {
		t.Fatal("should be disabled")
	}
	rm.SetEnabled(true)
	if !rm.IsEnabled() {
		t.Fatal("should be enabled after SetEnabled(true)")
	}
}

func TestRetryManagerAbort(t *testing.T) {
	rm := NewRetryManager(RetryConfig{MaxRetries: 3, BaseDelayMs: 10, MaxDelayMs: 100}, true)
	rm.AbortRetry()
}

// --- CycleModel Tests ---

func TestCycleModelNoScoped(t *testing.T) {
	session := newTestSession(t, newTestModel())
	result := session.CycleModel()
	if result != nil {
		t.Fatal("expected nil when no scoped models")
	}
}

func TestCycleModelWithScoped(t *testing.T) {
	model := &providers.Model{
		ID: "model-a", Api: "ollama", ProviderID: "ollama",
		ContextWindow: 8192, MaxTokens: 2048,
	}
	session := newTestSession(t, model)
	session.scopedModels = []string{"model-a", "model-b", "model-c"}

	next := session.CycleModel()
	if next == nil || next.ID != "model-b" {
		t.Fatalf("expected model-b, got %v", next)
	}

	next = session.CycleModel()
	if next == nil || next.ID != "model-c" {
		t.Fatalf("expected model-c, got %v", next)
	}

	next = session.CycleModel()
	if next == nil || next.ID != "model-a" {
		t.Fatalf("expected model-a, got %v", next)
	}
}

// --- CycleThinkingLevel Tests ---

func TestCycleThinkingLevel(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")
	session, err := NewCodingSession(CodingSessionOptions{
		Cwd: dir, AgentDir: agentDir, Model: newTestModel(),
		ThinkingLevel: agent.ThinkingLevelOff,
		GetAPIKey:     func(p string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	level := session.CycleThinkingLevel()
	if level != agent.ThinkingLevelLow {
		t.Fatalf("expected low, got %s", level)
	}
	level = session.CycleThinkingLevel()
	if level != agent.ThinkingLevelMedium {
		t.Fatalf("expected medium, got %s", level)
	}
	level = session.CycleThinkingLevel()
	if level != agent.ThinkingLevelHigh {
		t.Fatalf("expected high, got %s", level)
	}
	level = session.CycleThinkingLevel()
	if level != agent.ThinkingLevelOff {
		t.Fatalf("expected off, got %s", level)
	}
}

// --- Getter/Setter Tests ---

func TestGetSetAutoCompaction(t *testing.T) {
	session := newTestSession(t, newTestModel())
	session.SetAutoCompaction(false)
	if session.GetConfig().AutoCompaction {
		t.Fatal("expected auto compaction disabled")
	}
	session.SetAutoCompaction(true)
	if !session.GetConfig().AutoCompaction {
		t.Fatal("expected auto compaction enabled")
	}
}

func TestGetSetAutoRetry(t *testing.T) {
	session := newTestSession(t, newTestModel())
	session.SetAutoRetry(true)
	if !session.GetConfig().AutoRetry {
		t.Fatal("expected auto retry enabled")
	}
	session.SetAutoRetry(false)
	if session.GetConfig().AutoRetry {
		t.Fatal("expected auto retry disabled")
	}
}

func TestGetModel(t *testing.T) {
	model := &providers.Model{
		ID: "my-model", Api: "ollama", ProviderID: "ollama",
		ContextWindow: 8192, MaxTokens: 2048,
	}
	session := newTestSession(t, model)
	got := session.GetModel()
	if got.ID != "my-model" {
		t.Fatalf("expected 'my-model', got %s", got.ID)
	}
}

func TestGetMessages(t *testing.T) {
	session := newTestSession(t, newTestModel())
	msgs := session.GetMessages()
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(msgs))
	}

	session.GetAgent().AppendMessage(providers.UserMessage{Role: "user", Content: "hi"})
	msgs = session.GetMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
}

// --- New method tests ---

func TestGetLastAssistantText(t *testing.T) {
	session := newTestSession(t, newTestModel())

	text := session.GetLastAssistantText()
	if text != "" {
		t.Fatalf("expected empty, got %s", text)
	}

	session.GetAgent().AppendMessage(providers.AssistantMessage{
		Role: "assistant",
		Content: []providers.ContentBlock{
			&providers.TextContent{Type: "text", Text: "hello from assistant"},
		},
	})

	text = session.GetLastAssistantText()
	if text != "hello from assistant" {
		t.Fatalf("expected 'hello from assistant', got %s", text)
	}

	session.GetAgent().AppendMessage(providers.UserMessage{Role: "user", Content: "question"})
	session.GetAgent().AppendMessage(providers.AssistantMessage{
		Role: "assistant",
		Content: []providers.ContentBlock{
			&providers.TextContent{Type: "text", Text: "second response"},
		},
	})

	text = session.GetLastAssistantText()
	if text != "second response" {
		t.Fatalf("expected 'second response', got %s", text)
	}
}

func TestSessionName(t *testing.T) {
	session := newTestSession(t, newTestModel())
	if session.GetSessionName() != "" {
		t.Fatal("expected empty session name")
	}
	session.SetSessionName("my-session")
	if session.GetSessionName() != "my-session" {
		t.Fatalf("expected 'my-session', got %s", session.GetSessionName())
	}
}

func TestIsCompacting(t *testing.T) {
	session := newTestSession(t, newTestModel())
	if session.IsCompacting() {
		t.Fatal("should not be compacting initially")
	}
}

func TestGetSessionFile(t *testing.T) {
	session := newTestSession(t, newTestModel())
	filePath := session.GetSessionFile()
	if filePath == "" {
		t.Fatal("expected non-empty session file path")
	}
}

func TestGetSessionStats(t *testing.T) {
	session := newTestSession(t, newTestModel())
	stats := session.GetSessionStats()
	if stats.SessionStarted <= 0 {
		t.Fatal("sessionStarted should be positive")
	}
	if stats.DurationMs < 0 {
		t.Fatal("durationMs should be non-negative")
	}
	if stats.MessageCount != 0 {
		t.Fatalf("expected 0 messages, got %d", stats.MessageCount)
	}

	session.GetAgent().AppendMessage(providers.UserMessage{Role: "user", Content: "hi"})
	stats = session.GetSessionStats()
	if stats.MessageCount != 1 {
		t.Fatalf("expected 1 message, got %d", stats.MessageCount)
	}
}

func TestExecuteBash(t *testing.T) {
	session := newTestSession(t, newTestModel())
	result, err := session.ExecuteBash(context.Background(), "echo hello", 5000)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}
	if result.Stdout != "hello\n" {
		t.Fatalf("expected 'hello\\n', got %q", result.Stdout)
	}
}

func TestExecuteBashNonZeroExit(t *testing.T) {
	session := newTestSession(t, newTestModel())
	result, err := session.ExecuteBash(context.Background(), "exit 42", 5000)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 42 {
		t.Fatalf("expected exit code 42, got %d", result.ExitCode)
	}
}

func TestGetAvailableModels(t *testing.T) {
	session := newTestSession(t, newTestModel())
	models := session.GetAvailableModels()
	if len(models) == 0 {
		t.Fatal("expected at least some models")
	}
}

func TestExportHTML(t *testing.T) {
	session := newTestSession(t, newTestModel())

	session.GetAgent().AppendMessage(providers.UserMessage{Role: "user", Content: "test prompt"})
	session.GetAgent().AppendMessage(providers.AssistantMessage{
		Role:    "assistant",
		Content: []providers.ContentBlock{&providers.TextContent{Type: "text", Text: "test response"}},
	})

	dir := t.TempDir()
	outPath := filepath.Join(dir, "export.html")
	if err := session.ExportHTML(outPath); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "test prompt") {
		t.Fatal("expected user message in HTML")
	}
	if !strings.Contains(content, "test response") {
		t.Fatal("expected assistant message in HTML")
	}
}

func TestSubscribeSession(t *testing.T) {
	session := newTestSession(t, newTestModel())

	var received SessionEvent
	unsub := session.SubscribeSession(func(evt SessionEvent) {
		received = evt
	})
	defer unsub()

	session.SetThinkingLevel(agent.ThinkingLevelHigh)

	if received.Type != SessionEventThinkingChange {
		t.Fatalf("expected thinking_change event, got %s", received.Type)
	}
	if received.Level != string(agent.ThinkingLevelHigh) {
		t.Fatalf("expected level 'high', got %s", received.Level)
	}
}

func TestMaybeAutoCompact_DisabledByConfig(t *testing.T) {
	session := newTestSession(t, newTestModelWithContext(10000))

	session.config.AutoCompaction = false
	session.totalTokens = 9000

	for i := 0; i < 10; i++ {
		session.agent.AppendMessage(providers.UserMessage{Role: "user", Content: "msg"})
	}
	msgsBefore := len(session.agent.GetState().Messages)

	session.maybeAutoCompact(context.Background())

	msgsAfter := len(session.agent.GetState().Messages)
	if msgsAfter != msgsBefore {
		t.Fatal("should not compact when AutoCompaction is disabled")
	}
}
