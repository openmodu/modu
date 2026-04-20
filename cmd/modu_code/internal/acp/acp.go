// Package acp implements an ACP stdio server (JSON-RPC 2.0 LDJSON) that
// wraps a modu CodingSession. Register modu_code in acp.config.json:
//
//	{"id": "modu", "command": "modu_code", "args": ["--acp"]}
package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/openmodu/modu/pkg/agent"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/types"
)

// Server wraps a CodingSession in the ACP JSON-RPC 2.0 stdio protocol.
type Server struct {
	session *coding_agent.CodingSession

	outMu  sync.Mutex
	out    *bufio.Writer
	sessID atomic.Int64
	msgID  atomic.Int64

	// reverse tracks outbound RPC requests (used for permission prompts).
	revMu   sync.Mutex
	reverse map[int64]chan *rpcMsg
}

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// New creates an ACP server for the given session.
func New(session *coding_agent.CodingSession) *Server {
	return &Server{
		session: session,
		out:     bufio.NewWriter(os.Stdout),
		reverse: make(map[int64]chan *rpcMsg),
	}
}

// Run starts the ACP dispatch loop. Blocks until stdin closes or ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	// Wire tool approval → session/request_permission reverse RPC.
	s.session.SetToolApprovalCallback(func(toolName, toolCallID string, args map[string]any) (agent.ToolApprovalDecision, error) {
		return s.requestPermission(ctx, toolName, toolCallID, args)
	})

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for sc.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line := sc.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var msg rpcMsg
		if err := json.Unmarshal(line, &msg); err != nil {
			fmt.Fprintf(os.Stderr, "modu_code acp: parse: %v\n", err)
			continue
		}
		go s.dispatch(ctx, &msg)
	}
	if err := sc.Err(); err != nil && err != io.EOF {
		return err
	}
	return nil
}

func (s *Server) dispatch(ctx context.Context, msg *rpcMsg) {
	// Response to an outbound reverse request (e.g. permission reply).
	if msg.Method == "" && msg.ID != nil {
		s.revMu.Lock()
		ch, ok := s.reverse[*msg.ID]
		if ok {
			delete(s.reverse, *msg.ID)
		}
		s.revMu.Unlock()
		if ok {
			ch <- msg
		}
		return
	}
	if msg.ID == nil {
		// Inbound notification (session/cancel etc.) — not yet handled.
		return
	}
	id := *msg.ID
	switch msg.Method {
	case "initialize":
		s.reply(id, map[string]any{
			"protocolVersion": 1,
			"capabilities":    map[string]any{},
			"serverInfo":      map[string]any{"name": "modu_code", "version": "0.1.0"},
		})
	case "session/new":
		n := s.sessID.Add(1)
		// Each session/new clears history so this subprocess is stateless
		// across sessions (matches how ACP uses (agentID,cwd) as session key).
		s.session.GetAgent().ClearMessages()
		s.reply(id, map[string]any{"sessionId": fmt.Sprintf("modu-sess-%d", n)})
	case "session/prompt":
		go s.handlePrompt(ctx, id, msg)
	default:
		s.replyErr(id, -32601, "method not found")
	}
}

func (s *Server) handlePrompt(ctx context.Context, id int64, msg *rpcMsg) {
	var p struct {
		SessionID string `json:"sessionId"`
		Prompt    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"prompt"`
	}
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		s.replyErr(id, -32602, "invalid params")
		return
	}
	var promptText strings.Builder
	for _, part := range p.Prompt {
		if part.Type == "text" {
			promptText.WriteString(part.Text)
		}
	}

	// Subscribe to agent events and forward text deltas as session/update.
	unsub := s.session.Subscribe(func(ev agent.AgentEvent) {
		if ev.Type != agent.EventTypeMessageUpdate || ev.StreamEvent == nil {
			return
		}
		se := ev.StreamEvent
		if se.Type == types.EventTextDelta && se.Delta != "" {
			s.notify("session/update", map[string]any{
				"sessionId": p.SessionID,
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]any{"type": "text", "text": se.Delta},
				},
			})
		}
	})
	defer unsub()

	if err := s.session.Prompt(ctx, promptText.String()); err != nil {
		s.replyErr(id, -32603, err.Error())
		return
	}
	s.reply(id, map[string]any{"stopReason": "end_turn"})
}

// requestPermission sends a session/request_permission reverse RPC and blocks
// until the client replies. Returns the chosen approval decision.
func (s *Server) requestPermission(ctx context.Context, toolName, toolCallID string, args map[string]any) (agent.ToolApprovalDecision, error) {
	reqID := s.msgID.Add(1)
	ch := make(chan *rpcMsg, 1)

	s.revMu.Lock()
	s.reverse[reqID] = ch
	s.revMu.Unlock()

	options := []map[string]any{
		{"optionId": "allow_once", "name": "Allow once", "kind": "allow_once"},
		{"optionId": "allow_always", "name": "Always allow", "kind": "allow_always"},
		{"optionId": "reject_once", "name": "Reject once", "kind": "reject_once"},
	}
	s.notify("session/request_permission", map[string]any{
		"toolCall": map[string]any{
			"toolCallId": toolCallID,
			"title":      toolName,
			"kind":       "execute",
			"arguments":  args,
		},
		"options": options,
	})
	// Also send as a proper reverse RPC so the client can reply.
	s.writeFrame(rpcMsg{
		JSONRPC: "2.0",
		ID:      &reqID,
		Method:  "session/request_permission",
		Params: func() json.RawMessage {
			raw, _ := json.Marshal(map[string]any{
				"toolCall": map[string]any{
					"toolCallId": toolCallID,
					"title":      toolName,
					"kind":       "execute",
					"arguments":  args,
				},
				"options": options,
			})
			return raw
		}(),
	})

	select {
	case <-ctx.Done():
		s.revMu.Lock()
		delete(s.reverse, reqID)
		s.revMu.Unlock()
		return agent.ToolApprovalDeny, ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			return agent.ToolApprovalDeny, nil
		}
		var result struct {
			Outcome struct {
				OptionID string `json:"optionId"`
			} `json:"outcome"`
		}
		_ = json.Unmarshal(resp.Result, &result)
		switch result.Outcome.OptionID {
		case "allow_always":
			return agent.ToolApprovalAllowAlways, nil
		case "reject_once":
			return agent.ToolApprovalDeny, nil
		case "reject_always":
			return agent.ToolApprovalDenyAlways, nil
		default:
			return agent.ToolApprovalAllow, nil
		}
	}
}

// ─── wire helpers ───────────────────────────────────────────────────────────

func (s *Server) writeFrame(v any) {
	b, _ := json.Marshal(v)
	s.outMu.Lock()
	s.out.Write(b)
	s.out.WriteByte('\n')
	s.out.Flush()
	s.outMu.Unlock()
}

func (s *Server) reply(id int64, result any) {
	raw, _ := json.Marshal(result)
	s.writeFrame(rpcMsg{JSONRPC: "2.0", ID: &id, Result: raw})
}

func (s *Server) replyErr(id int64, code int, msg string) {
	s.writeFrame(rpcMsg{JSONRPC: "2.0", ID: &id, Error: &rpcError{Code: code, Message: msg}})
}

func (s *Server) notify(method string, params any) {
	raw, _ := json.Marshal(params)
	s.writeFrame(rpcMsg{JSONRPC: "2.0", Method: method, Params: raw})
}
