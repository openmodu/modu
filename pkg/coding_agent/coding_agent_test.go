package coding_agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/coding_agent/foundation/config"
	"github.com/openmodu/modu/pkg/coding_agent/plugins/extension"
	subagentext "github.com/openmodu/modu/pkg/coding_agent/plugins/extension/subagent"
	"github.com/openmodu/modu/pkg/coding_agent/plugins/subagent"
	"github.com/openmodu/modu/pkg/coding_agent/services/memory"
	sessionpkg "github.com/openmodu/modu/pkg/coding_agent/services/session"
	"github.com/openmodu/modu/pkg/coding_agent/tools"
	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/skills"
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

type namedTestTool string

func (t namedTestTool) Name() string        { return string(t) }
func (t namedTestTool) Label() string       { return string(t) }
func (t namedTestTool) Description() string { return string(t) }
func (t namedTestTool) Parameters() any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t namedTestTool) Execute(context.Context, string, map[string]any, agent.ToolUpdateCallback) (agent.ToolResult, error) {
	return agent.ToolResult{}, nil
}

type testToolProvider struct {
	ctx       agent.ToolContext
	rebindCwd string
}

func (p *testToolProvider) Tools(ctx agent.ToolContext) []agent.Tool {
	p.ctx = ctx
	out := []agent.Tool{namedTestTool("provider_tool")}
	out = append(out, ctx.ExtraTools...)
	return out
}

func (p *testToolProvider) Rebind(tool agent.Tool, ctx agent.ToolContext) (agent.Tool, bool) {
	p.rebindCwd = ctx.Cwd
	if tool.Name() == "provider_tool" {
		return namedTestTool("provider_tool_rebound"), true
	}
	return nil, false
}
func (t *testEchoTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate agent.ToolUpdateCallback) (agent.ToolResult, error) {
	value, _ := args["value"].(string)
	return agent.ToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "echoed: " + value}},
	}, nil
}

type testCwdTool struct {
	cwd string
}

func (t *testCwdTool) Name() string        { return "echo" }
func (t *testCwdTool) Label() string       { return "Cwd" }
func (t *testCwdTool) Description() string { return "Report cwd " + t.cwd }
func (t *testCwdTool) Parameters() any     { return map[string]any{"type": "object"} }
func (t *testCwdTool) Parallel() bool      { return true }
func (t *testCwdTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate agent.ToolUpdateCallback) (agent.ToolResult, error) {
	return agent.ToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "tool-cwd:" + t.cwd}},
	}, nil
}
func (t *testCwdTool) WithCwd(cwd string) agent.Tool {
	return &testCwdTool{cwd: cwd}
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
	for _, name := range []string{"grep", "find", "ls"} {
		if containsTool(toolNames, name) {
			t.Fatalf("expected search/list tool %s to be opt-in, got default tools %v", name, toolNames)
		}
	}

	// Check config
	cfg := session.GetConfig()
	if cfg == nil {
		t.Fatal("config should not be nil")
	}
}

