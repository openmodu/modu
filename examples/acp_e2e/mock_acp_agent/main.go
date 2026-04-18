// Command mock_acp_agent is a deterministic ACP agent used by the gateway
// E2E tests. It speaks LDJSON JSON-RPC 2.0 on stdio, the same wire
// protocol as `@zed-industries/claude-code-acp`, but produces scripted
// outputs so tests don't need an LLM API key.
//
// Prompt dispatch:
//
//   - a prompt containing "permission" triggers a reverse session/request_permission
//     request before the prompt reply.
//   - a prompt containing "error" makes the session/prompt reply fail.
//   - any other prompt produces three text chunks and stopReason=end_turn.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
)

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type agent struct {
	out    *bufio.Writer
	outMu  sync.Mutex
	sessID atomic.Int64
	nextID atomic.Int64

	// reverseWaiters tracks our outbound requests to the client (permission),
	// keyed by id → response channel.
	rmu       sync.Mutex
	reverse   map[int]chan *message
}

func (a *agent) writeFrame(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	a.outMu.Lock()
	defer a.outMu.Unlock()
	a.out.Write(b)
	a.out.WriteByte('\n')
	a.out.Flush()
}

func (a *agent) reply(id int, result any) {
	raw, _ := json.Marshal(result)
	a.writeFrame(message{JSONRPC: "2.0", ID: &id, Result: raw})
}

func (a *agent) replyError(id int, code int, msg string) {
	a.writeFrame(message{JSONRPC: "2.0", ID: &id, Error: &rpcError{Code: code, Message: msg}})
}

func (a *agent) notify(method string, params any) {
	raw, _ := json.Marshal(params)
	a.writeFrame(message{JSONRPC: "2.0", Method: method, Params: raw})
}

// reverseRequest sends a request TO the client and blocks until it replies.
func (a *agent) reverseRequest(method string, params any) *message {
	id := int(a.nextID.Add(1))
	ch := make(chan *message, 1)
	a.rmu.Lock()
	a.reverse[id] = ch
	a.rmu.Unlock()

	raw, _ := json.Marshal(params)
	a.writeFrame(message{JSONRPC: "2.0", ID: &id, Method: method, Params: raw})

	return <-ch
}

func (a *agent) handle(msg *message) {
	switch {
	case msg.Method != "" && msg.ID != nil:
		a.handleRequest(msg)
	case msg.Method == "" && msg.ID != nil:
		// response to one of our reverse requests
		a.rmu.Lock()
		ch, ok := a.reverse[*msg.ID]
		if ok {
			delete(a.reverse, *msg.ID)
		}
		a.rmu.Unlock()
		if ok {
			ch <- msg
		}
	case msg.Method != "" && msg.ID == nil:
		// notification from client — e.g. session/cancel
	}
}

func (a *agent) handleRequest(msg *message) {
	id := *msg.ID
	switch msg.Method {
	case "initialize":
		a.reply(id, map[string]any{
			"protocolVersion": 1,
			"capabilities":    map[string]any{},
		})
	case "session/new":
		n := a.sessID.Add(1)
		a.reply(id, map[string]any{"sessionId": fmt.Sprintf("sess-%d", n)})
	case "session/prompt":
		a.handlePrompt(id, msg)
	default:
		a.replyError(id, -32601, "method not implemented by mock")
	}
}

func (a *agent) handlePrompt(id int, msg *message) {
	var p struct {
		SessionID string           `json:"sessionId"`
		Prompt    []map[string]any `json:"prompt"`
	}
	_ = json.Unmarshal(msg.Params, &p)

	text := ""
	if len(p.Prompt) > 0 {
		text, _ = p.Prompt[0]["text"].(string)
	}

	if strings.Contains(text, "error") {
		a.replyError(id, -32603, "simulated prompt failure")
		return
	}

	if strings.Contains(text, "permission") {
		resp := a.reverseRequest("session/request_permission", map[string]any{
			"sessionId": p.SessionID,
			"toolCall": map[string]any{
				"toolCallId": "mock-tool-1",
				"title":      "simulated dangerous operation",
				"kind":       "execute",
			},
			"options": []map[string]any{
				{"optionId": "allow", "name": "Allow", "kind": "allow_once"},
				{"optionId": "deny", "name": "Deny", "kind": "reject_once"},
			},
		})
		// Inspect the outcome so the mock can indicate via text what happened.
		var outcome struct {
			Outcome struct {
				Outcome  string `json:"outcome"`
				OptionID string `json:"optionId"`
			} `json:"outcome"`
		}
		_ = json.Unmarshal(resp.Result, &outcome)
		a.notify("session/update", map[string]any{
			"sessionId": p.SessionID,
			"update": map[string]any{
				"sessionUpdate": "agent_message_chunk",
				"content": map[string]any{
					"type": "text",
					"text": fmt.Sprintf("permission-%s", outcome.Outcome.Outcome),
				},
			},
		})
		a.reply(id, map[string]any{"stopReason": "end_turn"})
		return
	}

	for _, chunk := range []string{"Hello", ", ", "world"} {
		a.notify("session/update", map[string]any{
			"sessionId": p.SessionID,
			"update": map[string]any{
				"sessionUpdate": "agent_message_chunk",
				"content": map[string]any{
					"type": "text",
					"text": chunk,
				},
			},
		})
	}
	a.reply(id, map[string]any{"stopReason": "end_turn"})
}

func main() {
	a := &agent{
		out:     bufio.NewWriter(os.Stdout),
		reverse: make(map[int]chan *message),
	}
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for in.Scan() {
		line := in.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var msg message
		if err := json.Unmarshal(line, &msg); err != nil {
			fmt.Fprintf(os.Stderr, "mock_acp_agent: parse: %v\n", err)
			continue
		}
		go a.handle(&msg)
	}
	if err := in.Err(); err != nil && err != io.EOF {
		fmt.Fprintf(os.Stderr, "mock_acp_agent: stdin: %v\n", err)
	}
}
