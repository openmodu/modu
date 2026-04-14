package trace

import (
	"context"
	"sync"
	"testing"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func newTestBridge(t *testing.T) (*OTelBridge, *tracetest.InMemoryExporter) {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
	)
	bridge, err := NewOTelBridge(context.Background(), OTelOptions{
		Provider:      tp,
		ServiceName:   "test-agent",
		SessionID:     "sess-1",
		Cwd:           "/tmp/test",
		ModelProvider: "openai",
		ModelID:       "gpt-5.4",
	})
	if err != nil {
		t.Fatal(err)
	}
	return bridge, exporter
}

func TestOTelBridgeSessionSpan(t *testing.T) {
	bridge, exporter := newTestBridge(t)

	bridge.RecordSessionEvent("session_start", map[string]any{
		"source": "test",
	})

	if err := bridge.Close(context.Background(), "test-done"); err != nil {
		t.Fatal(err)
	}

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}

	sessionSpan := spans[len(spans)-1]
	if sessionSpan.Name != "coding_agent.session" {
		t.Fatalf("expected session span, got %s", sessionSpan.Name)
	}

	// Check session attributes
	attrMap := spanAttributeMap(sessionSpan.Attributes)
	if got := attrMap["agent.session_id"]; got != "sess-1" {
		t.Fatalf("expected session_id=sess-1, got %v", got)
	}
	if got := attrMap["llm.model"]; got != "gpt-5.4" {
		t.Fatalf("expected model=gpt-5.4, got %v", got)
	}

	// Check events
	eventNames := make([]string, 0, len(sessionSpan.Events))
	for _, e := range sessionSpan.Events {
		eventNames = append(eventNames, e.Name)
	}
	if !contains(eventNames, "session_start") {
		t.Fatalf("expected session_start event, got %v", eventNames)
	}
	if !contains(eventNames, "session_end") {
		t.Fatalf("expected session_end event, got %v", eventNames)
	}
}

func TestOTelBridgeTurnAndToolSpans(t *testing.T) {
	bridge, exporter := newTestBridge(t)

	bridge.RecordAgentEvent(agent.AgentEvent{Type: agent.EventTypeAgentStart})
	bridge.RecordAgentEvent(agent.AgentEvent{Type: agent.EventTypeTurnStart})
	bridge.RecordAgentEvent(agent.AgentEvent{
		Type: agent.EventTypeMessageEnd,
		Message: types.AssistantMessage{
			Role: "assistant",
			Content: []types.ContentBlock{
				&types.TextContent{Type: "text", Text: "let me read that file"},
			},
			Usage:      types.AgentUsage{Input: 100, Output: 50, TotalTokens: 150},
			StopReason: "end_turn",
		},
	})
	bridge.RecordAgentEvent(agent.AgentEvent{
		Type:       agent.EventTypeToolExecutionStart,
		ToolCallID: "tc-1",
		ToolName:   "read",
		Args:       map[string]any{"path": "/tmp/test.txt"},
	})
	bridge.RecordAgentEvent(agent.AgentEvent{
		Type:       agent.EventTypeToolExecutionUpdate,
		ToolCallID: "tc-1",
		ToolName:   "read",
	})
	bridge.RecordAgentEvent(agent.AgentEvent{
		Type:       agent.EventTypeToolExecutionEnd,
		ToolCallID: "tc-1",
		ToolName:   "read",
		Result: agent.AgentToolResult{
			Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "file content"}},
		},
	})
	bridge.RecordAgentEvent(agent.AgentEvent{
		Type: agent.EventTypeTurnEnd,
		ToolResults: []types.ToolResultMessage{
			{Role: "tool_result"},
		},
	})
	bridge.RecordAgentEvent(agent.AgentEvent{Type: agent.EventTypeAgentEnd})

	if err := bridge.Close(context.Background(), "done"); err != nil {
		t.Fatal(err)
	}

	spans := exporter.GetSpans()
	spanNames := make(map[string]int)
	for _, s := range spans {
		spanNames[s.Name]++
	}

	if spanNames["coding_agent.turn"] != 1 {
		t.Fatalf("expected 1 turn span, got %d in %v", spanNames["coding_agent.turn"], spanNames)
	}
	if spanNames["coding_agent.llm"] != 1 {
		t.Fatalf("expected 1 llm span, got %d", spanNames["coding_agent.llm"])
	}
	if spanNames["coding_agent.tool.read"] != 1 {
		t.Fatalf("expected 1 tool.read span, got %d", spanNames["coding_agent.tool.read"])
	}

	// Verify tool span attributes
	for _, s := range spans {
		if s.Name == "coding_agent.tool.read" {
			attrMap := spanAttributeMap(s.Attributes)
			if got := attrMap["tool.name"]; got != "read" {
				t.Fatalf("expected tool.name=read, got %v", got)
			}
			if got := attrMap["tool.call_id"]; got != "tc-1" {
				t.Fatalf("expected tool.call_id=tc-1, got %v", got)
			}
			// Check for tool_progress event from the update
			hasProgress := false
			for _, e := range s.Events {
				if e.Name == "tool_progress" {
					hasProgress = true
				}
			}
			if !hasProgress {
				t.Fatal("expected tool_progress event from ToolExecutionUpdate")
			}
		}
	}
}

