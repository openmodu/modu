package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openmodu/modu/pkg/acp/client"
	"github.com/openmodu/modu/pkg/acp/jsonrpc"
	"github.com/openmodu/modu/pkg/acp/manager"
)

// ---------- fake ACP agent transport ----------
//
// The fake responds to initialize / session/new / session/prompt with a
// scripted stream of session/update notifications. Tests stage behavior by
// swapping out the promptResponder callback.

type fakeTransport struct {
	lines  chan []byte
	done   chan struct{}
	agent  *fakeAgent
	mu     sync.Mutex
	closed bool
}

type fakeAgent struct {
	mu sync.Mutex

	sessionCounter atomic.Int32

	// promptResponder is called on every session/prompt. It receives the
	// request msg and a sink to emit session/update notifications with.
	// It must return the stopReason (or an error to reject the prompt).
	promptResponder func(msg *jsonrpc.Message, emit func(update map[string]any)) (string, error)

	// reverseRequest, if non-nil, is sent to the client when the prompt
	// starts — used to exercise session/request_permission routing.
	reverseRequest *jsonrpc.Request

	// reversePending holds the in-flight reverse-request id → response chan.
	reversePending map[int]chan *jsonrpc.Message
}

func newFakeAgent() *fakeAgent {
	return &fakeAgent{
		reversePending: map[int]chan *jsonrpc.Message{},
	}
}

func newFakeTransport(a *fakeAgent) *fakeTransport {
	return &fakeTransport{
		lines: make(chan []byte, 64),
		done:  make(chan struct{}),
		agent: a,
	}
}

func (t *fakeTransport) Start() error { return nil }
func (t *fakeTransport) Stop() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	close(t.lines)
	close(t.done)
	return nil
}
func (t *fakeTransport) Lines() <-chan []byte  { return t.lines }
func (t *fakeTransport) Done() <-chan struct{} { return t.done }

// Write receives a JSON-RPC frame from the Client. We dispatch it on a
// goroutine to avoid blocking the writer if a responder produces many
// session/update frames.
func (t *fakeTransport) Write(line []byte) error {
	var msg jsonrpc.Message
	if err := json.Unmarshal(line, &msg); err != nil {
		return nil // ignore malformed
	}
	go t.dispatch(&msg)
	return nil
}

func (t *fakeTransport) dispatch(msg *jsonrpc.Message) {
	switch {
	case msg.IsResponse():
		// reverse-RPC response from the client — resolve the waiter.
		if msg.ID == nil {
			return
		}
		t.agent.mu.Lock()
		ch, ok := t.agent.reversePending[*msg.ID]
		if ok {
			delete(t.agent.reversePending, *msg.ID)
		}
		t.agent.mu.Unlock()
		if ok {
			ch <- msg
		}
	case msg.IsRequest():
		t.handleRequest(msg)
	case msg.IsNotification():
		// session/cancel: ignore (test may assert on it elsewhere).
	}
}

func (t *fakeTransport) handleRequest(msg *jsonrpc.Message) {
	id := *msg.ID
	switch msg.Method {
	case "initialize":
		t.send(jsonrpc.NewResponse(id, map[string]any{
			"protocolVersion": 1,
			"capabilities":    map[string]any{},
		}))
	case "session/new":
		n := t.agent.sessionCounter.Add(1)
		t.send(jsonrpc.NewResponse(id, map[string]any{
			"sessionId": fmt.Sprintf("sess-%d", n),
		}))
	case "session/prompt":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		_ = msg.ParseParams(&p)

		// If configured, fire a reverse request before replying.
		if t.agent.reverseRequest != nil {
			rr := *t.agent.reverseRequest
			t.sendReverseRequest(p.SessionID, &rr)
		}

		emit := func(update map[string]any) {
			n := jsonrpc.NewNotification("session/update", map[string]any{
				"sessionId": p.SessionID,
				"update":    update,
			})
			t.send(n)
		}
		stopReason := "end_turn"
		if t.agent.promptResponder != nil {
			sr, err := t.agent.promptResponder(msg, emit)
			if err != nil {
				t.send(jsonrpc.NewErrorResponse(id, jsonrpc.InternalError, err.Error()))
				return
			}
			stopReason = sr
		}
		t.send(jsonrpc.NewResponse(id, map[string]any{"stopReason": stopReason}))
	default:
		t.send(jsonrpc.NewErrorResponse(id, jsonrpc.MethodNotFound, "not implemented in fake"))
	}
}

