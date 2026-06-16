package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/providers/openai"
	"github.com/openmodu/modu/pkg/types"
)

// TestExperimentOnlyCurrentTurnThinkingOnTheWire runs the full request path
// (StreamDefault -> buildChatRequest -> provider.Stream -> buildBody -> HTTP POST)
// against a fake server and inspects the literal request body, proving that only
// the most recent assistant turn's thinking is sent to the model.
func TestExperimentOnlyCurrentTurnThinkingOnTheWire(t *testing.T) {
	var bodies [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, b)
		w.Header().Set("Content-Type", "text/event-stream")
		// Minimal valid SSE so the provider's stream parser finishes cleanly.
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	const provID = "experiment-openai"
	providers.Register(openai.New(provID, openai.WithBaseURL(srv.URL), openai.WithAPIKey("x")))

	model := &types.Model{ID: "test-model", ProviderID: provID}
	llmCtx := &types.LLMContext{
		Messages: []types.AgentMessage{
			types.UserMessage{Role: types.RoleUser, Content: "q1"},
			types.AssistantMessage{Role: types.RoleAssistant, Content: []types.ContentBlock{
				&types.ThinkingContent{Type: "thinking", Thinking: "OLD_TURN_THINKING_should_be_dropped"},
				&types.TextContent{Type: "text", Text: "answer 1"},
			}},
			types.UserMessage{Role: types.RoleUser, Content: "q2"},
			types.AssistantMessage{Role: types.RoleAssistant, Content: []types.ContentBlock{
				&types.ThinkingContent{Type: "thinking", Thinking: "CURRENT_TURN_THINKING_should_be_kept"},
				&types.TextContent{Type: "text", Text: "answer 2"},
			}},
		},
	}

	stream, err := StreamDefault(t.Context(), model, llmCtx, &types.SimpleStreamOptions{})
	if err != nil {
		t.Fatalf("StreamDefault: %v", err)
	}
	for range stream.Events() { // drain
	}

	if len(bodies) == 0 {
		t.Fatal("server received no request")
	}
	wire := string(bodies[0])

	// Pretty-print exactly what hit the wire so the experiment is inspectable.
	var pretty any
	_ = json.Unmarshal(bodies[0], &pretty)
	out, _ := json.MarshalIndent(pretty, "", "  ")
	t.Logf("=== literal request body sent to the model ===\n%s", out)

	if strings.Contains(wire, "OLD_TURN_THINKING_should_be_dropped") {
		t.Errorf("FAIL: historical thinking leaked onto the wire")
	} else {
		t.Logf("PASS: historical thinking absent from the wire")
	}
	if !strings.Contains(wire, "CURRENT_TURN_THINKING_should_be_kept") {
		t.Errorf("FAIL: current-turn thinking missing from the wire")
	} else {
		t.Logf("PASS: current-turn thinking present on the wire")
	}
}
