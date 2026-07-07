package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/types"
)

func TestRequestPermissionSendsSingleReverseRPCWithRejectAlways(t *testing.T) {
	var out bytes.Buffer
	s := testACPServer(&out)
	resultCh := make(chan types.ToolApprovalDecision, 1)

	go func() {
		decision, _ := s.requestPermission(context.Background(), "bash", "call-1", map[string]any{"cmd": "pwd"})
		resultCh <- decision
	}()

	id := waitReverseRequest(t, s)
	lines := waitWrittenLines(t, s, &out)
	if len(lines) != 1 {
		t.Fatalf("expected one permission frame, got %d: %q", len(lines), strings.Join(lines, "\n"))
	}
	var frame rpcMsg
	if err := json.Unmarshal([]byte(lines[0]), &frame); err != nil {
		t.Fatal(err)
	}
	if frame.Method != "session/request_permission" || frame.ID == nil {
		t.Fatalf("expected reverse permission RPC, got %#v", frame)
	}
	var params struct {
		Options []struct {
			OptionID string `json:"optionId"`
		} `json:"options"`
	}
	if err := json.Unmarshal(frame.Params, &params); err != nil {
		t.Fatal(err)
	}
	var hasRejectAlways bool
	for _, opt := range params.Options {
		if opt.OptionID == "reject_always" {
			hasRejectAlways = true
			break
		}
	}
	if !hasRejectAlways {
		t.Fatalf("expected reject_always option, got %#v", params.Options)
	}

	s.dispatch(context.Background(), &rpcMsg{
		JSONRPC: "2.0",
		ID:      &id,
		Result:  json.RawMessage(`{"outcome":{"optionId":"reject_always"}}`),
	})

	select {
	case got := <-resultCh:
		if got != types.ToolApprovalDenyAlways {
			t.Fatalf("expected deny always, got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for permission result")
	}
}

func TestRequestPermissionDeniesMalformedResult(t *testing.T) {
	var out bytes.Buffer
	s := testACPServer(&out)
	resultCh := make(chan types.ToolApprovalDecision, 1)

	go func() {
		decision, _ := s.requestPermission(context.Background(), "bash", "call-1", nil)
		resultCh <- decision
	}()

	id := waitReverseRequest(t, s)
	s.dispatch(context.Background(), &rpcMsg{
		JSONRPC: "2.0",
		ID:      &id,
		Result:  json.RawMessage(`{"outcome":`),
	})

	select {
	case got := <-resultCh:
		if got != types.ToolApprovalDeny {
			t.Fatalf("expected malformed response to deny, got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for permission result")
	}
}

func TestRequestPermissionDeniesUnknownOption(t *testing.T) {
	var out bytes.Buffer
	s := testACPServer(&out)
	resultCh := make(chan types.ToolApprovalDecision, 1)

	go func() {
		decision, _ := s.requestPermission(context.Background(), "bash", "call-1", nil)
		resultCh <- decision
	}()

	id := waitReverseRequest(t, s)
	s.dispatch(context.Background(), &rpcMsg{
		JSONRPC: "2.0",
		ID:      &id,
		Result:  json.RawMessage(`{"outcome":{"optionId":"surprise"}}`),
	})

	select {
	case got := <-resultCh:
		if got != types.ToolApprovalDeny {
			t.Fatalf("expected unknown option to deny, got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for permission result")
	}
}

func TestRequestPermissionReturnsErrorWhenParamsCannotMarshal(t *testing.T) {
	var out bytes.Buffer
	s := testACPServer(&out)
	decision, err := s.requestPermission(context.Background(), "bad", "call-1", map[string]any{"bad": func() {}})
	if err == nil {
		t.Fatal("expected marshal error")
	}
	if decision != types.ToolApprovalDeny {
		t.Fatalf("expected deny on marshal error, got %q", decision)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Fatalf("expected no frame on marshal error, got %q", out.String())
	}
	if len(s.reverse) != 0 {
		t.Fatalf("expected reverse request cleanup, got %#v", s.reverse)
	}
}

func TestReplySendsErrorFrameWhenResultCannotMarshal(t *testing.T) {
	var out bytes.Buffer
	s := testACPServer(&out)
	s.reply(7, map[string]any{"bad": func() {}})
	lines := waitWrittenLines(t, s, &out)
	if len(lines) != 1 {
		t.Fatalf("expected one frame, got %d", len(lines))
	}
	var frame rpcMsg
	if err := json.Unmarshal([]byte(lines[0]), &frame); err != nil {
		t.Fatal(err)
	}
	if frame.ID == nil || *frame.ID != 7 || frame.Error == nil || frame.Error.Code != -32603 {
		t.Fatalf("expected error reply frame, got %#v", frame)
	}
}

func TestPromptRejectsUnknownSession(t *testing.T) {
	var out bytes.Buffer
	s := testACPServer(&out)
	s.activeSessionID = "modu-sess-1"
	id := int64(9)
	params := mustJSON(t, map[string]any{
		"sessionId": "modu-sess-2",
		"prompt":    []map[string]string{{"type": "text", "text": "hello"}},
	})

	s.handlePrompt(context.Background(), id, &rpcMsg{ID: &id, Params: params})

	lines := waitWrittenLines(t, s, &out)
	if len(lines) != 1 {
		t.Fatalf("expected one error frame, got %d: %q", len(lines), strings.Join(lines, "\n"))
	}
	var frame rpcMsg
	if err := json.Unmarshal([]byte(lines[0]), &frame); err != nil {
		t.Fatal(err)
	}
	if frame.Error == nil || !strings.Contains(frame.Error.Message, "unknown session") {
		t.Fatalf("expected unknown session error, got %#v", frame)
	}
}

func TestPromptsRunSeriallyForSharedSession(t *testing.T) {
	var out bytes.Buffer
	dir := t.TempDir()
	model := &types.Model{ID: "test", ProviderID: "test"}
	var calls atomic.Int64
	var active atomic.Int64
	var peak atomic.Int64
	streamFn := func(ctx context.Context, _ *types.Model, _ *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		stream := types.NewEventStream()
		go func() {
			current := active.Add(1)
			for {
				p := peak.Load()
				if current <= p || peak.CompareAndSwap(p, current) {
					break
				}
			}
			defer active.Add(-1)
			defer stream.Close()
			time.Sleep(20 * time.Millisecond)
			n := calls.Add(1)
			msg := &types.AssistantMessage{
				Role:       types.RoleAssistant,
				ProviderID: model.ProviderID,
				Model:      model.ID,
				StopReason: "stop",
				Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: "ok"}},
				Usage:      types.AgentUsage{Input: int(n), Output: 1, TotalTokens: int(n) + 1},
				Timestamp:  time.Now().UnixMilli(),
			}
			stream.Push(types.StreamEvent{Type: types.EventDone, Reason: "stop", Message: msg})
			stream.Resolve(msg, nil)
		}()
		return stream, nil
	}
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:       dir,
		AgentDir:  filepath.Join(dir, ".modu"),
		Model:     model,
		GetAPIKey: func(string) (string, error) { return "", nil },
		StreamFn:  streamFn,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close("test")
	s := &Server{
		session:         session,
		out:             bufio.NewWriter(&out),
		reverse:         make(map[int64]chan *rpcMsg),
		activeSessionID: "modu-sess-1",
	}

	var wg sync.WaitGroup
	for i := int64(1); i <= 2; i++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			params := mustJSON(t, map[string]any{
				"sessionId": "modu-sess-1",
				"prompt":    []map[string]string{{"type": "text", "text": "hello"}},
			})
			s.handlePrompt(context.Background(), id, &rpcMsg{ID: &id, Params: params})
		}(i)
	}
	wg.Wait()

	if got := calls.Load(); got != 2 {
		t.Fatalf("expected both prompts to run, calls=%d output=%s", got, out.String())
	}
	if got := peak.Load(); got != 1 {
		t.Fatalf("expected prompt streams to be serialized, peak=%d", got)
	}
	if strings.Contains(out.String(), "agent is already processing") {
		t.Fatalf("expected no concurrent-agent error, output:\n%s", out.String())
	}
}

func testACPServer(out *bytes.Buffer) *Server {
	return &Server{
		out:     bufio.NewWriter(out),
		reverse: make(map[int64]chan *rpcMsg),
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func waitReverseRequest(t *testing.T, s *Server) int64 {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s.revMu.Lock()
		for id := range s.reverse {
			s.revMu.Unlock()
			return id
		}
		s.revMu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for reverse request")
	return 0
}

func waitWrittenLines(t *testing.T, s *Server, out *bytes.Buffer) []string {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s.outMu.Lock()
		text := strings.TrimSpace(out.String())
		s.outMu.Unlock()
		if text != "" {
			return strings.Split(text, "\n")
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for written frame")
	return nil
}