func TestOTelBridgeInterrupt(t *testing.T) {
	bridge, exporter := newTestBridge(t)

	bridge.RecordAgentEvent(agent.AgentEvent{Type: agent.EventTypeAgentStart})
	bridge.RecordAgentEvent(agent.AgentEvent{Type: agent.EventTypeTurnStart})
	bridge.RecordAgentEvent(agent.AgentEvent{
		Type: agent.EventTypeInterrupt,
		Interrupt: &agent.InterruptEvent{
			Reason:    "max_steps",
			StepCount: 10,
		},
	})
	bridge.RecordAgentEvent(agent.AgentEvent{Type: agent.EventTypeTurnEnd})
	bridge.RecordAgentEvent(agent.AgentEvent{Type: agent.EventTypeAgentEnd})

	if err := bridge.Close(context.Background(), "interrupted"); err != nil {
		t.Fatal(err)
	}

	spans := exporter.GetSpans()
	// Find the turn span and check for interrupt event
	foundInterrupt := false
	for _, s := range spans {
		if s.Name == "coding_agent.turn" {
			for _, e := range s.Events {
				if e.Name == "interrupt" {
					foundInterrupt = true
					// Just verify the event exists with some attributes
					if len(e.Attributes) == 0 {
						t.Fatal("expected attributes on interrupt event")
					}
				}
			}
		}
	}
	if !foundInterrupt {
		t.Fatal("expected interrupt event on turn span")
	}
}

func TestOTelBridgeModelUpdate(t *testing.T) {
	bridge, exporter := newTestBridge(t)

	bridge.RecordSessionEvent("model_change", map[string]any{
		"provider": "anthropic",
		"modelId":  "claude-opus-4-6",
	})

	if err := bridge.Close(context.Background(), "done"); err != nil {
		t.Fatal(err)
	}

	spans := exporter.GetSpans()
	sessionSpan := spans[len(spans)-1]
	attrMap := spanAttributeMap(sessionSpan.Attributes)
	if got := attrMap["llm.provider"]; got != "anthropic" {
		t.Fatalf("expected updated provider=anthropic, got %v", got)
	}
	if got := attrMap["llm.model"]; got != "claude-opus-4-6" {
		t.Fatalf("expected updated model=claude-opus-4-6, got %v", got)
	}
}

func TestOTelBridgeNilSafety(t *testing.T) {
	// Nil bridge should not panic
	var bridge *OTelBridge
	bridge.RecordSessionEvent("test", nil)
	bridge.RecordAgentEvent(agent.AgentEvent{Type: agent.EventTypeAgentStart})
	if err := bridge.Close(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
}

func TestOTelBridgeConcurrency(t *testing.T) {
	bridge, _ := newTestBridge(t)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bridge.RecordAgentEvent(agent.AgentEvent{Type: agent.EventTypeAgentStart})
			bridge.RecordSessionEvent("ping", map[string]any{"i": 1})
		}()
	}
	wg.Wait()
	if err := bridge.Close(context.Background(), "concurrent-done"); err != nil {
		t.Fatal(err)
	}
}

func TestOTelBridgeCostAttributes(t *testing.T) {
	bridge, exporter := newTestBridge(t)

	bridge.RecordAgentEvent(agent.AgentEvent{Type: agent.EventTypeAgentStart})
	bridge.RecordAgentEvent(agent.AgentEvent{Type: agent.EventTypeTurnStart})
	bridge.RecordAgentEvent(agent.AgentEvent{
		Type: agent.EventTypeMessageEnd,
		Message: types.AssistantMessage{
			Role: "assistant",
			Content: []types.ContentBlock{
				&types.TextContent{Type: "text", Text: "hello"},
			},
			Usage: types.AgentUsage{
				Input:       100,
				Output:      50,
				TotalTokens: 150,
				Cost: struct {
					Input      float64 `json:"input"`
					Output     float64 `json:"output"`
					CacheRead  float64 `json:"cacheRead"`
					CacheWrite float64 `json:"cacheWrite"`
					Total      float64 `json:"total"`
				}{
					Input:  0.001,
					Output: 0.003,
					Total:  0.004,
				},
			},
			StopReason: "end_turn",
		},
	})
	bridge.RecordAgentEvent(agent.AgentEvent{Type: agent.EventTypeTurnEnd})
	bridge.RecordAgentEvent(agent.AgentEvent{Type: agent.EventTypeAgentEnd})

	if err := bridge.Close(context.Background(), "done"); err != nil {
		t.Fatal(err)
	}

	spans := exporter.GetSpans()
	for _, s := range spans {
		if s.Name == "coding_agent.llm" {
			attrMap := spanAttributeMap(s.Attributes)
			if got, ok := attrMap["llm.cost.total"]; !ok || got != 0.004 {
				t.Fatalf("expected llm.cost.total=0.004, got %v", got)
			}
		}
	}
}

// --- helpers ---

func spanAttributeMap(attrs []attribute.KeyValue) map[string]any {
	m := make(map[string]any, len(attrs))
	for _, a := range attrs {
		m[string(a.Key)] = a.Value.AsInterface()
	}
	return m
}

func contains(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}
	return false
}