// sendReverseRequest issues a request to the Client (permission / fs) and
// waits for its response so tests can assert on it.
func (t *fakeTransport) sendReverseRequest(sessionID string, req *jsonrpc.Request) *jsonrpc.Message {
	// Rewrite params to inject sessionId for methods that expect one.
	if params, ok := req.Params.(map[string]any); ok {
		if _, has := params["sessionId"]; !has && sessionID != "" {
			params["sessionId"] = sessionID
		}
	}
	t.agent.mu.Lock()
	ch := make(chan *jsonrpc.Message, 1)
	t.agent.reversePending[req.ID] = ch
	t.agent.mu.Unlock()
	t.send(req)
	select {
	case resp := <-ch:
		return resp
	case <-time.After(5 * time.Second):
		return nil
	}
}

func (t *fakeTransport) send(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()
	if closed {
		return
	}
	t.lines <- b
}

var _ client.Transport = (*fakeTransport)(nil)

// ---------- test harness ----------

type harness struct {
	srv        *Server
	ts         *httptest.Server
	store      *Store
	agents     map[string]*fakeAgent
	transports map[string]*fakeTransport // key = agentID|cwd
	transMu    sync.Mutex
}

func newHarness(t *testing.T, token string, agentIDs ...string) *harness {
	t.Helper()
	if len(agentIDs) == 0 {
		agentIDs = []string{"claude"}
	}

	cfg := &manager.Config{}
	agents := map[string]*fakeAgent{}
	for _, id := range agentIDs {
		cfg.Agents = append(cfg.Agents, manager.AgentConfig{
			ID: id, Command: "ignored", Name: id,
		})
		agents[id] = newFakeAgent()
	}

	store := NewStore(32)
	mgr := manager.New(cfg, hooksFor(store))

	h := &harness{
		store:      store,
		agents:     agents,
		transports: map[string]*fakeTransport{},
	}
	// Inject a per-(agent,cwd) fake transport keyed so tests can reach it
	// for reverse-RPC staging. Note the factory has no cwd; we tag by agent
	// and accept that tests using one cwd per agent will see distinct keys.
	mgr.SetNewProcess(func(cfg manager.AgentConfig) client.Transport {
		tx := newFakeTransport(agents[cfg.ID])
		h.transMu.Lock()
		h.transports[cfg.ID] = tx
		h.transMu.Unlock()
		return tx
	})

	srv := NewServer(Options{
		Manager:     mgr,
		Store:       store,
		Token:       token,
		WorkersEach: 1,
	})
	h.srv = srv
	h.ts = httptest.NewServer(srv)
	t.Cleanup(func() {
		h.ts.Close()
		_ = srv.Close()
	})
	return h
}

