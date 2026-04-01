package subagent

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

type testTool struct {
	name string
}

func (t *testTool) Name() string        { return t.name }
func (t *testTool) Label() string       { return t.name }
func (t *testTool) Description() string { return t.name }
func (t *testTool) Parameters() any     { return nil }
func (t *testTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	return agent.AgentToolResult{}, nil
}

func TestRunRespectsToolFilteringAndThinkingLevel(t *testing.T) {
	model := &types.Model{ID: "mock", ProviderID: "mock"}
	def := &SubagentDefinition{
		Name:            "tester",
		SystemPrompt:    "test prompt",
		Tools:           []string{"read", "bash"},
		DisallowedTools: []string{"bash"},
		ThinkingLevel:   agent.ThinkingLevelHigh,
	}

	streamFn := func(ctx context.Context, _ *types.Model, llmCtx *types.LLMContext, opts *types.SimpleStreamOptions) (types.EventStream, error) {
		stream := types.NewEventStream()
		go func() {
			text := "ok"
			if len(llmCtx.Tools) != 1 || llmCtx.Tools[0].Name != "read" {
				text = "bad-tools"
			}
			if opts == nil || opts.Reasoning != types.ThinkingLevelHigh {
				text = "bad-thinking"
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
			stream.Close()
		}()
		return stream, nil
	}

	result, err := Run(
		context.Background(),
		def,
		"do it",
		[]agent.AgentTool{&testTool{name: "read"}, &testTool{name: "bash"}},
		model,
		func(string) (string, error) { return "", nil },
		streamFn,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result != "ok" {
		t.Fatalf("expected ok, got %q", result)
	}
}

func TestParseDefinitionSupportsExtendedFrontmatter(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/agent.md"
	content := `---
description: test agent
tools: read, bash
disallowed_tools: bash
permission_mode: read-only
background: true
effort: high
thinking: high
max_turns: 3
---
You are a test agent.`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	def, err := ParseDefinition(path, "project")
	if err != nil {
		t.Fatal(err)
	}
	if len(def.Tools) != 2 || def.Tools[0] != "read" || def.Tools[1] != "bash" {
		t.Fatalf("unexpected tools: %#v", def.Tools)
	}
	if len(def.DisallowedTools) != 1 || def.DisallowedTools[0] != "bash" {
		t.Fatalf("unexpected disallowed tools: %#v", def.DisallowedTools)
	}
	if def.PermissionMode != "read-only" {
		t.Fatalf("unexpected permission mode: %q", def.PermissionMode)
	}
	if !def.Background {
		t.Fatal("expected background=true")
	}
	if def.Effort != "high" {
		t.Fatalf("unexpected effort: %q", def.Effort)
	}
	if def.ThinkingLevel != agent.ThinkingLevelHigh {
		t.Fatalf("unexpected thinking level: %q", def.ThinkingLevel)
	}
	if def.MaxTurns != 3 {
		t.Fatalf("unexpected max turns: %d", def.MaxTurns)
	}
}

func TestRunUsesEffortWhenThinkingLevelNotExplicit(t *testing.T) {
	model := &types.Model{ID: "mock", ProviderID: "mock"}
	def := &SubagentDefinition{
		Name:         "effort-agent",
		SystemPrompt: "effort prompt",
		Effort:       "high",
	}

	streamFn := func(ctx context.Context, _ *types.Model, llmCtx *types.LLMContext, opts *types.SimpleStreamOptions) (types.EventStream, error) {
		stream := types.NewEventStream()
		go func() {
			text := "ok"
			if opts == nil || opts.Reasoning != types.ThinkingLevelHigh {
				text = "bad-effort-mapping"
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
			stream.Close()
		}()
		return stream, nil
	}

	result, err := Run(
		context.Background(),
		def,
		"do work",
		[]agent.AgentTool{},
		model,
		func(string) (string, error) { return "", nil },
		streamFn,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result != "ok" {
		t.Fatalf("expected ok, got %q", result)
	}
}

func TestRunReadOnlyPermissionModeFiltersMutatingTools(t *testing.T) {
	model := &types.Model{ID: "mock", ProviderID: "mock"}
	def := &SubagentDefinition{
		Name:           "reader",
		SystemPrompt:   "read only",
		PermissionMode: "read-only",
	}

	streamFn := func(ctx context.Context, _ *types.Model, llmCtx *types.LLMContext, opts *types.SimpleStreamOptions) (types.EventStream, error) {
		stream := types.NewEventStream()
		go func() {
			toolNames := make(map[string]bool)
			for _, tool := range llmCtx.Tools {
				toolNames[tool.Name] = true
			}

			text := "ok"
			if !toolNames["read"] || !toolNames["find"] {
				text = "missing-readonly-tools"
			}
			if toolNames["bash"] || toolNames["edit"] || toolNames["memo"] {
				text = "included-mutation-tools"
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
			stream.Close()
		}()
		return stream, nil
	}

	result, err := Run(
		context.Background(),
		def,
		"inspect",
		[]agent.AgentTool{
			&testTool{name: "read"},
			&testTool{name: "find"},
			&testTool{name: "bash"},
			&testTool{name: "edit"},
			&testTool{name: "memo"},
		},
		model,
		func(string) (string, error) { return "", nil },
		streamFn,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result != "ok" {
		t.Fatalf("expected ok, got %q", result)
	}
}

func TestRunRespectsMaxTurns(t *testing.T) {
	model := &types.Model{ID: "mock", ProviderID: "mock"}
	def := &SubagentDefinition{
		Name:         "looper",
		SystemPrompt: "loop",
		MaxTurns:     1,
	}

	callCount := 0
	streamFn := func(ctx context.Context, _ *types.Model, llmCtx *types.LLMContext, opts *types.SimpleStreamOptions) (types.EventStream, error) {
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
						&types.ToolCallContent{Type: "toolCall", ID: "tool-1", Name: "read", Arguments: map[string]any{}},
					},
					Timestamp: time.Now().UnixMilli(),
				}
				stream.Push(types.StreamEvent{Type: "done", Reason: "toolUse", Message: msg})
				stream.Resolve(msg, nil)
			} else {
				msg = &types.AssistantMessage{
					Role:       "assistant",
					ProviderID: model.ProviderID,
					Model:      model.ID,
					StopReason: "stop",
					Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: "should-not-happen"}},
					Timestamp:  time.Now().UnixMilli(),
				}
				stream.Push(types.StreamEvent{Type: "done", Reason: "stop", Message: msg})
				stream.Resolve(msg, nil)
			}
			stream.Close()
		}()
		return stream, nil
	}

	_, err := Run(
		context.Background(),
		def,
		"loop",
		[]agent.AgentTool{&testTool{name: "read"}},
		model,
		func(string) (string, error) { return "", nil },
		streamFn,
	)
	if err == nil || !strings.Contains(err.Error(), "max_turns=1") {
		t.Fatalf("expected max_turns error, got %v", err)
	}
}
