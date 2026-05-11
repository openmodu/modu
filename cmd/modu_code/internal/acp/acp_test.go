package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/openmodu/modu/pkg/agent"
)

func TestRequestPermissionSendsSingleReverseRPCWithRejectAlways(t *testing.T) {
	var out bytes.Buffer
	s := testACPServer(&out)
	resultCh := make(chan agent.ToolApprovalDecision, 1)

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
		if got != agent.ToolApprovalDenyAlways {
			t.Fatalf("expected deny always, got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for permission result")
	}
}

func TestRequestPermissionDeniesMalformedResult(t *testing.T) {
	var out bytes.Buffer
	s := testACPServer(&out)
	resultCh := make(chan agent.ToolApprovalDecision, 1)

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
		if got != agent.ToolApprovalDeny {
			t.Fatalf("expected malformed response to deny, got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for permission result")
	}
}

func TestRequestPermissionDeniesUnknownOption(t *testing.T) {
	var out bytes.Buffer
	s := testACPServer(&out)
	resultCh := make(chan agent.ToolApprovalDecision, 1)

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
		if got != agent.ToolApprovalDeny {
			t.Fatalf("expected unknown option to deny, got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for permission result")
	}
}

func testACPServer(out *bytes.Buffer) *Server {
	return &Server{
		out:     bufio.NewWriter(out),
		reverse: make(map[int64]chan *rpcMsg),
	}
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
