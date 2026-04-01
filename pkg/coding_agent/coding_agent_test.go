package coding_agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/coding_agent/extension"
	sessionpkg "github.com/openmodu/modu/pkg/coding_agent/session"
	"github.com/openmodu/modu/pkg/coding_agent/skills"
	"github.com/openmodu/modu/pkg/coding_agent/subagent"
	"github.com/openmodu/modu/pkg/mailbox/client"
	"github.com/openmodu/modu/pkg/types"
)

type testEchoTool struct{}

func (t *testEchoTool) Name() string        { return "echo" }
func (t *testEchoTool) Label() string       { return "Echo" }
func (t *testEchoTool) Description() string { return "Echo test tool" }
func (t *testEchoTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"value": map[string]any{"type": "string"},
		},
		"required": []string{"value"},
	}
}
func (t *testEchoTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	value, _ := args["value"].(string)
	return agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "echoed: " + value}},
	}, nil
}

func TestNewCodingSession(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")

	model := &types.Model{
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

func TestNewCodingSessionRegistersSpawnAgentToolWhenMailboxConfigured(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")
	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:           dir,
		AgentDir:      agentDir,
		Model:         newTestModel(),
		MailboxClient: client.NewMailboxClient("orchestrator", "127.0.0.1:9999"),
		GetAPIKey:     func(provider string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	if !containsTool(session.GetActiveToolNames(), "spawn_agent") {
		t.Fatalf("expected spawn_agent in active tools, got %v", session.GetActiveToolNames())
	}
}

func TestNewCodingSessionRegistersTodoWriteTool(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")
	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:       dir,
		AgentDir:  agentDir,
		Model:     newTestModel(),
		GetAPIKey: func(provider string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	if !containsTool(session.GetActiveToolNames(), "todo_write") {
		t.Fatalf("expected todo_write in active tools, got %v", session.GetActiveToolNames())
	}

	var todoTool agent.AgentTool
	for _, tool := range session.GetAgent().GetState().Tools {
		if tool.Name() == "todo_write" {
			todoTool = tool
			break
		}
	}
	if todoTool == nil {
		t.Fatal("todo_write tool not found in agent state")
	}

	_, err = todoTool.Execute(context.Background(), "todo-1", map[string]any{
		"todos": []any{
			map[string]any{"content": "inspect package", "status": "completed"},
			map[string]any{"content": "patch code", "status": "in_progress"},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	todos := session.GetTodos()
	if len(todos) != 2 {
		t.Fatalf("expected 2 todos, got %d", len(todos))
	}
	if todos[1].Content != "patch code" || todos[1].Status != "in_progress" {
		t.Fatalf("unexpected todos: %#v", todos)
	}
}

func TestSpawnSubagentBackgroundAndTaskOutput(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")

	agentsDir := filepath.Join(agentDir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	agentDef := `---
description: background helper
background: true
---
Return a short completion message.`
	if err := os.WriteFile(filepath.Join(agentsDir, "helper.md"), []byte(agentDef), 0o644); err != nil {
		t.Fatal(err)
	}

	model := newTestModel()
	streamFn := func(ctx context.Context, _ *types.Model, llmCtx *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		stream := types.NewEventStream()
		go func() {
			last := llmCtx.Messages[len(llmCtx.Messages)-1]
			userText := ""
			if msg, ok := last.(types.UserMessage); ok {
				userText, _ = msg.Content.(string)
			}
			msg := &types.AssistantMessage{
				Role:       "assistant",
				ProviderID: model.ProviderID,
				Model:      model.ID,
				StopReason: "stop",
				Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: "bg-result: " + userText}},
				Timestamp:  time.Now().UnixMilli(),
			}
			stream.Push(types.StreamEvent{Type: "done", Reason: "stop", Message: msg})
			stream.Resolve(msg, nil)
			stream.Close()
		}()
		return stream, nil
	}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:       dir,
		AgentDir:  agentDir,
		Model:     model,
		GetAPIKey: func(provider string) (string, error) { return "", nil },
		StreamFn:  streamFn,
	})
	if err != nil {
		t.Fatal(err)
	}

	var spawnTool, outputTool agent.AgentTool
	for _, tool := range session.GetAgent().GetState().Tools {
		switch tool.Name() {
		case "spawn_subagent":
			spawnTool = tool
		case "task_output":
			outputTool = tool
		}
	}
	if spawnTool == nil || outputTool == nil {
		t.Fatalf("expected spawn_subagent and task_output tools, got %v", session.GetActiveToolNames())
	}

	result, err := spawnTool.Execute(context.Background(), "bg-1", map[string]any{
		"name": "helper",
		"task": "hello",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	details, _ := result.Details.(map[string]string)
	taskID, _ := details["task_id"]
	if taskID == "" {
		t.Fatalf("expected background task id in details, got %#v", result.Details)
	}

	var output string
	for i := 0; i < 20; i++ {
		res, err := outputTool.Execute(context.Background(), "out-1", map[string]any{
			"task_id": taskID,
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		output = extractTextBlocks(res.Content)
		if strings.Contains(output, "bg-result: hello") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(output, "bg-result: hello") {
		t.Fatalf("expected background task output, got %q", output)
	}
}

func TestPlanModeTools(t *testing.T) {
	session := newTestSession(t, newTestModel())
	var enterTool, exitTool agent.AgentTool
	for _, tool := range session.GetAgent().GetState().Tools {
		switch tool.Name() {
		case "enter_plan_mode":
			enterTool = tool
		case "exit_plan_mode":
			exitTool = tool
		}
	}
	if enterTool == nil || exitTool == nil {
		t.Fatalf("expected plan mode tools, got %v", session.GetActiveToolNames())
	}

	_, err := enterTool.Execute(context.Background(), "plan-1", map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !session.planMode {
		t.Fatal("expected plan mode enabled")
	}

	_, err = exitTool.Execute(context.Background(), "plan-2", map[string]any{"plan": "do work"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if session.planMode {
		t.Fatal("expected plan mode disabled")
	}
}

func TestEnterAndExitWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	dir := t.TempDir()
	initGitRepo(t, dir)

	agentDir := filepath.Join(dir, ".coding_agent")
	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:       dir,
		AgentDir:  agentDir,
		Model:     newTestModel(),
		GetAPIKey: func(provider string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	original := session.cwd
	path, err := session.EnterWorktree()
	if err != nil {
		t.Fatal(err)
	}
	if path == "" || session.cwd == original {
		t.Fatalf("expected worktree cwd change, got cwd=%s path=%s", session.cwd, path)
	}
	if _, err := os.Stat(filepath.Join(path, "README.md")); err != nil {
		t.Fatalf("expected repo file in worktree: %v", err)
	}

	if err := session.ExitWorktree(); err != nil {
		t.Fatal(err)
	}
	if session.cwd != original {
		t.Fatalf("expected cwd restored to %s, got %s", original, session.cwd)
	}
}

func TestSpawnSubagentIsolationWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	dir := t.TempDir()
	initGitRepo(t, dir)
	agentDir := filepath.Join(dir, ".coding_agent")
	agentsDir := filepath.Join(agentDir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	agentDef := `---
description: isolated helper
tools: bash
isolation: worktree
---
Run pwd in bash and return the tool output.`
	if err := os.WriteFile(filepath.Join(agentsDir, "isolated.md"), []byte(agentDef), 0o644); err != nil {
		t.Fatal(err)
	}

	model := newTestModel()
	callCount := 0
	streamFn := func(ctx context.Context, _ *types.Model, llmCtx *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		callCount++
		stream := types.NewEventStream()
		go func() {
			if callCount == 1 {
				msg := &types.AssistantMessage{
					Role:       "assistant",
					ProviderID: model.ProviderID,
					Model:      model.ID,
					StopReason: "toolUse",
					Content: []types.ContentBlock{
						&types.ToolCallContent{Type: "toolCall", ID: "tool-1", Name: "bash", Arguments: map[string]any{"command": "pwd"}},
					},
					Timestamp: time.Now().UnixMilli(),
				}
				stream.Push(types.StreamEvent{Type: "done", Reason: "toolUse", Message: msg})
				stream.Resolve(msg, nil)
			} else {
				last := llmCtx.Messages[len(llmCtx.Messages)-1]
				text := ""
				if toolResult, ok := last.(types.ToolResultMessage); ok {
					for _, block := range toolResult.Content {
						if tc, ok := block.(*types.TextContent); ok {
							text = tc.Text
						}
					}
				}
				msg := &types.AssistantMessage{
					Role:       "assistant",
					ProviderID: model.ProviderID,
					Model:      model.ID,
					StopReason: "stop",
					Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: text}},
					Timestamp:  time.Now().UnixMilli(),
				}
				stream.Push(types.StreamEvent{Type: "done", Reason: "stop", Message: msg})
				stream.Resolve(msg, nil)
			}
			stream.Close()
		}()
		return stream, nil
	}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:       dir,
		AgentDir:  agentDir,
		Model:     model,
		GetAPIKey: func(provider string) (string, error) { return "", nil },
		StreamFn:  streamFn,
	})
	if err != nil {
		t.Fatal(err)
	}

	var spawnTool agent.AgentTool
	for _, tool := range session.GetAgent().GetState().Tools {
		if tool.Name() == "spawn_subagent" {
			spawnTool = tool
			break
		}
	}
	if spawnTool == nil {
		t.Fatalf("expected spawn_subagent, got %v", session.GetActiveToolNames())
	}

	res, err := spawnTool.Execute(context.Background(), "iso-1", map[string]any{
		"name": "isolated",
		"task": "show cwd",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	output := extractTextBlocks(res.Content)
	if !strings.Contains(output, filepath.Join(agentDir, "worktrees")) {
		t.Fatalf("expected isolated worktree path, got %q", output)
	}
	if strings.Contains(output, dir) && !strings.Contains(output, filepath.Join(agentDir, "worktrees")) {
		t.Fatalf("expected worktree path instead of repo root, got %q", output)
	}
}

func TestCodingSessionRequiresCwd(t *testing.T) {
	_, err := NewCodingSession(CodingSessionOptions{
		Model: &types.Model{ID: "test"},
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

	model := &types.Model{
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

func newTestModel() *types.Model {
	return &types.Model{
		ID: "test", Api: "ollama", ProviderID: "ollama",
		ContextWindow: 8192, MaxTokens: 2048,
	}
}

func newTestModelWithContext(contextWindow int) *types.Model {
	return &types.Model{
		ID: "test", Api: "ollama", ProviderID: "ollama",
		ContextWindow: contextWindow, MaxTokens: 2048,
	}
}

func newTestSession(t *testing.T, model *types.Model) *CodingSession {
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

	session.agent.AppendMessage(types.UserMessage{Role: "user", Content: "msg1"})
	session.agent.AppendMessage(types.UserMessage{Role: "user", Content: "msg2"})
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
	session.streamFn = func(ctx context.Context, model *types.Model, llmCtx *types.LLMContext, opts *types.SimpleStreamOptions) (types.EventStream, error) {
		stream := types.NewEventStream()
		go func() {
			msg := &types.AssistantMessage{
				Role:       "assistant",
				ProviderID: model.ProviderID,
				Model:      model.ID,
				Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: "mock summary"}},
			}
			stream.Push(types.StreamEvent{Type: "done", Reason: "stop", Message: msg})
			stream.Resolve(msg, nil)
			stream.Close()
		}()
		return stream, nil
	}

	for i := 0; i < 10; i++ {
		session.agent.AppendMessage(types.UserMessage{Role: "user", Content: "msg"})
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
	model := &types.Model{
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
	model := &types.Model{
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

	session.GetAgent().AppendMessage(types.UserMessage{Role: "user", Content: "hi"})
	msgs = session.GetMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
}

func TestPromptPersistsAssistantAndToolMessages(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")
	model := newTestModel()
	tool := &testEchoTool{}
	callIndex := 0

	streamFn := func(ctx context.Context, _ *types.Model, _ *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		stream := types.NewEventStream()
		go func() {
			if callIndex == 0 {
				msg := &types.AssistantMessage{
					Role:       "assistant",
					ProviderID: model.ProviderID,
					Model:      model.ID,
					StopReason: "toolUse",
					Content: []types.ContentBlock{
						&types.ToolCallContent{Type: "toolCall", ID: "tool-1", Name: "echo", Arguments: map[string]any{"value": "hello"}},
					},
					Timestamp: time.Now().UnixMilli(),
				}
				stream.Push(types.StreamEvent{Type: "done", Reason: "toolUse", Message: msg})
				stream.Resolve(msg, nil)
			} else {
				msg := &types.AssistantMessage{
					Role:       "assistant",
					ProviderID: model.ProviderID,
					Model:      model.ID,
					StopReason: "stop",
					Content: []types.ContentBlock{
						&types.TextContent{Type: "text", Text: "done"},
					},
					Timestamp: time.Now().UnixMilli(),
				}
				stream.Push(types.StreamEvent{Type: "done", Reason: "stop", Message: msg})
				stream.Resolve(msg, nil)
			}
			callIndex++
			stream.Close()
		}()
		return stream, nil
	}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:       dir,
		AgentDir:  agentDir,
		Model:     model,
		Tools:     []agent.AgentTool{tool},
		GetAPIKey: func(p string) (string, error) { return "", nil },
		StreamFn:  streamFn,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := session.Prompt(context.Background(), "run echo"); err != nil {
		t.Fatal(err)
	}

	var roles []agent.MessageRole
	for _, entry := range session.sessionManager.Load() {
		if entry.Type != sessionpkg.EntryTypeMessage {
			continue
		}
		if data, ok := entry.Data.(sessionpkg.MessageData); ok {
			roles = append(roles, data.Role)
			continue
		}
		if data, ok := entry.Data.(map[string]any); ok {
			if role, ok := data["role"].(string); ok {
				roles = append(roles, agent.MessageRole(role))
			}
		}
	}

	if !containsRole(roles, agent.RoleUser) {
		t.Fatalf("expected user message to be persisted, got roles=%v", roles)
	}
	if !containsRole(roles, agent.RoleAssistant) {
		t.Fatalf("expected assistant message to be persisted, got roles=%v", roles)
	}
	if !containsRole(roles, agent.RoleToolResult) {
		t.Fatalf("expected tool result message to be persisted, got roles=%v", roles)
	}

	if _, err := os.Stat(session.messagesFilePath()); err != nil {
		t.Fatalf("expected messages snapshot to exist: %v", err)
	}
}

func TestPromptSlashSkillRunsInIsolatedAgent(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")

	skillDir := filepath.Join(agentDir, "skills", "summarize")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillContent := `---
description: summarize content
---
You are a summarizer. Reply with a concise summary of the user's request.`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0o644); err != nil {
		t.Fatal(err)
	}

	model := newTestModel()
	streamFn := func(ctx context.Context, _ *types.Model, llmCtx *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		stream := types.NewEventStream()
		go func() {
			last := llmCtx.Messages[len(llmCtx.Messages)-1]
			userText := ""
			if msg, ok := last.(types.UserMessage); ok {
				userText, _ = msg.Content.(string)
			}
			msg := &types.AssistantMessage{
				Role:       "assistant",
				ProviderID: model.ProviderID,
				Model:      model.ID,
				StopReason: "stop",
				Content: []types.ContentBlock{
					&types.TextContent{Type: "text", Text: "skill-result: " + userText},
				},
				Timestamp: time.Now().UnixMilli(),
			}
			stream.Push(types.StreamEvent{Type: "done", Reason: "stop", Message: msg})
			stream.Resolve(msg, nil)
			stream.Close()
		}()
		return stream, nil
	}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:       dir,
		AgentDir:  agentDir,
		Model:     model,
		GetAPIKey: func(p string) (string, error) { return "", nil },
		StreamFn:  streamFn,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := session.Prompt(context.Background(), "/summarize hello world"); err != nil {
		t.Fatal(err)
	}

	got := session.GetLastAssistantText()
	if got != "skill-result: hello world" {
		t.Fatalf("expected isolated skill result, got %q", got)
	}
}

func TestPrepareSubagentDefinitionInjectsSkillsAndMemory(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")

	skillDir := filepath.Join(agentDir, "skills", "helper")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\ndescription: helper skill\n---\nUse helper instructions."), 0o644); err != nil {
		t.Fatal(err)
	}

	skillMgr := skills.NewManager(agentDir, dir)
	if err := skillMgr.Discover(); err != nil {
		t.Fatal(err)
	}

	mem := NewMemoryStore(agentDir, dir)
	if err := mem.WriteGlobalLongTerm("global note"); err != nil {
		t.Fatal(err)
	}
	if err := mem.WriteProjectLongTerm("project note"); err != nil {
		t.Fatal(err)
	}

	def := prepareSubagentDefinition(&subagent.SubagentDefinition{
		Name:         "worker",
		SystemPrompt: "Base prompt.",
		Skills:       []string{"helper"},
		MemoryScope:  "both",
	}, skillMgr, mem)

	if !strings.Contains(def.SystemPrompt, "Base prompt.") {
		t.Fatalf("expected base prompt in %q", def.SystemPrompt)
	}
	if !strings.Contains(def.SystemPrompt, "Use helper instructions.") {
		t.Fatalf("expected skill content in %q", def.SystemPrompt)
	}
	if !strings.Contains(def.SystemPrompt, "global note") || !strings.Contains(def.SystemPrompt, "project note") {
		t.Fatalf("expected memory context in %q", def.SystemPrompt)
	}
}

// --- New method tests ---

func TestGetLastAssistantText(t *testing.T) {
	session := newTestSession(t, newTestModel())

	text := session.GetLastAssistantText()
	if text != "" {
		t.Fatalf("expected empty, got %s", text)
	}

	session.GetAgent().AppendMessage(types.AssistantMessage{
		Role: "assistant",
		Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: "hello from assistant"},
		},
	})

	text = session.GetLastAssistantText()
	if text != "hello from assistant" {
		t.Fatalf("expected 'hello from assistant', got %s", text)
	}

	session.GetAgent().AppendMessage(types.UserMessage{Role: "user", Content: "question"})
	session.GetAgent().AppendMessage(types.AssistantMessage{
		Role: "assistant",
		Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: "second response"},
		},
	})

	text = session.GetLastAssistantText()
	if text != "second response" {
		t.Fatalf("expected 'second response', got %s", text)
	}
}

func containsRole(roles []agent.MessageRole, want agent.MessageRole) bool {
	for _, role := range roles {
		if role == want {
			return true
		}
	}
	return false
}

func containsTool(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

func extractTextBlocks(content []types.ContentBlock) string {
	var parts []string
	for _, block := range content {
		if tc, ok := block.(*types.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}
	run("init")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "init")
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

	session.GetAgent().AppendMessage(types.UserMessage{Role: "user", Content: "hi"})
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

	session.GetAgent().AppendMessage(types.UserMessage{Role: "user", Content: "test prompt"})
	session.GetAgent().AppendMessage(types.AssistantMessage{
		Role:    "assistant",
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "test response"}},
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
		session.agent.AppendMessage(types.UserMessage{Role: "user", Content: "msg"})
	}
	msgsBefore := len(session.agent.GetState().Messages)

	session.maybeAutoCompact(context.Background())

	msgsAfter := len(session.agent.GetState().Messages)
	if msgsAfter != msgsBefore {
		t.Fatal("should not compact when AutoCompaction is disabled")
	}
}
