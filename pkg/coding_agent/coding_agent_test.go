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
	"github.com/openmodu/modu/pkg/coding_agent/compaction"
	"github.com/openmodu/modu/pkg/coding_agent/extension"
	sessionpkg "github.com/openmodu/modu/pkg/coding_agent/session"
	"github.com/openmodu/modu/pkg/coding_agent/skills"
	"github.com/openmodu/modu/pkg/coding_agent/subagent"
	"github.com/openmodu/modu/pkg/mailbox/client"
	"github.com/openmodu/modu/pkg/types"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
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

type testHintTool struct{}

func (t *testHintTool) Name() string        { return "hint_tool" }
func (t *testHintTool) Label() string       { return "Hint Tool" }
func (t *testHintTool) Description() string { return "Emit a harness hint in output" }
func (t *testHintTool) Parameters() any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}
func (t *testHintTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	return agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{
			Type: "text",
			Text: "visible output\n<claude-code-hint v=1 type=plugin value=test@local />",
		}},
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

func TestNewCodingSessionHonorsFeatureGates(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configContent := `{"features":{"todoTool":false,"taskOutputTool":false,"planMode":false,"worktreeMode":false,"memoryTool":false}}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:       dir,
		AgentDir:  agentDir,
		Model:     newTestModel(),
		GetAPIKey: func(provider string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"todo_write", "task_output", "enter_plan_mode", "exit_plan_mode", "enter_worktree", "exit_worktree", "memory"} {
		if containsTool(session.GetActiveToolNames(), name) {
			t.Fatalf("expected feature-gated tool %s to be disabled, got %v", name, session.GetActiveToolNames())
		}
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

	// Enable task_output explicitly for this test (it's off by default).
	cfg := DefaultConfig()
	cfg.Features.TaskOutputTool = boolPtr(true)
	settingsPath := filepath.Join(agentDir, "settings.json")
	if err := SaveConfig(cfg, settingsPath); err != nil {
		t.Fatal(err)
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
	if !cfg.HarnessEnableActions() {
		t.Fatal("harness actions should be enabled by default")
	}
	if cfg.Harness.LogFiles.ToolUse == "" || cfg.Harness.ArtifactFiles.ToolUse == "" || cfg.Harness.BridgeDirs.ToolUse == "" {
		t.Fatalf("expected default harness outputs, got %#v", cfg.Harness)
	}
}

func TestLoadConfigMissing(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agent")
	cwd := filepath.Join(dir, "repo")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(agentDir, cwd)
	if err != nil {
		t.Fatalf("loading missing config should not error: %v", err)
	}
	if cfg.ThinkingLevel == "" {
		t.Fatal("should have defaults when files are missing")
	}
	data, err := os.ReadFile(filepath.Join(agentDir, "settings.json"))
	if err != nil {
		t.Fatalf("expected default settings.json bootstrap, got %v", err)
	}
	if !strings.Contains(string(data), `"harness"`) {
		t.Fatalf("expected bootstrapped settings to include harness config, got %q", string(data))
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

func TestLoadConfigRejectsDisallowedHarnessActionCommand(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configContent := `{"harness":{"actions":{"toolUse":[{"type":"exec","command":"sh"}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(agentDir, dir); err == nil {
		t.Fatal("expected invalid harness action command to be rejected")
	}
}

func TestLoadConfigRejectsDisallowedHarnessActionDir(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agent")
	cwd := filepath.Join(dir, "repo")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	configContent := `{"harness":{"actionPolicy":{"allowDirPrefixes":["/tmp/safe"]},"actions":{"toolUse":[{"type":"exec","command":"/bin/sh","dir":"/tmp/unsafe"}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(agentDir, cwd); err == nil || !strings.Contains(err.Error(), "dir not allowed by policy") {
		t.Fatalf("expected dir policy error, got %v", err)
	}
}

func TestLoadConfigRejectsInvalidHarnessActionOnFailure(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agent")
	cwd := filepath.Join(dir, "repo")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	configContent := `{"harness":{"actions":{"toolUse":[{"type":"exec","command":"/bin/sh","onFailure":"panic"}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(agentDir, cwd); err == nil || !strings.Contains(err.Error(), "onFailure") {
		t.Fatalf("expected onFailure validation error, got %v", err)
	}
}

func TestRuntimeStateFileAndJSON(t *testing.T) {
	session := newTestSession(t, newTestModel())
	session.SetTodos([]TodoItem{{Content: "plan work", Status: "in_progress"}})
	stateJSON := session.RuntimeStateJSON()
	if !strings.Contains(stateJSON, `"todos"`) || !strings.Contains(stateJSON, `"features"`) {
		t.Fatalf("expected runtime state json, got %q", stateJSON)
	}
	data, err := os.ReadFile(session.RuntimePaths().RuntimeStateFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"todo_tool": true`) || !strings.Contains(string(data), `"content": "plan work"`) {
		t.Fatalf("unexpected runtime state file: %q", string(data))
	}
}

func TestCodingSessionWritesTraceFiles(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")
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
						&types.ToolCallContent{Type: "toolCall", ID: "tool-1", Name: "echo", Arguments: map[string]any{"value": "trace-me"}},
					},
					Timestamp: time.Now().UnixMilli(),
				}
				stream.Push(types.StreamEvent{Type: "done", Reason: "toolUse", Message: msg})
				stream.Resolve(msg, nil)
			} else {
				last := llmCtx.Messages[len(llmCtx.Messages)-1]
				toolOutput := ""
				if result, ok := last.(types.ToolResultMessage); ok {
					toolOutput = extractTextBlocks(result.Content)
				}
				msg := &types.AssistantMessage{
					Role:       "assistant",
					ProviderID: model.ProviderID,
					Model:      model.ID,
					StopReason: "stop",
					Content: []types.ContentBlock{
						&types.TextContent{Type: "text", Text: "final: " + toolOutput},
					},
					Usage:     types.AgentUsage{Input: 13, Output: 8, TotalTokens: 21},
					Timestamp: time.Now().UnixMilli(),
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
		Tools:     []agent.AgentTool{&testEchoTool{}},
		GetAPIKey: func(provider string) (string, error) { return "", nil },
		StreamFn:  streamFn,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := session.Prompt(context.Background(), "capture trace"); err != nil {
		t.Fatal(err)
	}

	traceSummary := session.TraceSummary()
	if traceSummary.Tokens.TotalTokens != 21 {
		t.Fatalf("expected trace token total 21, got %#v", traceSummary.Tokens)
	}
	if traceSummary.Counts.ToolCalls != 1 {
		t.Fatalf("expected 1 traced tool call, got %#v", traceSummary.Counts)
	}

	eventsData, err := os.ReadFile(session.RuntimePaths().TraceEventsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(eventsData), `"type":"tool_execution_start"`) || !strings.Contains(string(eventsData), `"toolName":"echo"`) {
		t.Fatalf("expected tool execution trace, got %q", string(eventsData))
	}

	summaryData, err := os.ReadFile(session.RuntimePaths().TraceSummaryFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(summaryData), `"totalTokens": 21`) {
		t.Fatalf("expected total token summary, got %q", string(summaryData))
	}

	stateData, err := os.ReadFile(session.RuntimePaths().RuntimeStateFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stateData), `"trace_events_file"`) || !strings.Contains(string(stateData), `"totalTokens": 21`) {
		t.Fatalf("expected trace paths and totals in runtime state, got %q", string(stateData))
	}
}

func TestCodingSessionWritesOTelSpans(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")
	model := newTestModel()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer func() {
		_ = provider.Shutdown(context.Background())
	}()

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
						&types.ToolCallContent{Type: "toolCall", ID: "tool-otel-1", Name: "echo", Arguments: map[string]any{"value": "otel"}},
					},
					Timestamp: time.Now().UnixMilli(),
				}
				stream.Push(types.StreamEvent{Type: "done", Reason: "toolUse", Message: msg})
				stream.Resolve(msg, nil)
			} else {
				last := llmCtx.Messages[len(llmCtx.Messages)-1]
				toolOutput := ""
				if result, ok := last.(types.ToolResultMessage); ok {
					toolOutput = extractTextBlocks(result.Content)
				}
				msg := &types.AssistantMessage{
					Role:       "assistant",
					ProviderID: model.ProviderID,
					Model:      model.ID,
					StopReason: "stop",
					Content: []types.ContentBlock{
						&types.TextContent{Type: "text", Text: "otel final: " + toolOutput},
					},
					Usage:     types.AgentUsage{Input: 9, Output: 5, TotalTokens: 14},
					Timestamp: time.Now().UnixMilli(),
				}
				stream.Push(types.StreamEvent{Type: "done", Reason: "stop", Message: msg})
				stream.Resolve(msg, nil)
			}
			stream.Close()
		}()
		return stream, nil
	}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:                dir,
		AgentDir:           agentDir,
		Model:              model,
		Tools:              []agent.AgentTool{&testEchoTool{}},
		GetAPIKey:          func(provider string) (string, error) { return "", nil },
		StreamFn:           streamFn,
		OTelTracerProvider: provider,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := session.Prompt(context.Background(), "capture otel trace"); err != nil {
		t.Fatal(err)
	}
	session.Close("test done")

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected exported spans")
	}

	toolSpan, ok := findSpanByName(spans, "coding_agent.tool.echo")
	if !ok {
		t.Fatalf("expected tool span, got %#v", spans)
	}
	if got := spanStringAttr(toolSpan.Attributes, "tool.name"); got != "echo" {
		t.Fatalf("expected tool.name=echo, got %q", got)
	}

	llmSpans := findSpansByName(spans, "coding_agent.llm")
	if len(llmSpans) == 0 {
		t.Fatalf("expected llm spans, got %#v", spans)
	}
	foundTokenSpan := false
	for _, span := range llmSpans {
		if spanIntAttr(span.Attributes, "llm.usage.total_tokens") == 14 {
			foundTokenSpan = true
			break
		}
	}
	if !foundTokenSpan {
		t.Fatalf("expected one llm span with total tokens 14, got %#v", llmSpans)
	}

	sessionSpan, ok := findSpanByName(spans, "coding_agent.session")
	if !ok {
		t.Fatalf("expected session span, got %#v", spans)
	}
	if got := spanStringAttr(sessionSpan.Attributes, "llm.model"); got != model.ID {
		t.Fatalf("expected session llm.model=%s, got %q", model.ID, got)
	}
}

func TestPermissionRulesEmitHarnessArtifacts(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configContent := `{"permissions":{"denyTools":["echo"]}}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}
	model := newTestModel()
	callCount := 0
	streamFn := func(ctx context.Context, _ *types.Model, llmCtx *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		callCount++
		stream := types.NewEventStream()
		go func() {
			var msg *types.AssistantMessage
			if callCount == 1 {
				msg = &types.AssistantMessage{
					Role:       "assistant",
					ProviderID: model.ProviderID,
					Model:      model.ID,
					StopReason: "toolUse",
					Content: []types.ContentBlock{
						&types.ToolCallContent{Type: "toolCall", ID: "tool-1", Name: "echo", Arguments: map[string]any{"value": "hi"}},
					},
					Timestamp: time.Now().UnixMilli(),
				}
			} else {
				msg = &types.AssistantMessage{
					Role:       "assistant",
					ProviderID: model.ProviderID,
					Model:      model.ID,
					StopReason: "stop",
					Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: "done"}},
					Timestamp:  time.Now().UnixMilli(),
				}
			}
			stream.Push(types.StreamEvent{Type: "done", Reason: msg.StopReason, Message: msg})
			stream.Resolve(msg, nil)
			stream.Close()
		}()
		return stream, nil
	}
	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:         dir,
		AgentDir:    agentDir,
		Model:       model,
		CustomTools: []agent.AgentTool{&testEchoTool{}},
		GetAPIKey:   func(provider string) (string, error) { return "", nil },
		StreamFn:    streamFn,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Prompt(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	artifactPath := filepath.Join(agentDir, "artifacts", "permission-latest.json")
	data, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"event": "permission_denied"`) || !strings.Contains(string(data), `"tool": "echo"`) {
		t.Fatalf("expected permission artifact, got %q", string(data))
	}
}

func TestDefaultHarnessOutputsWorkWithoutManualSettings(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:         dir,
		AgentDir:    agentDir,
		Model:       newTestModel(),
		CustomTools: []agent.AgentTool{&testEchoTool{}},
		GetAPIKey:   func(provider string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	var echoTool agent.AgentTool
	for _, tool := range session.GetAgent().GetState().Tools {
		if tool.Name() == "echo" {
			echoTool = tool
			break
		}
	}
	if echoTool == nil {
		t.Fatal("expected echo tool")
	}
	if _, err := echoTool.Execute(context.Background(), "echo-auto-default-1", map[string]any{"value": "ok"}, nil); err != nil {
		t.Fatal(err)
	}

	cfg := session.GetConfig()
	checkExists := func(target string) {
		path := target
		if !filepath.IsAbs(path) {
			path = filepath.Join(agentDir, path)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected default harness output at %s, got %v", path, err)
		}
	}
	checkExists(cfg.Harness.LogFiles.ToolUse)
	checkExists(cfg.Harness.ArtifactFiles.ToolUse)
	bridgeDir := cfg.Harness.BridgeDirs.ToolUse
	if !filepath.IsAbs(bridgeDir) {
		bridgeDir = filepath.Join(agentDir, bridgeDir)
	}
	entries, err := os.ReadDir(bridgeDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected default bridge events in %s, err=%v", bridgeDir, err)
	}
	if _, err := os.Stat(session.RuntimePaths().RuntimeIndexFile); err != nil {
		t.Fatalf("expected runtime index file, got %v", err)
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
		Name:              "worker",
		SystemPrompt:      "Base prompt.",
		Skills:            []string{"helper"},
		MemoryScope:       "both",
		DisallowedTools:   []string{"bash"},
		HarnessBlockTools: []string{"edit", "write"},
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
	if len(def.DisallowedTools) != 3 || def.DisallowedTools[0] != "bash" || def.DisallowedTools[1] != "edit" || def.DisallowedTools[2] != "write" {
		t.Fatalf("expected merged disallowed tools, got %#v", def.DisallowedTools)
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

func TestHarnessHooksWrapToolExecution(t *testing.T) {
	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:         t.TempDir(),
		AgentDir:    filepath.Join(t.TempDir(), ".coding_agent"),
		Model:       newTestModel(),
		CustomTools: []agent.AgentTool{&testEchoTool{}},
		GetAPIKey:   func(provider string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	var preCalled, postCalled bool
	session.RegisterHarnessHook(HarnessHook{
		PreToolUse: func(call HarnessToolCall) error {
			if call.ToolName == "echo" {
				preCalled = true
			}
			return nil
		},
		PostToolUse: func(call HarnessToolCall, result agent.AgentToolResult, err error) {
			if call.ToolName == "echo" && err == nil {
				postCalled = true
			}
		},
	})

	var echo agent.AgentTool
	for _, tool := range session.GetAgent().GetState().Tools {
		if tool.Name() == "echo" {
			echo = tool
			break
		}
	}
	if echo == nil {
		t.Fatal("expected wrapped echo tool")
	}

	_, err = echo.Execute(context.Background(), "echo-1", map[string]any{"value": "ok"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !preCalled || !postCalled {
		t.Fatalf("expected harness hooks to run, pre=%v post=%v", preCalled, postCalled)
	}
}

func TestHarnessHintStrippedAndStored(t *testing.T) {
	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:         t.TempDir(),
		AgentDir:    filepath.Join(t.TempDir(), ".coding_agent"),
		Model:       newTestModel(),
		CustomTools: []agent.AgentTool{&testHintTool{}},
		GetAPIKey:   func(provider string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	var hintTool agent.AgentTool
	for _, tool := range session.GetAgent().GetState().Tools {
		if tool.Name() == "hint_tool" {
			hintTool = tool
			break
		}
	}
	if hintTool == nil {
		t.Fatal("expected wrapped hint tool")
	}

	result, err := hintTool.Execute(context.Background(), "hint-1", map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].(*types.TextContent).Text
	if strings.Contains(text, "claude-code-hint") {
		t.Fatalf("expected hint tag to be stripped, got %q", text)
	}
	hints := session.GetPendingHarnessHints()
	if len(hints) != 1 || hints[0].Value != "test@local" {
		t.Fatalf("expected stored harness hint, got %#v", hints)
	}
	entries, err := os.ReadDir(session.RuntimePaths().ToolResultsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("expected tool result artifact to be written")
	}
}

func TestHarnessPathsToolAndPlanFile(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(t.TempDir(), ".coding_agent")
	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:       dir,
		AgentDir:  agentDir,
		Model:     newTestModel(),
		GetAPIKey: func(provider string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	var harnessPathsTool, readTool agent.AgentTool
	for _, tool := range session.GetAgent().GetState().Tools {
		switch tool.Name() {
		case "harness_paths":
			harnessPathsTool = tool
		case "read":
			readTool = tool
		}
	}
	if harnessPathsTool == nil || readTool == nil {
		t.Fatalf("expected harness_paths and read tools, got %v", session.GetActiveToolNames())
	}

	session.ExitPlanMode("ship feature safely")
	paths := session.RuntimePaths()
	if _, err := os.Stat(paths.PlanFile); err != nil {
		t.Fatalf("expected plan file to exist: %v", err)
	}

	result, err := harnessPathsTool.Execute(context.Background(), "paths-1", map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	details, _ := result.Details.(map[string]any)
	if details["plan_file"] != paths.PlanFile {
		t.Fatalf("expected plan_file detail %q, got %#v", paths.PlanFile, details)
	}

	readResult, err := readTool.Execute(context.Background(), "read-plan", map[string]any{"path": paths.PlanFile}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readResult.Content[0].(*types.TextContent).Text, "ship feature safely") {
		t.Fatalf("expected read tool to access harness plan file, got %#v", readResult.Content)
	}
}

func TestHarnessCompactHooksRun(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(t.TempDir(), ".coding_agent")
	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:      dir,
		AgentDir: agentDir,
		Model:    newTestModel(),
		GetAPIKey: func(provider string) (string, error) {
			return "", nil
		},
		StreamFn: func(ctx context.Context, _ *types.Model, llmCtx *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
			stream := types.NewEventStream()
			go func() {
				msg := &types.AssistantMessage{
					Role:       "assistant",
					ProviderID: "mock",
					Model:      "mock",
					StopReason: "stop",
					Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: "summary ok"}},
					Timestamp:  time.Now().UnixMilli(),
				}
				stream.Push(types.StreamEvent{Type: "done", Reason: "stop", Message: msg})
				stream.Resolve(msg, nil)
				stream.Close()
			}()
			return stream, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	session.GetAgent().AppendMessage(types.UserMessage{Role: "user", Content: "one"})
	session.GetAgent().AppendMessage(types.AssistantMessage{Role: "assistant", Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "two"}}})
	session.GetAgent().AppendMessage(types.UserMessage{Role: "user", Content: "three"})
	session.GetAgent().AppendMessage(types.AssistantMessage{Role: "assistant", Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "four"}}})
	session.GetAgent().AppendMessage(types.UserMessage{Role: "user", Content: "five"})

	var preCount, postCount int
	session.RegisterHarnessHook(HarnessHook{
		PreCompact: func(messageCount int) error {
			preCount = messageCount
			return nil
		},
		PostCompact: func(result *compaction.Result, err error) {
			if err == nil && result != nil {
				postCount = result.NewCount
			}
		},
	})

	if err := session.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	if preCount != 5 || postCount == 0 {
		t.Fatalf("expected compact hooks to run, pre=%d post=%d", preCount, postCount)
	}
}

func TestHarnessSubagentHooksRun(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, ".coding_agent")
	agentsDir := filepath.Join(agentDir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "reviewer.md"), []byte("review stuff"), 0o600); err != nil {
		t.Fatal(err)
	}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:      root,
		AgentDir: agentDir,
		Model:    newTestModel(),
		GetAPIKey: func(provider string) (string, error) {
			return "", nil
		},
		StreamFn: func(ctx context.Context, _ *types.Model, llmCtx *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
			stream := types.NewEventStream()
			go func() {
				msg := &types.AssistantMessage{
					Role:       "assistant",
					ProviderID: "mock",
					Model:      "mock",
					StopReason: "stop",
					Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: "subagent ok"}},
					Timestamp:  time.Now().UnixMilli(),
				}
				stream.Push(types.StreamEvent{Type: "done", Reason: "stop", Message: msg})
				stream.Resolve(msg, nil)
				stream.Close()
			}()
			return stream, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var started, stopped bool
	session.RegisterHarnessHook(HarnessHook{
		SubagentStart: func(run HarnessSubagentRun) {
			if run.Name == "reviewer" {
				started = true
			}
		},
		SubagentStop: func(run HarnessSubagentRun, result string, err error) {
			if run.Name == "reviewer" && err == nil && result == "subagent ok" {
				stopped = true
			}
		},
	})

	var tool agent.AgentTool
	for _, ttool := range session.GetAgent().GetState().Tools {
		if ttool.Name() == "spawn_subagent" {
			tool = ttool
			break
		}
	}
	if tool == nil {
		t.Fatal("expected spawn_subagent tool")
	}

	_, err = tool.Execute(context.Background(), "spawn-1", map[string]any{"name": "reviewer", "task": "check code"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !started || !stopped {
		t.Fatalf("expected subagent hooks to run, started=%v stopped=%v", started, stopped)
	}
}

func TestHarnessConfigBlocksTools(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configContent := `{"harness":{"blockTools":["echo"]}}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:         dir,
		AgentDir:    agentDir,
		Model:       newTestModel(),
		CustomTools: []agent.AgentTool{&testEchoTool{}},
		GetAPIKey:   func(provider string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	var echo agent.AgentTool
	for _, tool := range session.GetAgent().GetState().Tools {
		if tool.Name() == "echo" {
			echo = tool
			break
		}
	}
	if echo == nil {
		t.Fatal("expected echo tool")
	}

	result, err := echo.Execute(context.Background(), "echo-2", map[string]any{"value": "blocked"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := extractTextBlocks(result.Content)
	if !strings.Contains(text, "harness blocked echo") {
		t.Fatalf("expected config-driven harness block, got %q", text)
	}
}

func TestHarnessConfigCanDisableHintCaptureAndArtifacts(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configContent := `{"harness":{"captureHints":false,"persistToolResults":false}}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:         dir,
		AgentDir:    agentDir,
		Model:       newTestModel(),
		CustomTools: []agent.AgentTool{&testHintTool{}},
		GetAPIKey:   func(provider string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	var hintTool agent.AgentTool
	for _, tool := range session.GetAgent().GetState().Tools {
		if tool.Name() == "hint_tool" {
			hintTool = tool
			break
		}
	}
	if hintTool == nil {
		t.Fatal("expected hint tool")
	}

	result, err := hintTool.Execute(context.Background(), "hint-2", map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := extractTextBlocks(result.Content)
	if !strings.Contains(text, "<claude-code-hint") {
		t.Fatalf("expected hint tag to remain visible when capture is disabled, got %q", text)
	}
	if hints := session.GetPendingHarnessHints(); len(hints) != 0 {
		t.Fatalf("expected no stored hints, got %#v", hints)
	}
	entries, err := os.ReadDir(session.RuntimePaths().ToolResultsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no tool artifacts, got %d entries", len(entries))
	}
}

func TestHarnessConfigAppendsEventLogs(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")
	if err := os.MkdirAll(filepath.Join(agentDir, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "agents", "reviewer.md"), []byte("review stuff"), 0o600); err != nil {
		t.Fatal(err)
	}
	configContent := `{"harness":{"logFiles":{"toolUse":"logs/tool-use.jsonl","compact":"logs/compact.jsonl","subagent":"logs/subagent.jsonl"}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:         dir,
		AgentDir:    agentDir,
		Model:       newTestModel(),
		CustomTools: []agent.AgentTool{&testEchoTool{}},
		GetAPIKey:   func(provider string) (string, error) { return "", nil },
		StreamFn: func(ctx context.Context, _ *types.Model, llmCtx *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
			stream := types.NewEventStream()
			go func() {
				msg := &types.AssistantMessage{
					Role:       "assistant",
					ProviderID: "mock",
					Model:      "mock",
					StopReason: "stop",
					Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: "subagent ok"}},
					Timestamp:  time.Now().UnixMilli(),
				}
				stream.Push(types.StreamEvent{Type: "done", Reason: "stop", Message: msg})
				stream.Resolve(msg, nil)
				stream.Close()
			}()
			return stream, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var echoTool, spawnTool agent.AgentTool
	for _, tool := range session.GetAgent().GetState().Tools {
		switch tool.Name() {
		case "echo":
			echoTool = tool
		case "spawn_subagent":
			spawnTool = tool
		}
	}
	if echoTool == nil || spawnTool == nil {
		t.Fatalf("expected echo and spawn_subagent, got %v", session.GetActiveToolNames())
	}

	if _, err := echoTool.Execute(context.Background(), "echo-log-1", map[string]any{"value": "ok"}, nil); err != nil {
		t.Fatal(err)
	}
	session.GetAgent().AppendMessage(types.UserMessage{Role: "user", Content: "one"})
	session.GetAgent().AppendMessage(types.AssistantMessage{Role: "assistant", Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "two"}}})
	session.GetAgent().AppendMessage(types.UserMessage{Role: "user", Content: "three"})
	session.GetAgent().AppendMessage(types.AssistantMessage{Role: "assistant", Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "four"}}})
	session.GetAgent().AppendMessage(types.UserMessage{Role: "user", Content: "five"})
	if err := session.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := spawnTool.Execute(context.Background(), "spawn-log-1", map[string]any{"name": "reviewer", "task": "check code"}, nil); err != nil {
		t.Fatal(err)
	}

	checkLog := func(rel string, want string) {
		data, err := os.ReadFile(filepath.Join(agentDir, rel))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), want) {
			t.Fatalf("expected %s log to contain %q, got %q", rel, want, string(data))
		}
	}
	checkLog("logs/tool-use.jsonl", `"tool":"echo"`)
	checkLog("logs/compact.jsonl", `"event":"post_compact"`)
	checkLog("logs/subagent.jsonl", `"name":"reviewer"`)
}

func TestHarnessConfigWritesLatestArtifacts(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")
	if err := os.MkdirAll(filepath.Join(agentDir, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "agents", "reviewer.md"), []byte("review stuff"), 0o600); err != nil {
		t.Fatal(err)
	}
	configContent := `{"harness":{"artifactFiles":{"toolUse":"artifacts/tool-use-latest.json","compact":"artifacts/compact-latest.json","subagent":"artifacts/subagent-latest.json"}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:         dir,
		AgentDir:    agentDir,
		Model:       newTestModel(),
		CustomTools: []agent.AgentTool{&testEchoTool{}},
		GetAPIKey:   func(provider string) (string, error) { return "", nil },
		StreamFn: func(ctx context.Context, _ *types.Model, llmCtx *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
			stream := types.NewEventStream()
			go func() {
				msg := &types.AssistantMessage{
					Role:       "assistant",
					ProviderID: "mock",
					Model:      "mock",
					StopReason: "stop",
					Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: "subagent ok"}},
					Timestamp:  time.Now().UnixMilli(),
				}
				stream.Push(types.StreamEvent{Type: "done", Reason: "stop", Message: msg})
				stream.Resolve(msg, nil)
				stream.Close()
			}()
			return stream, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var echoTool, spawnTool agent.AgentTool
	for _, tool := range session.GetAgent().GetState().Tools {
		switch tool.Name() {
		case "echo":
			echoTool = tool
		case "spawn_subagent":
			spawnTool = tool
		}
	}
	if echoTool == nil || spawnTool == nil {
		t.Fatalf("expected echo and spawn_subagent, got %v", session.GetActiveToolNames())
	}

	if _, err := echoTool.Execute(context.Background(), "echo-artifact-1", map[string]any{"value": "ok"}, nil); err != nil {
		t.Fatal(err)
	}
	session.GetAgent().AppendMessage(types.UserMessage{Role: "user", Content: "one"})
	session.GetAgent().AppendMessage(types.AssistantMessage{Role: "assistant", Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "two"}}})
	session.GetAgent().AppendMessage(types.UserMessage{Role: "user", Content: "three"})
	session.GetAgent().AppendMessage(types.AssistantMessage{Role: "assistant", Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "four"}}})
	session.GetAgent().AppendMessage(types.UserMessage{Role: "user", Content: "five"})
	if err := session.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := spawnTool.Execute(context.Background(), "spawn-artifact-1", map[string]any{"name": "reviewer", "task": "check code"}, nil); err != nil {
		t.Fatal(err)
	}

	checkArtifact := func(rel string, want string) {
		data, err := os.ReadFile(filepath.Join(agentDir, rel))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), want) {
			t.Fatalf("expected %s artifact to contain %q, got %q", rel, want, string(data))
		}
	}
	checkArtifact("artifacts/tool-use-latest.json", `"tool": "spawn_subagent"`)
	checkArtifact("artifacts/compact-latest.json", `"event": "post_compact"`)
	checkArtifact("artifacts/subagent-latest.json", `"event": "subagent_stop"`)
}

func TestHarnessConfigWritesEventBridgeFiles(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")
	if err := os.MkdirAll(filepath.Join(agentDir, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "agents", "reviewer.md"), []byte("review stuff"), 0o600); err != nil {
		t.Fatal(err)
	}
	configContent := `{"harness":{"bridgeDirs":{"toolUse":"bridge/tool-use","compact":"bridge/compact","subagent":"bridge/subagent"}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:         dir,
		AgentDir:    agentDir,
		Model:       newTestModel(),
		CustomTools: []agent.AgentTool{&testEchoTool{}},
		GetAPIKey:   func(provider string) (string, error) { return "", nil },
		StreamFn: func(ctx context.Context, _ *types.Model, llmCtx *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
			stream := types.NewEventStream()
			go func() {
				msg := &types.AssistantMessage{
					Role:       "assistant",
					ProviderID: "mock",
					Model:      "mock",
					StopReason: "stop",
					Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: "subagent ok"}},
					Timestamp:  time.Now().UnixMilli(),
				}
				stream.Push(types.StreamEvent{Type: "done", Reason: "stop", Message: msg})
				stream.Resolve(msg, nil)
				stream.Close()
			}()
			return stream, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var echoTool, spawnTool agent.AgentTool
	for _, tool := range session.GetAgent().GetState().Tools {
		switch tool.Name() {
		case "echo":
			echoTool = tool
		case "spawn_subagent":
			spawnTool = tool
		}
	}
	if echoTool == nil || spawnTool == nil {
		t.Fatalf("expected echo and spawn_subagent, got %v", session.GetActiveToolNames())
	}

	if _, err := echoTool.Execute(context.Background(), "echo-bridge-1", map[string]any{"value": "ok"}, nil); err != nil {
		t.Fatal(err)
	}
	session.GetAgent().AppendMessage(types.UserMessage{Role: "user", Content: "one"})
	session.GetAgent().AppendMessage(types.AssistantMessage{Role: "assistant", Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "two"}}})
	session.GetAgent().AppendMessage(types.UserMessage{Role: "user", Content: "three"})
	session.GetAgent().AppendMessage(types.AssistantMessage{Role: "assistant", Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "four"}}})
	session.GetAgent().AppendMessage(types.UserMessage{Role: "user", Content: "five"})
	if err := session.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := spawnTool.Execute(context.Background(), "spawn-bridge-1", map[string]any{"name": "reviewer", "task": "check code"}, nil); err != nil {
		t.Fatal(err)
	}

	checkBridge := func(rel string, want string) {
		entries, err := os.ReadDir(filepath.Join(agentDir, rel))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) == 0 {
			t.Fatalf("expected bridge events in %s", rel)
		}
		for _, entry := range entries {
			data, err := os.ReadFile(filepath.Join(agentDir, rel, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), want) {
				return
			}
		}
		t.Fatalf("expected %s bridge events to contain %q", rel, want)
	}
	checkBridge("bridge/tool-use", `"tool":"spawn_subagent"`)
	checkBridge("bridge/compact", `"event":"post_compact"`)
	checkBridge("bridge/subagent", `"event":"subagent_stop"`)
}

func TestHarnessConfigDispatchesHostActions(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")
	marker := filepath.Join(agentDir, "action-marker.txt")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configContent := `{"harness":{"enableActions":true,"actions":{"toolUse":[{"type":"exec","command":"/bin/sh","args":["-c","printf '%s:%s:%s' \"$HARNESS_EVENT_TYPE\" \"$HARNESS_TOOL\" \"$HARNESS_AGENT_DIR\" > action-marker.txt"],"dir":"{{agent_dir}}"}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:         dir,
		AgentDir:    agentDir,
		Model:       newTestModel(),
		CustomTools: []agent.AgentTool{&testEchoTool{}},
		GetAPIKey:   func(provider string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	var echoTool agent.AgentTool
	for _, tool := range session.GetAgent().GetState().Tools {
		if tool.Name() == "echo" {
			echoTool = tool
			break
		}
	}
	if echoTool == nil {
		t.Fatal("expected echo tool")
	}
	if _, err := echoTool.Execute(context.Background(), "echo-action-1", map[string]any{"value": "ok"}, nil); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "post_tool_use:echo:"+agentDir {
		t.Fatalf("expected host action marker, got %q", string(data))
	}

	statusPath := filepath.Join(session.RuntimePaths().RuntimeDir, "actions", "tool_use", "latest.json")
	statusData, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(statusData), `"status": "ok"`) {
		t.Fatalf("expected successful action status, got %q", string(statusData))
	}
}

func TestHarnessConfigDoesNotDispatchActionsWhenExplicitlyDisabled(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")
	marker := filepath.Join(agentDir, "action-disabled.txt")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configContent := `{"harness":{"enableActions":false,"actions":{"toolUse":[{"type":"exec","command":"/bin/sh","args":["-c","printf 'ran' > action-disabled.txt"],"dir":"{{agent_dir}}"}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:         dir,
		AgentDir:    agentDir,
		Model:       newTestModel(),
		CustomTools: []agent.AgentTool{&testEchoTool{}},
		GetAPIKey:   func(provider string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	var echoTool agent.AgentTool
	for _, tool := range session.GetAgent().GetState().Tools {
		if tool.Name() == "echo" {
			echoTool = tool
			break
		}
	}
	if echoTool == nil {
		t.Fatal("expected echo tool")
	}
	if _, err := echoTool.Execute(context.Background(), "echo-action-disabled-1", map[string]any{"value": "ok"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("expected disabled actions not to run, stat err=%v", err)
	}
}

func TestHarnessConfigActionFailureIsRecorded(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configContent := `{"harness":{"enableActions":true,"actions":{"toolUse":[{"type":"exec","command":"/bin/sh","args":["-c","exit 7"]}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:         dir,
		AgentDir:    agentDir,
		Model:       newTestModel(),
		CustomTools: []agent.AgentTool{&testEchoTool{}},
		GetAPIKey:   func(provider string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	var echoTool agent.AgentTool
	for _, tool := range session.GetAgent().GetState().Tools {
		if tool.Name() == "echo" {
			echoTool = tool
			break
		}
	}
	if echoTool == nil {
		t.Fatal("expected echo tool")
	}
	if _, err := echoTool.Execute(context.Background(), "echo-action-fail-1", map[string]any{"value": "ok"}, nil); err != nil {
		t.Fatal(err)
	}

	statusPath := filepath.Join(session.RuntimePaths().RuntimeDir, "actions", "tool_use", "latest.json")
	statusData, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(statusData), `"status": "error"`) || !strings.Contains(string(statusData), `"exit status 7"`) {
		t.Fatalf("expected failing action status, got %q", string(statusData))
	}
}

func TestHarnessConfigActionOutputIsCaptured(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configContent := `{"harness":{"enableActions":true,"actions":{"toolUse":[{"type":"exec","command":"/bin/sh","args":["-c","printf 'out'; printf 'err' >&2"]}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:         dir,
		AgentDir:    agentDir,
		Model:       newTestModel(),
		CustomTools: []agent.AgentTool{&testEchoTool{}},
		GetAPIKey:   func(provider string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	var echoTool agent.AgentTool
	for _, tool := range session.GetAgent().GetState().Tools {
		if tool.Name() == "echo" {
			echoTool = tool
			break
		}
	}
	if echoTool == nil {
		t.Fatal("expected echo tool")
	}
	if _, err := echoTool.Execute(context.Background(), "echo-action-output-1", map[string]any{"value": "ok"}, nil); err != nil {
		t.Fatal(err)
	}

	statusPath := filepath.Join(session.RuntimePaths().RuntimeDir, "actions", "tool_use", "latest.json")
	statusData, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(statusData), `"stdout": "out"`) || !strings.Contains(string(statusData), `"stderr": "err"`) || !strings.Contains(string(statusData), `"output": "outerr"`) {
		t.Fatalf("expected split captured output in action status, got %q", string(statusData))
	}
}

func TestHarnessConfigActionTimeoutIsRecorded(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configContent := `{"harness":{"enableActions":true,"actions":{"toolUse":[{"type":"exec","command":"/bin/sh","args":["-c","sleep 1"],"timeoutMs":10}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:         dir,
		AgentDir:    agentDir,
		Model:       newTestModel(),
		CustomTools: []agent.AgentTool{&testEchoTool{}},
		GetAPIKey:   func(provider string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	var echoTool agent.AgentTool
	for _, tool := range session.GetAgent().GetState().Tools {
		if tool.Name() == "echo" {
			echoTool = tool
			break
		}
	}
	if echoTool == nil {
		t.Fatal("expected echo tool")
	}
	if _, err := echoTool.Execute(context.Background(), "echo-action-timeout-1", map[string]any{"value": "ok"}, nil); err != nil {
		t.Fatal(err)
	}

	statusPath := filepath.Join(session.RuntimePaths().RuntimeDir, "actions", "tool_use", "latest.json")
	statusData, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(statusData), `"timed_out": true`) {
		t.Fatalf("expected timeout marker in action status, got %q", string(statusData))
	}
}

func TestHarnessConfigActionRetriesUntilSuccess(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")
	marker := filepath.Join(agentDir, "retry-marker.txt")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configContent := `{"harness":{"enableActions":true,"actions":{"toolUse":[{"type":"exec","command":"/bin/sh","args":["-c","if [ ! -f retry-marker.txt ]; then printf 'first' > retry-marker.txt; exit 9; fi; printf 'second'"],"dir":"{{agent_dir}}","retry":{"maxAttempts":2,"delayMs":1}}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:         dir,
		AgentDir:    agentDir,
		Model:       newTestModel(),
		CustomTools: []agent.AgentTool{&testEchoTool{}},
		GetAPIKey:   func(provider string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	var echoTool agent.AgentTool
	for _, tool := range session.GetAgent().GetState().Tools {
		if tool.Name() == "echo" {
			echoTool = tool
			break
		}
	}
	if echoTool == nil {
		t.Fatal("expected echo tool")
	}
	if _, err := echoTool.Execute(context.Background(), "echo-action-retry-1", map[string]any{"value": "ok"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("expected retry marker, got %v", err)
	}

	statusPath := filepath.Join(session.RuntimePaths().RuntimeDir, "actions", "tool_use", "latest.json")
	statusData, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	status := string(statusData)
	if !strings.Contains(status, `"status": "ok"`) || !strings.Contains(status, `"attempts": 2`) || !strings.Contains(status, `"stdout": "second"`) {
		t.Fatalf("expected retry success status, got %q", status)
	}
}

func TestHarnessConfigActionStopOnFailureSkipsRemainingActions(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")
	skippedMarker := filepath.Join(agentDir, "should-not-exist.txt")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configContent := `{"harness":{"enableActions":true,"actions":{"toolUse":[{"type":"exec","command":"/bin/sh","args":["-c","exit 6"],"onFailure":"stop"},{"type":"exec","command":"/bin/sh","args":["-c","printf 'ran' > should-not-exist.txt"],"dir":"{{agent_dir}}"}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:         dir,
		AgentDir:    agentDir,
		Model:       newTestModel(),
		CustomTools: []agent.AgentTool{&testEchoTool{}},
		GetAPIKey:   func(provider string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	var echoTool agent.AgentTool
	for _, tool := range session.GetAgent().GetState().Tools {
		if tool.Name() == "echo" {
			echoTool = tool
			break
		}
	}
	if echoTool == nil {
		t.Fatal("expected echo tool")
	}
	if _, err := echoTool.Execute(context.Background(), "echo-action-stop-1", map[string]any{"value": "ok"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(skippedMarker); !os.IsNotExist(err) {
		t.Fatalf("expected second action to be skipped, stat err=%v", err)
	}

	statusPath := filepath.Join(session.RuntimePaths().RuntimeDir, "actions", "tool_use", "latest.json")
	statusData, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(statusData), `"on_failure": "stop"`) || !strings.Contains(string(statusData), `"status": "error"`) {
		t.Fatalf("expected stop-on-failure status, got %q", string(statusData))
	}
}

func TestHarnessRuntimeIndexUpdates(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configContent := `{"harness":{"artifactFiles":{"toolUse":"artifacts/tool-use-latest.json"}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}
	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:         dir,
		AgentDir:    agentDir,
		Model:       newTestModel(),
		CustomTools: []agent.AgentTool{&testEchoTool{}},
		GetAPIKey:   func(provider string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	var echoTool agent.AgentTool
	for _, tool := range session.GetAgent().GetState().Tools {
		if tool.Name() == "echo" {
			echoTool = tool
			break
		}
	}
	if echoTool == nil {
		t.Fatal("expected echo tool")
	}
	if _, err := echoTool.Execute(context.Background(), "echo-index-1", map[string]any{"value": "ok"}, nil); err != nil {
		t.Fatal(err)
	}

	indexPath := session.RuntimePaths().RuntimeIndexFile
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"tool_use"`) || !strings.Contains(string(data), `"post_tool_use"`) {
		t.Fatalf("expected runtime index to contain tool_use last event, got %q", string(data))
	}
	if !strings.Contains(string(data), `"runtime_index_file"`) {
		t.Fatalf("expected runtime paths in index, got %q", string(data))
	}
}

func TestPromptToolHarnessArtifactIntegration(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configContent := `{"harness":{"artifactFiles":{"toolUse":"artifacts/tool-use-latest.json"}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}

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
					Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: "done"}},
					Timestamp:  time.Now().UnixMilli(),
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

	artifactPath := filepath.Join(agentDir, "artifacts", "tool-use-latest.json")
	data, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"tool": "echo"`) {
		t.Fatalf("expected harness artifact from prompt-driven tool execution, got %q", string(data))
	}
	if text := session.GetLastAssistantText(); text != "done" {
		t.Fatalf("expected final assistant text to remain intact, got %q", text)
	}
}

func TestHandleToolExecutionEndQueuesNestedContextForDeeperPath(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "repo")
	deepDir := filepath.Join(cwd, "pkg", "feature")
	agentDir := filepath.Join(root, ".coding_agent")

	for _, dir := range []string{deepDir, agentDir, filepath.Join(cwd, ".git")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("root context"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deepDir, "AGENTS.md"), []byte("deep context"), 0o600); err != nil {
		t.Fatal(err)
	}
	targetFile := filepath.Join(deepDir, "file.go")
	if err := os.WriteFile(targetFile, []byte("package feature\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:       cwd,
		AgentDir:  agentDir,
		Model:     newTestModel(),
		GetAPIKey: func(provider string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	if session.agent.QueuedMessageCount() != 0 {
		t.Fatalf("expected no queued steering messages initially, got %d", session.agent.QueuedMessageCount())
	}

	session.handleToolExecutionEnd(agent.AgentEvent{
		Type:     agent.EventTypeToolExecutionEnd,
		ToolName: "read",
		Args:     map[string]any{"path": targetFile},
		Result: agent.AgentToolResult{
			Details: map[string]any{"path": targetFile},
		},
	})

	if session.agent.QueuedMessageCount() != 1 {
		t.Fatalf("expected one queued steering message, got %d", session.agent.QueuedMessageCount())
	}

	session.handleToolExecutionEnd(agent.AgentEvent{
		Type:     agent.EventTypeToolExecutionEnd,
		ToolName: "read",
		Args:     map[string]any{"path": targetFile},
		Result: agent.AgentToolResult{
			Details: map[string]any{"path": targetFile},
		},
	})

	if session.agent.QueuedMessageCount() != 1 {
		t.Fatalf("expected nested context to dedupe, got %d queued messages", session.agent.QueuedMessageCount())
	}
}

func TestTransientNestedContextMessagesArePrunedAndNotPersisted(t *testing.T) {
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

	transient := (&CustomMessage{
		Source: nestedContextSource,
		Text:   "Additional path-specific instructions became relevant after accessing:\n- " + filepath.Join(dir, "nested", "file.go"),
	}).ToLlmMessage()
	normal := types.UserMessage{Role: "user", Content: "regular user message"}

	session.agent.AppendMessage(transient)
	session.handleMessageEnd(transient)
	session.agent.AppendMessage(normal)
	session.handleMessageEnd(normal)
	session.pruneTransientContextMessages()

	msgs := session.agent.GetState().Messages
	if len(msgs) != 1 {
		t.Fatalf("expected only non-transient message to remain, got %d", len(msgs))
	}
	if _, ok := msgs[0].(types.UserMessage); !ok {
		t.Fatalf("expected remaining message to be user message, got %T", msgs[0])
	}

	data, err := os.ReadFile(session.messagesFilePath())
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, nestedContextSource) {
		t.Fatalf("expected transient nested context not to be persisted, got:\n%s", text)
	}
	if !strings.Contains(text, "regular user message") {
		t.Fatalf("expected regular message to be persisted, got:\n%s", text)
	}
}

func findSpanByName(spans tracetest.SpanStubs, name string) (tracetest.SpanStub, bool) {
	for _, span := range spans {
		if span.Name == name {
			return span, true
		}
	}
	return tracetest.SpanStub{}, false
}

func findSpansByName(spans tracetest.SpanStubs, name string) tracetest.SpanStubs {
	var out tracetest.SpanStubs
	for _, span := range spans {
		if span.Name == name {
			out = append(out, span)
		}
	}
	return out
}

func spanStringAttr(attrs []attribute.KeyValue, key string) string {
	for _, attr := range attrs {
		if string(attr.Key) == key {
			return attr.Value.AsString()
		}
	}
	return ""
}

func spanIntAttr(attrs []attribute.KeyValue, key string) int {
	for _, attr := range attrs {
		if string(attr.Key) == key {
			return int(attr.Value.AsInt64())
		}
	}
	return 0
}