func TestNewCodingSessionUsesToolProvider(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")
	provider := &testToolProvider{}
	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:          dir,
		AgentDir:     agentDir,
		Model:        newTestModel(),
		CustomTools:  []agent.Tool{namedTestTool("custom_tool")},
		ToolProvider: provider,
		GetAPIKey:    func(provider string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	names := session.GetActiveToolNames()
	if !containsTool(names, "provider_tool_rebound") || !containsTool(names, "custom_tool") {
		t.Fatalf("expected provider and custom tools, got %v", names)
	}
	if containsTool(names, "read") {
		t.Fatalf("expected custom provider to own default tool construction, got %v", names)
	}
	if provider.ctx.Cwd != dir {
		t.Fatalf("expected provider cwd %q, got %q", dir, provider.ctx.Cwd)
	}
	if !provider.ctx.FeatureEnabled(tools.FeatureMemory) || !provider.ctx.FeatureEnabled(tools.FeatureTodo) {
		t.Fatalf("expected feature flags in provider context, got %#v", provider.ctx.Features)
	}

	session.refreshToolsForCwd(filepath.Join(dir, "child"))
	names = session.GetActiveToolNames()
	if !containsTool(names, "provider_tool_rebound") {
		t.Fatalf("expected provider rebind to update tool, got %v", names)
	}
	if provider.rebindCwd != filepath.Join(dir, "child") {
		t.Fatalf("expected rebind cwd to be delegated, got %q", provider.rebindCwd)
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

	var todoTool agent.Tool
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
			reply := "bg-result: " + userText
			if strings.Contains(userText, "Continue this delegated subagent task.") {
				reply = fmt.Sprintf("resume-messages:%d", len(llmCtx.Messages))
			}
			msg := &types.AssistantMessage{
				Role:       "assistant",
				ProviderID: model.ProviderID,
				Model:      model.ID,
				StopReason: "stop",
				Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: reply}},
				Timestamp:  time.Now().UnixMilli(),
			}
			stream.Push(types.StreamEvent{Type: "done", Reason: "stop", Message: msg})
			stream.Resolve(msg, nil)
			stream.Close()
		}()
		return stream, nil
	}

	// Enable task_output explicitly for this test (it's off by default).
	cfg := config.Default()
	cfg.Features.TaskOutputTool = config.Ptr(true)
	settingsPath := filepath.Join(agentDir, "settings.json")
	if err := config.Save(cfg, settingsPath); err != nil {
		t.Fatal(err)
	}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:       dir,
		AgentDir:  agentDir,
		Model:     model,
		GetAPIKey: func(provider string) (string, error) { return "", nil },
		StreamFn:  streamFn,
		Extensions: []extension.Extension{
			subagentext.New(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var spawnTool, outputTool, subagentTool agent.Tool
	for _, tool := range session.GetAgent().GetState().Tools {
		switch tool.Name() {
		case "spawn_subagent":
			spawnTool = tool
		case "task_output":
			outputTool = tool
		case "subagent":
			subagentTool = tool
		}
	}
	if spawnTool == nil || outputTool == nil || subagentTool == nil {
		t.Fatalf("expected spawn_subagent, subagent, and task_output tools from extension, got %v", session.GetActiveToolNames())
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
	if _, err := os.Stat(session.RuntimePaths().BackgroundTasksFile); err != nil {
		t.Fatalf("expected persisted background task file: %v", err)
	}
	tasks := session.GetBackgroundTasks()
	if len(tasks) != 1 || tasks[0].Agent != "helper" || tasks[0].Task != "hello" {
		t.Fatalf("expected persisted subagent metadata, got %#v", tasks)
	}
	if tasks[0].RunDir == "" || tasks[0].StatusFile == "" || tasks[0].SessionFile == "" {
		t.Fatalf("expected async run paths, got %#v", tasks[0])
	}
	if _, err := os.Stat(tasks[0].StatusFile); err != nil {
		t.Fatalf("expected async run status file: %v", err)
	}
	if _, err := os.Stat(tasks[0].SessionFile); err != nil {
		t.Fatalf("expected child session file: %v", err)
	}
	if err := os.Remove(session.RuntimePaths().BackgroundTasksFile); err != nil {
		t.Fatalf("remove background task list to verify run status recovery: %v", err)
	}

	asyncOut := filepath.Join(dir, "async-output.md")
	asyncRes, err := subagentTool.Execute(context.Background(), "bg-output-1", map[string]any{
		"agent":      "helper",
		"task":       "async output",
		"async":      true,
		"output":     asyncOut,
		"outputMode": "file-only",
	}, nil)
	if err != nil || asyncRes.IsError {
		t.Fatalf("async output subagent failed: err=%v res=%+v", err, asyncRes)
	}
	asyncDetails, _ := asyncRes.Details.(map[string]string)
	asyncTaskID := asyncDetails["task_id"]
	if asyncTaskID == "" {
		t.Fatalf("expected async output task id, got %#v", asyncRes.Details)
	}
	for i := 0; i < 20; i++ {
		res, err := outputTool.Execute(context.Background(), "out-async", map[string]any{
			"task_id": asyncTaskID,
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		output = extractTextBlocks(res.Content)
		if strings.Contains(output, "Output saved to: "+asyncOut) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(output, "Output saved to: "+asyncOut) {
		t.Fatalf("expected async output task to return saved-file reference, got %q", output)
	}
	data, err := os.ReadFile(asyncOut)
	if err != nil {
		t.Fatalf("expected async output file: %v", err)
	}
	if string(data) != "bg-result: async output" {
		t.Fatalf("unexpected async output file content: %q", string(data))
	}

	session2, err := NewCodingSession(CodingSessionOptions{
		Cwd:       dir,
		AgentDir:  agentDir,
		Model:     model,
		GetAPIKey: func(provider string) (string, error) { return "", nil },
		StreamFn:  streamFn,
		Extensions: []extension.Extension{
			subagentext.New(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var outputTool2, subagentTool2 agent.Tool
	for _, tool := range session2.GetAgent().GetState().Tools {
		switch tool.Name() {
		case "task_output":
			outputTool2 = tool
		case "subagent":
			subagentTool2 = tool
		}
	}
	if outputTool2 == nil || subagentTool2 == nil {
		t.Fatalf("expected task_output and subagent in resumed session, got %v", session2.GetActiveToolNames())
	}
	res, err := outputTool2.Execute(context.Background(), "out-2", map[string]any{
		"task_id": taskID,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := extractTextBlocks(res.Content); !strings.Contains(got, "bg-result: hello") {
		t.Fatalf("expected persisted background output, got %q", got)
	}

	resumeRes, err := subagentTool2.Execute(context.Background(), "resume-1", map[string]any{
		"action":  "resume",
		"id":      taskID,
		"message": "continue with prior context",
	}, nil)
	if err != nil || resumeRes.IsError {
		t.Fatalf("resume failed: err=%v res=%+v", err, resumeRes)
	}
	resumeDetails, _ := resumeRes.Details.(map[string]string)
	resumeTaskID := resumeDetails["task_id"]
	if resumeTaskID == "" {
		t.Fatalf("expected resume task id, got %#v", resumeRes.Details)
	}
	for i := 0; i < 20; i++ {
		res, err = outputTool2.Execute(context.Background(), "out-3", map[string]any{
			"task_id": resumeTaskID,
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		output = extractTextBlocks(res.Content)
		if strings.Contains(output, "resume-messages:3") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(output, "resume-messages:3") {
		t.Fatalf("expected resume to include child session context, got %q", output)
	}
}

func TestSubagentContextForkSeedsChildMessages(t *testing.T) {
	dir := t.TempDir()
	agentDir := t.TempDir()
	agentsDir := filepath.Join(agentDir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "helper.md"), []byte(`---
name: helper
description: counts messages
---
Return the child message count.
`), 0o644); err != nil {
		t.Fatal(err)
	}
	model := newTestModel()
	streamFn := func(ctx context.Context, _ *types.Model, llmCtx *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		stream := types.NewEventStream()
		go func() {
			defer stream.Close()
			msg := &types.AssistantMessage{
				Role:       "assistant",
				ProviderID: model.ProviderID,
				Model:      model.ID,
				StopReason: "stop",
				Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: fmt.Sprintf("messages:%d", len(llmCtx.Messages))}},
				Timestamp:  time.Now().UnixMilli(),
			}
			stream.Push(types.StreamEvent{Type: "done", Reason: "stop", Message: msg})
			stream.Resolve(msg, nil)
		}()
		return stream, nil
	}
	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:       dir,
		AgentDir:  agentDir,
		Model:     model,
		GetAPIKey: func(provider string) (string, error) { return "", nil },
		StreamFn:  streamFn,
		Extensions: []extension.Extension{
			subagentext.New(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	session.agent.ReplaceMessages([]agent.AgentMessage{
		types.UserMessage{Role: "user", Content: "parent question", Timestamp: time.Now().UnixMilli()},
		&types.AssistantMessage{
			Role:       "assistant",
			ProviderID: model.ProviderID,
			Model:      model.ID,
			StopReason: "stop",
			Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: "parent answer"}},
			Timestamp:  time.Now().UnixMilli(),
		},
	})

	var subagentTool agent.Tool
	for _, tool := range session.GetAgent().GetState().Tools {
		if tool.Name() == "subagent" {
			subagentTool = tool
			break
		}
	}
	if subagentTool == nil {
		t.Fatalf("expected subagent tool, got %v", session.GetActiveToolNames())
	}

	res, err := subagentTool.Execute(context.Background(), "fresh", map[string]any{
		"agent": "helper",
		"task":  "fresh",
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("fresh subagent failed: err=%v text=%q res=%+v", err, extractTextBlocks(res.Content), res)
	}
	if got := extractTextBlocks(res.Content); !strings.Contains(got, "messages:1") {
		t.Fatalf("fresh context should only include task message, got %q", got)
	}

	res, err = subagentTool.Execute(context.Background(), "fork", map[string]any{
		"agent":   "helper",
		"task":    "fork",
		"context": "fork",
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("fork subagent failed: err=%v text=%q res=%+v", err, extractTextBlocks(res.Content), res)
	}
	if got := extractTextBlocks(res.Content); !strings.Contains(got, "messages:3") {
		t.Fatalf("fork context should include parent messages plus task, got %q", got)
	}
}

func TestSubagentCwdBindsChildWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	childDir := filepath.Join(root, "nested")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatal(err)
	}
	agentDir := t.TempDir()
	agentsDir := filepath.Join(agentDir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "helper.md"), []byte(`---
name: helper
description: reports cwd
tools: echo
---
System prompt for helper.
`), 0o644); err != nil {
		t.Fatal(err)
	}
	model := newTestModel()
	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:         root,
		AgentDir:    agentDir,
		Model:       model,
		CustomTools: []agent.Tool{&testCwdTool{}},
		GetAPIKey:   func(provider string) (string, error) { return "", nil },
		Extensions: []extension.Extension{
			subagentext.New(),
		},
		StreamFn: func(ctx context.Context, _ *types.Model, llmCtx *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
			stream := types.NewEventStream()
			go func() {
				defer stream.Close()
				text := llmCtx.SystemPrompt
				if len(llmCtx.Tools) > 0 {
					text += "\n" + llmCtx.Tools[0].Description
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
			}()
			return stream, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var subagentTool agent.Tool
	for _, tool := range session.GetAgent().GetState().Tools {
		if tool.Name() == "subagent" {
			subagentTool = tool
			break
		}
	}
	if subagentTool == nil {
		t.Fatalf("expected subagent tool, got %v", session.GetActiveToolNames())
	}

	res, err := subagentTool.Execute(context.Background(), "cwd", map[string]any{
		"agent": "helper",
		"task":  "report cwd",
		"cwd":   "nested",
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("cwd subagent failed: err=%v text=%q res=%+v", err, extractTextBlocks(res.Content), res)
	}
	got := extractTextBlocks(res.Content)
	if !strings.Contains(got, "Working directory: "+childDir) {
		t.Fatalf("child prompt missing cwd %q:\n%s", childDir, got)
	}
	if !strings.Contains(got, "Report cwd "+childDir) {
		t.Fatalf("child tool was not rebound to cwd %q:\n%s", childDir, got)
	}
}

// TestBackgroundTaskManagerCreateWithMetadataInDirRedirects covers the
// host-level half of K.sessionDir: when a caller supplies runDirParent,
// the manager places the per-task RunDir / StatusFile / SessionFile under
// that path instead of the manager's default runRoot.
func TestBackgroundTaskManagerCreateWithMetadataInDirRedirects(t *testing.T) {
	defaultRoot := t.TempDir()
	overrideRoot := t.TempDir()
	mgr := newBackgroundTaskManager()
	if err := mgr.SetStorePath(filepath.Join(defaultRoot, "background_tasks.json")); err != nil {
		t.Fatal(err)
	}

	defaultID := mgr.CreateWithMetadata("subagent", "default-summary", "agentA", "task1", "", "")
	overrideID := mgr.CreateWithMetadataInDir("subagent", "override-summary", "agentB", "task2", "", "", overrideRoot)

	defaultTask, ok := mgr.Get(defaultID)
	if !ok {
		t.Fatalf("default task %s not found", defaultID)
	}
	if !strings.HasPrefix(defaultTask.RunDir, defaultRoot) {
		t.Errorf("default task RunDir should land under %s, got %q", defaultRoot, defaultTask.RunDir)
	}

	overrideTask, ok := mgr.Get(overrideID)
	if !ok {
		t.Fatalf("override task %s not found", overrideID)
	}
	if !strings.HasPrefix(overrideTask.RunDir, overrideRoot) {
		t.Errorf("override task RunDir should land under %s, got %q", overrideRoot, overrideTask.RunDir)
	}
	if !strings.HasPrefix(overrideTask.SessionFile, overrideRoot) {
		t.Errorf("override task SessionFile should land under %s, got %q", overrideRoot, overrideTask.SessionFile)
	}
	if !strings.HasPrefix(overrideTask.StatusFile, overrideRoot) {
		t.Errorf("override task StatusFile should land under %s, got %q", overrideRoot, overrideTask.StatusFile)
	}
}

func TestPlanModeTools(t *testing.T) {
	session := newTestSession(t, newTestModel())
	var enterTool, exitTool agent.Tool
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
	if !session.IsPlanMode() {
		t.Fatal("expected plan mode enabled")
	}

	_, err = exitTool.Execute(context.Background(), "plan-2", map[string]any{
		"plan":  "do work",
		"steps": []any{"step one", "step two"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if session.IsPlanMode() {
		t.Fatal("expected plan mode disabled")
	}
	todos := session.GetTodos()
	if len(todos) != 2 {
		t.Fatalf("expected approved steps to become 2 todos, got %v", todos)
	}
	if todos[0].Content != "step one" || todos[0].Status != "pending" {
		t.Fatalf("unexpected first todo: %+v", todos[0])
	}
}

// TestPlanGateAutoAllowed verifies the agent-level approval gate lets
func TestPlanModeApprovalBlocksMutatingToolsBeforeCallback(t *testing.T) {
	session := newTestSession(t, newTestModel())
	session.EnterPlanMode()
	called := false
	session.SetToolApprovalCallback(func(name, id string, args map[string]any) (agent.ToolApprovalDecision, error) {
		called = true
		return agent.ToolApprovalAllow, nil
	})

	for _, tool := range []string{"write", "edit", "bash"} {
		decision, err := session.approvalManager.Approve(tool, "call-"+tool, nil)
		if decision != agent.ToolApprovalDeny {
			t.Fatalf("%s should be denied in plan mode, got %v", tool, decision)
		}
		if err == nil || !strings.Contains(err.Error(), "plan mode is active") {
			t.Fatalf("%s should return plan mode denial reason, got %v", tool, err)
		}
	}
	if called {
		t.Fatal("plan mode block should happen before interactive approval callback")
	}
	if decision, err := session.approvalManager.Approve("exit_plan_mode", "plan", nil); decision != agent.ToolApprovalAllow || err != nil {
		t.Fatalf("exit_plan_mode should still be auto-allowed, got decision=%v err=%v", decision, err)
	}
}

// TestSubmitPlanApprove covers the approve path: plan mode exits and the
// steps become the todo list.
func TestSubmitPlanApprove(t *testing.T) {
	session := newTestSession(t, newTestModel())
	session.EnterPlanMode()
	session.SetPlanDecisionCallback(func(plan string, steps []string) string { return "approve" })

	adapter := session.plan
	msg := adapter.SubmitPlan(context.Background(), "do work", []string{"a", "b"})

	if session.IsPlanMode() {
		t.Fatal("expected plan mode off after approve")
	}
	if todos := session.GetTodos(); len(todos) != 2 || todos[0].Content != "a" {
		t.Fatalf("expected 2 todos from steps, got %v", todos)
	}
	status := session.PlanStatus()
	if status.Active || !status.PlanExists || status.RevisionCount != 1 || status.TodoTotal != 2 || status.TodoPending != 2 {
		t.Fatalf("unexpected approved plan status: %#v", status)
	}
	revisions := session.ListPlanRevisions()
	if len(revisions) != 1 || revisions[0].Path == "" {
		t.Fatalf("expected one plan revision, got %#v", revisions)
	}
	if !strings.Contains(msg, "approved") {
		t.Fatalf("expected approval message, got %q", msg)
	}
}

func TestClearPlanRemovesPlanArtifactAndTodos(t *testing.T) {
	session := newTestSession(t, newTestModel())
	session.EnterPlanMode()
	session.ExitPlanMode("clear me", []string{"a"})
	if status := session.PlanStatus(); !status.PlanExists || status.TodoTotal != 1 {
		t.Fatalf("expected plan artifact and todo before clear, got %#v", status)
	}

	if err := session.ClearPlan(); err != nil {
		t.Fatal(err)
	}
	status := session.PlanStatus()
	if status.PlanExists || status.TodoTotal != 0 {
		t.Fatalf("expected cleared plan status, got %#v", status)
	}
}

// TestSubmitPlanAutoAccept covers approve_auto: edits are auto-allowed for the
// rest of the session.
func TestSubmitPlanAutoAccept(t *testing.T) {
	session := newTestSession(t, newTestModel())
	session.EnterPlanMode()
	session.SetPlanDecisionCallback(func(plan string, steps []string) string { return "approve_auto" })

	session.plan.SubmitPlan(context.Background(), "p", []string{"x"})

	for _, tool := range []string{"write", "edit", "bash"} {
		if d, _ := session.approvalManager.Approve(tool, "t", nil); d != agent.ToolApprovalAllow {
			t.Fatalf("%s should be auto-allowed after approve_auto, got %v", tool, d)
		}
	}
}

// TestSubmitPlanReject covers rejection with feedback: plan mode stays on, no
// todos are created, and the feedback is relayed to the model.
func TestSubmitPlanReject(t *testing.T) {
	session := newTestSession(t, newTestModel())
	session.EnterPlanMode()
	session.SetPlanDecisionCallback(func(plan string, steps []string) string {
		return "reject:use the existing helper instead"
	})

	msg := session.plan.SubmitPlan(context.Background(), "p", []string{"x"})

	if !session.IsPlanMode() {
		t.Fatal("expected to remain in plan mode after rejection")
	}
	if todos := session.GetTodos(); len(todos) != 0 {
		t.Fatalf("expected no todos after rejection, got %v", todos)
	}
	if !strings.Contains(msg, "use the existing helper instead") || !strings.Contains(msg, "REJECTED") {
		t.Fatalf("expected rejection feedback relayed, got %q", msg)
	}
}

func TestPlanModeBlocksMutatingTools(t *testing.T) {
	session := newTestSession(t, newTestModel())
	session.EnterPlanMode()

	var writeTool, editTool, bashTool agent.Tool
	for _, tool := range session.GetAgent().GetState().Tools {
		switch tool.Name() {
		case "write":
			writeTool = tool
		case "edit":
			editTool = tool
		case "bash":
			bashTool = tool
		}
	}
	if writeTool == nil || editTool == nil || bashTool == nil {
		t.Fatalf("expected write, edit, and bash tools, got %v", session.GetActiveToolNames())
	}

	writeResult, err := writeTool.Execute(context.Background(), "write-plan", map[string]any{
		"path":    "planned.txt",
		"content": "should not write",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(extractTextBlocks(writeResult.Content), "blocked while plan mode is active") {
		t.Fatalf("expected plan mode block, got %#v", writeResult.Content)
	}
	if _, err := os.Stat(filepath.Join(session.cwd, "planned.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected write tool not to create file, stat err=%v", err)
	}

	path := filepath.Join(session.cwd, "existing.txt")
	if err := os.WriteFile(path, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	editResult, err := editTool.Execute(context.Background(), "edit-plan", map[string]any{
		"path":     "existing.txt",
		"old_text": "before",
		"new_text": "after",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(extractTextBlocks(editResult.Content), "blocked while plan mode is active") {
		t.Fatalf("expected plan mode block, got %#v", editResult.Content)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "before" {
		t.Fatalf("expected edit tool not to mutate file, got %q", data)
	}

	bashResult, err := bashTool.Execute(context.Background(), "bash-plan", map[string]any{
		"command": "cat > bash-created.txt <<'EOF'\ncreated\nEOF",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(extractTextBlocks(bashResult.Content), "blocked while plan mode is active") {
		t.Fatalf("expected plan mode block, got %#v", bashResult.Content)
	}
	if _, err := os.Stat(filepath.Join(session.cwd, "bash-created.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected bash tool not to create file, stat err=%v", err)
	}
}

func TestPlanModeBlocksWriteAfterEnterWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	dir := t.TempDir()
	initGitRepo(t, dir)
	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:       dir,
		AgentDir:  filepath.Join(dir, ".coding_agent"),
		Model:     newTestModel(),
		GetAPIKey: func(provider string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	session.EnterPlanMode()
	worktree, err := session.EnterWorktree()
	if err != nil {
		t.Fatal(err)
	}
	defer session.ExitWorktree()

	var writeTool agent.Tool
	for _, tool := range session.GetAgent().GetState().Tools {
		if tool.Name() == "write" {
			writeTool = tool
			break
		}
	}
	if writeTool == nil {
		t.Fatalf("expected write tool after entering worktree, got %v", session.GetActiveToolNames())
	}
	result, err := writeTool.Execute(context.Background(), "write-worktree-plan", map[string]any{
		"path":    "worktree-plan.txt",
		"content": "should not write",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(extractTextBlocks(result.Content), "blocked while plan mode is active") {
		t.Fatalf("expected plan mode block after worktree refresh, got %#v", result.Content)
	}
	if _, err := os.Stat(filepath.Join(worktree, "worktree-plan.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected write tool not to create file in worktree, stat err=%v", err)
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
	status := session.WorktreeStatus()
	if !status.Active || status.Path != path || status.Cwd != path || status.OriginalCwd != original || !status.Exists {
		t.Fatalf("unexpected active worktree status: %#v", status)
	}
	worktrees := session.ListManagedWorktrees()
	if len(worktrees) != 1 || !worktrees[0].Active || worktrees[0].Path != path || !worktrees[0].Exists {
		t.Fatalf("unexpected managed worktree list: %#v", worktrees)
	}
	stalePath := filepath.Join(agentDir, "worktrees", "wt-stale")
	if err := os.MkdirAll(stalePath, 0o755); err != nil {
		t.Fatal(err)
	}
	removed, err := session.CleanupManagedWorktrees()
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0].Path != stalePath {
		t.Fatalf("expected stale worktree removed, got %#v", removed)
	}
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("expected stale worktree removed, stat err=%v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected active worktree kept: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	diff, err := session.ActiveWorktreeDiff()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff.NameStatus, "README.md") || !strings.Contains(diff.Patch, "changed") {
		t.Fatalf("expected active worktree diff for README.md, got %#v", diff)
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
	status = session.WorktreeStatus()
	if status.Active || status.Path != "" || status.Cwd != original {
		t.Fatalf("unexpected inactive worktree status: %#v", status)
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
	var capturedSystemPrompt string
	streamFn := func(ctx context.Context, _ *types.Model, llmCtx *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		callCount++
		if callCount == 1 {
			capturedSystemPrompt = llmCtx.SystemPrompt
		}
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
		Extensions: []extension.Extension{
			subagentext.New(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var spawnTool agent.Tool
	for _, tool := range session.GetAgent().GetState().Tools {
		if tool.Name() == "spawn_subagent" {
			spawnTool = tool
			break
		}
	}
	if spawnTool == nil {
		t.Fatalf("expected spawn_subagent from extension, got %v", session.GetActiveToolNames())
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
	if !strings.Contains(capturedSystemPrompt, filepath.Join(agentDir, "worktrees")) {
		t.Fatalf("expected system prompt to include worktree cwd, got %q", capturedSystemPrompt)
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
	cfg := config.Default()
	if cfg.ThinkingLevel == "" {
		t.Fatal("thinking level should have default")
	}
	if !cfg.AutoCompaction {
		t.Fatal("auto compaction should be on by default")
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
	cfg, err := config.Load(agentDir, cwd)
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

	cfg, err := config.Load(agentDir, dir)
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

func TestGitRuntimeStateInspect(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	dir := t.TempDir()
	initGitRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scratch.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "add", "README.md")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add failed: %v\n%s", err, string(out))
	}

	session := &CodingSession{}
	state := session.gitRuntimeStateForCwd(dir)
	if state["available"] != true || state["inGitRepository"] != true {
		t.Fatalf("unexpected git state: %#v", state)
	}
	if stats, ok := state["stagedStats"].(map[string]any); !ok || stats["files"].(float64) == 0 {
		t.Fatalf("expected staged stats, got %#v", state["stagedStats"])
	}
	untracked, ok := state["untrackedFiles"].([]any)
	if !ok || len(untracked) != 1 || untracked[0] != "scratch.txt" {
		t.Fatalf("expected scratch.txt as untracked, got %#v", state["untrackedFiles"])
	}
	if last, ok := state["lastCommit"].(map[string]any); !ok || last["hash"] == "" {
		t.Fatalf("expected last commit, got %#v", state["lastCommit"])
	}
}

func TestRuntimeStateGitRefreshCanRunAsync(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	dir := t.TempDir()
	initGitRepo(t, dir)
	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:       dir,
		AgentDir:  filepath.Join(dir, ".coding_agent"),
		Model:     newTestModel(),
		GetAPIKey: func(provider string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.After(2 * time.Second)
	for {
		session.gitCache.mu.RLock()
		refreshing := session.gitCache.refreshing
		session.gitCache.mu.RUnlock()
		if !refreshing {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for startup git refresh")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	session.gitCache.mu.Lock()
	session.gitCache.state = nil
	session.gitCache.cwd = ""
	session.gitCache.refreshing = false
	session.gitCache.refreshingCwd = ""
	session.gitCache.mu.Unlock()

	state := session.RuntimeState()
	if state.Git["available"] != false || state.Git["refreshing"] != false {
		t.Fatalf("expected empty git cache to return non-refreshing placeholder, got %#v", state.Git)
	}

	session.RefreshRuntimeStateAsync()
	deadline = time.After(2 * time.Second)
	for {
		session.gitCache.mu.RLock()
		cached := session.gitCache.state
		cachedCwd := session.gitCache.cwd
		session.gitCache.mu.RUnlock()
		if cachedCwd == dir && cached["available"] == true {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("expected async git refresh to populate cache, got cwd=%q state=%#v", cachedCwd, cached)
		default:
			time.Sleep(10 * time.Millisecond)
		}
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
	api.AddHook(extension.ToolHook{
		Before: func(toolName string, args map[string]any) bool {
			if toolName == "bash" {
				e.blocked = append(e.blocked, toolName)
				return false // block bash tool
			}
			return true
		},
		After: func(toolName string, args map[string]any, result agent.ToolResult) {
			e.afterCalls = append(e.afterCalls, toolName)
		},
	})
	return nil
}

type testRuntimeStateExtension struct{}

func (e *testRuntimeStateExtension) Name() string { return "stateful" }
func (e *testRuntimeStateExtension) Init(extension.ExtensionAPI) error {
	return nil
}
func (e *testRuntimeStateExtension) RuntimeState() any {
	return map[string]any{"status": "ok"}
}

func TestRuntimeStateIncludesExtensionState(t *testing.T) {
	dir := t.TempDir()
	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:        dir,
		AgentDir:   filepath.Join(dir, ".coding_agent"),
		Model:      newTestModel(),
		Extensions: []extension.Extension{&testRuntimeStateExtension{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	states := session.ExtensionRuntimeStates()
	state, ok := states["stateful"].(map[string]any)
	if !ok || state["status"] != "ok" {
		t.Fatalf("extension runtime state missing: %#v", states)
	}
	if got := session.RuntimeStateJSON(); !strings.Contains(got, `"extensions"`) || !strings.Contains(got, `"stateful"`) {
		t.Fatalf("runtime state json missing extension state: %s", got)
	}
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

type testEventExtension struct {
	agentStartCount int
}

func (e *testEventExtension) Name() string { return "test-events" }
func (e *testEventExtension) Init(api extension.ExtensionAPI) error {
	api.On(string(agent.EventTypeAgentStart), func(event agent.Event) {
		e.agentStartCount++
	})
	return nil
}

func TestExtensionEventHandlersReceiveEvents(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")
	model := newTestModel()
	streamFn := func(ctx context.Context, _ *types.Model, _ *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		stream := types.NewEventStream()
		go func() {
			defer stream.Close()
			msg := &types.AssistantMessage{
				Role:       "assistant",
				ProviderID: model.ProviderID,
				Model:      model.ID,
				StopReason: "stop",
				Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: "ok"}},
				Timestamp:  time.Now().UnixMilli(),
			}
			stream.Push(types.StreamEvent{Type: "done", Reason: "stop", Message: msg})
			stream.Resolve(msg, nil)
		}()
		return stream, nil
	}
	ext := &testEventExtension{}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:        dir,
		AgentDir:   agentDir,
		Model:      model,
		Extensions: []extension.Extension{ext},
		GetAPIKey:  func(provider string) (string, error) { return "", nil },
		StreamFn:   streamFn,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Prompt(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if ext.agentStartCount == 0 {
		t.Fatal("expected extension event handler to receive agent_start")
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
	session.ctxMgr.AddUsage(5000)

	session.agent.AppendMessage(types.UserMessage{Role: "user", Content: "msg1"})
	session.agent.AppendMessage(types.UserMessage{Role: "user", Content: "msg2"})
	msgsBefore := len(session.agent.GetState().Messages)

	session.ctxMgr.MaybeAutoCompact(context.Background())

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
	session.ctxMgr.AddUsage(105000)

	msgsBefore := len(session.agent.GetState().Messages)

	session.ctxMgr.MaybeAutoCompact(context.Background())

	msgsAfter := len(session.agent.GetState().Messages)
	if msgsAfter >= msgsBefore {
		t.Fatalf("should compact above threshold: before=%d after=%d", msgsBefore, msgsAfter)
	}

	if session.ctxMgr.Tokens() != 0 {
		t.Fatalf("expected totalTokens reset to 0, got %d", session.ctxMgr.Tokens())
	}
}

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

func TestSetModelByName(t *testing.T) {
	providers.Models["test-provider"] = map[string]*types.Model{
		"model-a": {ID: "model-a", Name: "friendly-a", ProviderID: "test-provider"},
	}
	session := newTestSession(t, newTestModel())

	if err := session.SetModelByName("friendly-a"); err != nil {
		t.Fatalf("SetModelByName: %v", err)
	}
	got := session.GetModel()
	if got.ProviderID != "test-provider" || got.ID != "model-a" {
		t.Fatalf("unexpected model: %#v", got)
	}
}

func TestSetModelClearsConversationContextWhenModelChanges(t *testing.T) {
	session := newTestSession(t, newTestModel())
	session.GetAgent().AppendMessage(types.UserMessage{Role: "user", Content: "old context"})
	if len(session.GetMessages()) == 0 {
		t.Fatal("expected setup message")
	}

	session.SetModel(&types.Model{ID: "next-model", ProviderID: "next-provider"})

	if got := len(session.GetMessages()); got != 0 {
		t.Fatalf("expected model switch to clear conversation context, got %d messages", got)
	}
}

func TestSetModelRefreshesConnectedModelPrompt(t *testing.T) {
	session := newTestSession(t, &types.Model{ID: "old-model", ProviderID: "old-provider"})

	session.SetModel(&types.Model{ID: "next-model", Name: "Next Model", ProviderID: "next-provider"})

	prompt := session.GetAgent().GetState().SystemPrompt
	if !strings.Contains(prompt, "- Connected model: next-provider/next-model") {
		t.Fatalf("expected refreshed connected model in prompt, got:\n%s", prompt)
	}
	if strings.Contains(prompt, "- Connected model: old-provider/old-model") {
		t.Fatalf("expected old connected model to be removed, got:\n%s", prompt)
	}
}

func TestClearConversationClearsInMemoryMessages(t *testing.T) {
	session := newTestSession(t, newTestModel())
	session.GetAgent().AppendMessage(types.UserMessage{Role: "user", Content: "old context"})

	if err := session.ClearConversation(); err != nil {
		t.Fatalf("ClearConversation: %v", err)
	}
	if got := len(session.GetMessages()); got != 0 {
		t.Fatalf("expected clear conversation to clear in-memory messages, got %d", got)
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
	var callIndex atomic.Int64

	streamFn := func(ctx context.Context, _ *types.Model, _ *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		stream := types.NewEventStream()
		go func() {
			if callIndex.Load() == 0 {
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
			callIndex.Add(1)
			stream.Close()
		}()
		return stream, nil
	}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:       dir,
		AgentDir:  agentDir,
		Model:     model,
		Tools:     []agent.Tool{tool},
		GetAPIKey: func(p string) (string, error) { return "", nil },
		StreamFn:  streamFn,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := session.Prompt(context.Background(), "run echo"); err != nil {
		t.Fatal(err)
	}

	var roles []string
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
				roles = append(roles, string(role))
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

func TestPromptSlashSkillPinsSkillForMainAgentTurn(t *testing.T) {
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
	var capturedMessages []agent.AgentMessage
	streamFn := func(ctx context.Context, _ *types.Model, llmCtx *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		capturedMessages = append([]agent.AgentMessage{}, llmCtx.Messages...)
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

	var events []agent.Event
	unsub := session.Subscribe(func(event agent.Event) {
		events = append(events, event)
	})
	defer unsub()

	if err := session.Prompt(context.Background(), "/summarize hello world"); err != nil {
		t.Fatal(err)
	}
	if !messagesContainText(capturedMessages, "explicitly invoked the \"summarize\" skill") {
		t.Fatalf("expected slash skill invocation to inject skill context, got %#v", capturedMessages)
	}
	if !messagesContainText(capturedMessages, "Working directory: "+dir) {
		t.Fatalf("expected slash skill context to include cwd %q, got %#v", dir, capturedMessages)
	}

	got := session.GetLastAssistantText()
	if got != "skill-result: hello world" {
		t.Fatalf("expected explicit skill result, got %q", got)
	}
	if !hasAssistantMessageEnd(events, "skill-result: hello world") {
		t.Fatalf("expected slash skill result to emit assistant message_end, got %#v", events)
	}
	if !hasAgentEnd(events) {
		t.Fatalf("expected slash skill result to emit agent_end, got %#v", events)
	}

	if err := session.Prompt(context.Background(), " \t/summarize spaced task\n"); err != nil {
		t.Fatal(err)
	}
	got = session.GetLastAssistantText()
	if got != "skill-result: spaced task" {
		t.Fatalf("expected trimmed slash skill invocation, got %q", got)
	}
}

func TestPromptTemplateSlashExpandsToUserPrompt(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")
	promptDir := filepath.Join(dir, ".coding_agent", "prompts")
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	template := `---
description: review a target
---
Review this target:
{{input}}`
	if err := os.WriteFile(filepath.Join(promptDir, "review.md"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}

	model := newTestModel()
	var capturedMessages []agent.AgentMessage
	streamFn := func(ctx context.Context, _ *types.Model, llmCtx *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		capturedMessages = append([]agent.AgentMessage{}, llmCtx.Messages...)
		stream := types.NewEventStream()
		go func() {
			defer stream.Close()
			msg := &types.AssistantMessage{
				Role:       "assistant",
				ProviderID: model.ProviderID,
				Model:      model.ID,
				StopReason: "stop",
				Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: "ok"}},
				Timestamp:  time.Now().UnixMilli(),
			}
			stream.Push(types.StreamEvent{Type: "done", Reason: "stop", Message: msg})
			stream.Resolve(msg, nil)
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

	if err := session.Prompt(context.Background(), "/review pkg/coding_agent"); err != nil {
		t.Fatal(err)
	}
	if !messagesContainText(capturedMessages, "Review this target:\npkg/coding_agent") {
		t.Fatalf("expected prompt template expansion in messages, got %#v", capturedMessages)
	}
	if templates := session.GetPromptTemplates(); len(templates) != 1 || templates[0].Name != "review" {
		t.Fatalf("expected review prompt template, got %#v", templates)
	}
}

func TestLocalPackageResourcesExposeSkillsAndPrompts(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")
	pkgDir := filepath.Join(dir, ".coding_agent", "packages", "team")
	if err := os.MkdirAll(filepath.Join(pkgDir, "skills", "helper"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(pkgDir, "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"team","skills":["skills/**/SKILL.md"],"prompts":["prompts/*.md"]}`
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "skills", "helper", "SKILL.md"), []byte("---\ndescription: helper\n---\nUse helper."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "prompts", "ship.md"), []byte("---\ndescription: ship\n---\nShip {{input}}"), 0o644); err != nil {
		t.Fatal(err)
	}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:       dir,
		AgentDir:  agentDir,
		Model:     newTestModel(),
		GetAPIKey: func(p string) (string, error) { return "", nil },
		StreamFn: func(ctx context.Context, _ *types.Model, llmCtx *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
			stream := types.NewEventStream()
			go func() {
				defer stream.Close()
				msg := &types.AssistantMessage{Role: "assistant", StopReason: "stop", Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "ok"}}, Timestamp: time.Now().UnixMilli()}
				stream.Push(types.StreamEvent{Type: "done", Reason: "stop", Message: msg})
				stream.Resolve(msg, nil)
			}()
			return stream, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	info := session.GetContextInfo()
	if len(info.Packages) != 1 || info.Packages[0].Name != "team" || info.Packages[0].Skills != 1 || info.Packages[0].Prompts != 1 {
		t.Fatalf("unexpected package info: %#v", info.Packages)
	}
	if !containsSkillName(info.Skills, "helper") {
		t.Fatalf("expected packaged helper skill, got %#v", info.Skills)
	}
	if !containsPromptName(info.PromptTemplates, "ship") {
		t.Fatalf("expected packaged ship prompt, got %#v", info.PromptTemplates)
	}

	lateDir := filepath.Join(dir, ".coding_agent", "packages", "late")
	if err := os.MkdirAll(filepath.Join(lateDir, "skills", "late"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(lateDir, "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lateDir, "package.json"), []byte(`{"name":"late","skills":["skills/**/SKILL.md"],"prompts":["prompts/*.md"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lateDir, "skills", "late", "SKILL.md"), []byte("---\ndescription: late\n---\nUse late."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lateDir, "prompts", "late.md"), []byte("---\ndescription: late\n---\nLate {{input}}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !containsSkillName(session.GetSkills(), "late") {
		t.Fatalf("expected post-start packaged late skill, got %#v", session.GetSkills())
	}
	if !containsPromptName(session.GetPromptTemplates(), "late") {
		t.Fatalf("expected post-start packaged late prompt, got %#v", session.GetPromptTemplates())
	}
}

func containsSkillName(skills []SkillInfo, name string) bool {
	for _, skill := range skills {
		if skill.Name == name {
			return true
		}
	}
	return false
}

func containsPromptName(prompts []PromptTemplateInfo, name string) bool {
	for _, prompt := range prompts {
		if prompt.Name == name {
			return true
		}
	}
	return false
}

func messagesContainText(messages []agent.AgentMessage, text string) bool {
	for _, msg := range messages {
		if strings.Contains(agentMessageText(msg), text) {
			return true
		}
	}
	return false
}

func agentMessageText(msg agent.AgentMessage) string {
	switch m := msg.(type) {
	case types.UserMessage:
		return contentText(m.Content)
	case *types.UserMessage:
		return contentText(m.Content)
	default:
		return ""
	}
}

func contentText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []types.ContentBlock:
		var parts []string
		for _, block := range c {
			if tc, ok := block.(*types.TextContent); ok && tc != nil {
				parts = append(parts, tc.Text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func hasAssistantMessageEnd(events []agent.Event, text string) bool {
	for _, event := range events {
		if event.Type != agent.EventTypeMessageEnd {
			continue
		}
		msg, ok := event.Message.(types.AssistantMessage)
		if !ok {
			continue
		}
		for _, block := range msg.Content {
			if tc, ok := block.(*types.TextContent); ok && tc.Text == text {
				return true
			}
		}
	}
	return false
}

func hasAgentEnd(events []agent.Event) bool {
	for _, event := range events {
		if event.Type == agent.EventTypeAgentEnd {
			return true
		}
	}
	return false
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

	mem := memory.New(agentDir, dir)
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

func containsRole(roles []string, want string) bool {
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
	session.ctxMgr.AddUsage(9000)

	for i := 0; i < 10; i++ {
		session.agent.AppendMessage(types.UserMessage{Role: "user", Content: "msg"})
	}
	msgsBefore := len(session.agent.GetState().Messages)

	session.ctxMgr.MaybeAutoCompact(context.Background())

	msgsAfter := len(session.agent.GetState().Messages)
	if msgsAfter != msgsBefore {
		t.Fatal("should not compact when AutoCompaction is disabled")
	}
}

func TestHarnessPlanFileReadable(t *testing.T) {
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

	var readTool agent.Tool
	for _, tool := range session.GetAgent().GetState().Tools {
		switch tool.Name() {
		case "read":
			readTool = tool
		}
	}
	if readTool == nil {
		t.Fatalf("expected read tool, got %v", session.GetActiveToolNames())
	}

	session.ExitPlanMode("ship feature safely", nil)
	paths := session.RuntimePaths()
	if _, err := os.Stat(paths.PlanFile); err != nil {
		t.Fatalf("expected plan file to exist: %v", err)
	}

	readResult, err := readTool.Execute(context.Background(), "read-plan", map[string]any{"path": paths.PlanFile}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readResult.Content[0].(*types.TextContent).Text, "ship feature safely") {
		t.Fatalf("expected read tool to access harness plan file, got %#v", readResult.Content)
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
		CustomTools: []agent.Tool{&testEchoTool{}},
		GetAPIKey:   func(provider string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	var echo agent.Tool
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

	session.ctxMgr.OnToolExecutionEnd(agent.Event{
		Type:     agent.EventTypeToolExecutionEnd,
		ToolName: "read",
		Args:     map[string]any{"path": targetFile},
		Result: agent.ToolResult{
			Details: map[string]any{"path": targetFile},
		},
	})

	if session.agent.QueuedMessageCount() != 1 {
		t.Fatalf("expected one queued steering message, got %d", session.agent.QueuedMessageCount())
	}

	session.ctxMgr.OnToolExecutionEnd(agent.Event{
		Type:     agent.EventTypeToolExecutionEnd,
		ToolName: "read",
		Args:     map[string]any{"path": targetFile},
		Result: agent.ToolResult{
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
	hiddenFollowUp := (&CustomMessage{
		Source: hiddenExtensionSource,
		Text:   "Continue working toward the active thread goal.",
	}).ToLlmMessage()
	normal := types.UserMessage{Role: "user", Content: "regular user message"}

	session.agent.AppendMessage(transient)
	session.handleMessageEnd(transient)
	session.agent.AppendMessage(hiddenFollowUp)
	session.handleMessageEnd(hiddenFollowUp)
	session.agent.AppendMessage(normal)
	session.handleMessageEnd(normal)
	session.ctxMgr.PruneTransient()

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
	if strings.Contains(text, hiddenExtensionSource) {
		t.Fatalf("expected hidden extension follow-up not to be persisted, got:\n%s", text)
	}
	if !strings.Contains(text, "regular user message") {
		t.Fatalf("expected regular message to be persisted, got:\n%s", text)
	}
}