func (h *harness) do(t *testing.T, method, path, token string, body any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req, err := http.NewRequest(method, h.ts.URL+path, &buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return resp
}

// ---------- tests ----------

func TestHealthz_NoAuth(t *testing.T) {
	h := newHarness(t, "secret")
	resp := h.do(t, "GET", "/healthz", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestAuthRequired(t *testing.T) {
	h := newHarness(t, "secret")
	// no token
	resp := h.do(t, "GET", "/api/agents", "", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no-token status = %d, want 401", resp.StatusCode)
	}
	// wrong token
	resp = h.do(t, "GET", "/api/agents", "wrong", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong-token status = %d, want 401", resp.StatusCode)
	}
	// right token
	resp = h.do(t, "GET", "/api/agents", "secret", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("right-token status = %d, want 200", resp.StatusCode)
	}
}

func TestListAgents(t *testing.T) {
	h := newHarness(t, "secret", "claude", "codex")
	resp := h.do(t, "GET", "/api/agents", "secret", nil)
	defer resp.Body.Close()
	var body struct {
		Agents []string `json:"agents"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Agents) != 2 {
		t.Errorf("agents = %v", body.Agents)
	}
}

func TestPostTask_Publishes(t *testing.T) {
	h := newHarness(t, "")
	h.agents["claude"].promptResponder = func(msg *jsonrpc.Message, emit func(map[string]any)) (string, error) {
		emit(map[string]any{
			"sessionUpdate": "agent_message_chunk",
			"content":       map[string]any{"type": "text", "text": "done"},
		})
		return "end_turn", nil
	}

	resp := h.do(t, "POST", "/api/tasks", "", map[string]any{
		"agent": "claude", "prompt": "hi", "cwd": "/tmp",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d: %s", resp.StatusCode, b)
	}
	var tk Task
	_ = json.NewDecoder(resp.Body).Decode(&tk)
	if tk.ID == "" {
		t.Error("empty id")
	}
	if tk.Agent != "claude" || tk.Prompt != "hi" {
		t.Errorf("task = %+v", tk)
	}
}

func TestPostTask_UnknownAgent(t *testing.T) {
	h := newHarness(t, "")
	resp := h.do(t, "POST", "/api/tasks", "", map[string]any{
		"agent": "ghost", "prompt": "hi",
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestPostTask_MissingFields(t *testing.T) {
	h := newHarness(t, "")
	resp := h.do(t, "POST", "/api/tasks", "", map[string]any{"agent": "claude"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestGetTask_Status(t *testing.T) {
	h := newHarness(t, "")
	h.agents["claude"].promptResponder = func(msg *jsonrpc.Message, emit func(map[string]any)) (string, error) {
		emit(map[string]any{
			"sessionUpdate": "agent_message_chunk",
			"content":       map[string]any{"type": "text", "text": "42"},
		})
		return "end_turn", nil
	}

	tk := postTask(t, h, "claude", "what?")
	waitFor(t, func() bool {
		cur, _ := h.store.Get(tk.ID)
		return cur.Status == TaskCompleted
	}, 2*time.Second)

	resp := h.do(t, "GET", "/api/tasks/"+tk.ID, "", nil)
	defer resp.Body.Close()
	var got Task
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got.Status != TaskCompleted {
		t.Errorf("status = %q", got.Status)
	}
	if got.Result != "42" {
		t.Errorf("result = %q", got.Result)
	}
}

func TestGetTask_NotFound(t *testing.T) {
	h := newHarness(t, "")
	resp := h.do(t, "GET", "/api/tasks/nope", "", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestStreamSSE(t *testing.T) {
	h := newHarness(t, "")
	h.agents["claude"].promptResponder = func(msg *jsonrpc.Message, emit func(map[string]any)) (string, error) {
		for _, s := range []string{"Hello", ", ", "world"} {
			emit(map[string]any{
				"sessionUpdate": "agent_message_chunk",
				"content":       map[string]any{"type": "text", "text": s},
			})
		}
		return "end_turn", nil
	}

	tk := postTask(t, h, "claude", "greet")

	req, _ := http.NewRequest("GET", h.ts.URL+"/api/tasks/"+tk.ID+"/stream", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("sse: %v", err)
	}
	defer resp.Body.Close()

	frames := readSSE(t, resp.Body, 5*time.Second)
	sawEvent := false
	sawDone := false
	for _, f := range frames {
		switch f.event {
		case "event":
			sawEvent = true
		case "status":
			var tk Task
			if json.Unmarshal(f.data, &tk) == nil && tk.Status == TaskCompleted {
				sawDone = true
			}
		}
	}
	if !sawEvent {
		t.Error("no 'event' SSE frames received")
	}
	if !sawDone {
		t.Error("did not see final completed status")
	}
}

func TestApprove_Forwards(t *testing.T) {
	h := newHarness(t, "")

	// Stage: agent pushes a permission request before replying.
	permResp := make(chan string, 1)
	h.agents["claude"].reverseRequest = jsonrpc.NewRequest(
		99, "session/request_permission",
		map[string]any{
			"toolCall": map[string]any{
				"toolCallId": "tc-1",
				"title":      "delete file",
				"kind":       "execute",
			},
			"options": []map[string]any{
				{"optionId": "allow", "name": "Allow", "kind": "allow_once"},
				{"optionId": "deny", "name": "Deny", "kind": "reject_once"},
			},
		},
	)
	h.agents["claude"].promptResponder = func(msg *jsonrpc.Message, emit func(map[string]any)) (string, error) {
		// The reverse-RPC fires before this returns, giving the test time to
		// POST /approve on the permission SSE it received.
		return "end_turn", nil
	}

	tk := postTask(t, h, "claude", "rm -rf")

	// Subscribe to SSE and wait for the permission frame.
	req, _ := http.NewRequest("GET", h.ts.URL+"/api/tasks/"+tk.ID+"/stream", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("sse: %v", err)
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	seenPermission := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !seenPermission {
		f, err := readOneSSE(reader)
		if err != nil {
			break
		}
		if f.event == "permission" {
			var p PermissionPrompt
			if json.Unmarshal(f.data, &p) == nil && p.ToolCallID == "tc-1" {
				// Approve via HTTP.
				go func() {
					ar := h.do(t, "POST", "/api/tasks/"+tk.ID+"/approve", "",
						map[string]string{"toolCallId": "tc-1", "optionId": "allow"})
					ar.Body.Close()
					permResp <- "done"
				}()
				seenPermission = true
			}
		}
	}
	if !seenPermission {
		t.Fatal("never saw permission SSE event")
	}
	select {
	case <-permResp:
	case <-time.After(3 * time.Second):
		t.Fatal("approve POST hung")
	}

	waitFor(t, func() bool {
		cur, _ := h.store.Get(tk.ID)
		return cur.Status == TaskCompleted
	}, 3*time.Second)
}

func TestApprove_NoPending(t *testing.T) {
	h := newHarness(t, "")
	tk := postTask(t, h, "claude", "hi") // ensure task exists
	resp := h.do(t, "POST", "/api/tasks/"+tk.ID+"/approve", "",
		map[string]string{"toolCallId": "ghost", "optionId": "allow"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", resp.StatusCode)
	}
}

// ---------- helpers ----------

func postTask(t *testing.T, h *harness, agent, prompt string) Task {
	t.Helper()
	resp := h.do(t, "POST", "/api/tasks", "", map[string]any{
		"agent": agent, "prompt": prompt, "cwd": "/tmp",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("post status = %d: %s", resp.StatusCode, b)
	}
	var tk Task
	if err := json.NewDecoder(resp.Body).Decode(&tk); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return tk
}

func waitFor(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("waitFor: condition never became true")
}

type sseFrame struct {
	event string
	data  []byte
}

func readSSE(t *testing.T, body io.Reader, timeout time.Duration) []sseFrame {
	t.Helper()
	r := bufio.NewReader(body)
	var out []sseFrame
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		f, err := readOneSSE(r)
		if err != nil {
			break
		}
		out = append(out, f)
		if f.event == "status" {
			var tk Task
			if json.Unmarshal(f.data, &tk) == nil &&
				(tk.Status == TaskCompleted || tk.Status == TaskFailed) {
				break
			}
		}
	}
	return out
}

func readOneSSE(r *bufio.Reader) (sseFrame, error) {
	var f sseFrame
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return f, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if f.event != "" || len(f.data) > 0 {
				return f, nil
			}
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			f.event = line[len("event: "):]
		} else if strings.HasPrefix(line, "data: ") {
			f.data = []byte(line[len("data: "):])
		}
	}
}
