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

	promptResponder func(msg *jsonrpc.Message, emit func(update map[string]any)) (string, error)
	reverseRequest  *jsonrpc.Request
	reversePending  map[int]chan *jsonrpc.Message
}

func newFakeAgent() *fakeAgent {
	return &fakeAgent{reversePending: map[int]chan *jsonrpc.Message{}}
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

func (t *fakeTransport) Write(line []byte) error {
	var msg jsonrpc.Message
	if err := json.Unmarshal(line, &msg); err != nil {
		return nil
	}
	go t.dispatch(&msg)
	return nil
}

func (t *fakeTransport) dispatch(msg *jsonrpc.Message) {
	switch {
	case msg.IsResponse():
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

func (t *fakeTransport) sendReverseRequest(sessionID string, req *jsonrpc.Request) *jsonrpc.Message {
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
	transports map[string]*fakeTransport
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

	store := NewStore(32, nil)
	mgr := manager.New(cfg, hooksFor(store))

	h := &harness{
		store:      store,
		agents:     agents,
		transports: map[string]*fakeTransport{},
	}
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

// postedTurn bundles the created turn with its session for URL construction.
type postedTurn struct {
	Turn
	SessionID string
}

// postTurn creates a project + session + turn in one shot (test helper).
func postTurn(t *testing.T, h *harness, agent, prompt string) postedTurn {
	t.Helper()

	// 1. Create project.
	resp := h.do(t, "POST", "/api/projects", "", map[string]any{
		"name": "test", "path": "/tmp",
	})
	var proj Project
	_ = json.NewDecoder(resp.Body).Decode(&proj)
	resp.Body.Close()

	// 2. Create session.
	resp = h.do(t, "POST", "/api/sessions", "", map[string]any{
		"projectId": proj.ID, "agent": agent,
	})
	var sess Session
	_ = json.NewDecoder(resp.Body).Decode(&sess)
	resp.Body.Close()

	// 3. Add turn.
	resp = h.do(t, "POST", "/api/sessions/"+sess.ID+"/turns", "", map[string]any{
		"prompt": prompt,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("add turn status = %d: %s", resp.StatusCode, b)
	}
	var turn Turn
	if err := json.NewDecoder(resp.Body).Decode(&turn); err != nil {
		t.Fatalf("decode turn: %v", err)
	}
	return postedTurn{Turn: turn, SessionID: sess.ID}
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
	resp := h.do(t, "GET", "/api/agents", "", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no-token status = %d, want 401", resp.StatusCode)
	}
	resp = h.do(t, "GET", "/api/agents", "wrong", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong-token status = %d, want 401", resp.StatusCode)
	}
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

func TestProject_CRUD(t *testing.T) {
	h := newHarness(t, "")

	// Create
	resp := h.do(t, "POST", "/api/projects", "", map[string]any{
		"name": "myproject", "path": "/tmp",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", resp.StatusCode)
	}
	var proj Project
	_ = json.NewDecoder(resp.Body).Decode(&proj)
	if proj.ID == "" || proj.Name != "myproject" {
		t.Errorf("project = %+v", proj)
	}

	// List
	resp2 := h.do(t, "GET", "/api/projects", "", nil)
	defer resp2.Body.Close()
	var list struct {
		Projects []Project `json:"projects"`
	}
	_ = json.NewDecoder(resp2.Body).Decode(&list)
	if len(list.Projects) != 1 {
		t.Errorf("projects count = %d", len(list.Projects))
	}

	// Delete
	resp3 := h.do(t, "DELETE", "/api/projects/"+proj.ID, "", nil)
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusNoContent {
		t.Errorf("delete status = %d", resp3.StatusCode)
	}
}

func TestSession_CreateAndGet(t *testing.T) {
	h := newHarness(t, "")
	h.agents["claude"].promptResponder = func(_ *jsonrpc.Message, _ func(map[string]any)) (string, error) {
		return "end_turn", nil
	}

	pt := postTurn(t, h, "claude", "hello")
	if pt.Turn.ID == "" {
		t.Fatal("empty turn id")
	}

	resp := h.do(t, "GET", "/api/sessions/"+pt.SessionID, "", nil)
	defer resp.Body.Close()
	var detail SessionDetail
	_ = json.NewDecoder(resp.Body).Decode(&detail)
	if detail.ID != pt.SessionID {
		t.Errorf("session id = %q", detail.ID)
	}
}

func TestAddTurn_Publishes(t *testing.T) {
	h := newHarness(t, "")
	h.agents["claude"].promptResponder = func(msg *jsonrpc.Message, emit func(map[string]any)) (string, error) {
		emit(map[string]any{
			"sessionUpdate": "agent_message_chunk",
			"content":       map[string]any{"type": "text", "text": "done"},
		})
		return "end_turn", nil
	}

	pt := postTurn(t, h, "claude", "hi")
	if pt.Turn.ID == "" {
		t.Error("empty id")
	}
	if pt.Turn.Agent != "claude" || pt.Turn.Prompt != "hi" {
		t.Errorf("turn = %+v", pt.Turn)
	}
}

func TestAddTurn_UnknownAgent(t *testing.T) {
	h := newHarness(t, "")

	// Create project first.
	resp := h.do(t, "POST", "/api/projects", "", map[string]any{"name": "p", "path": "/tmp"})
	var proj Project
	_ = json.NewDecoder(resp.Body).Decode(&proj)
	resp.Body.Close()

	// Try to create session with unknown agent.
	resp = h.do(t, "POST", "/api/sessions", "", map[string]any{
		"projectId": proj.ID, "agent": "ghost",
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestTurnStatus_CompletedAfterRun(t *testing.T) {
	h := newHarness(t, "")
	h.agents["claude"].promptResponder = func(msg *jsonrpc.Message, emit func(map[string]any)) (string, error) {
		emit(map[string]any{
			"sessionUpdate": "agent_message_chunk",
			"content":       map[string]any{"type": "text", "text": "42"},
		})
		return "end_turn", nil
	}

	pt := postTurn(t, h, "claude", "what?")
	waitFor(t, func() bool {
		cur, _ := h.store.GetTurn(pt.Turn.ID)
		return cur.Status == TurnCompleted
	}, 2*time.Second)

	resp := h.do(t, "GET", "/api/sessions/"+pt.SessionID, "", nil)
	defer resp.Body.Close()
	var detail SessionDetail
	_ = json.NewDecoder(resp.Body).Decode(&detail)
	if len(detail.Turns) == 0 {
		t.Fatal("no turns in detail")
	}
	got := detail.Turns[0]
	if got.Status != TurnCompleted {
		t.Errorf("status = %q", got.Status)
	}
	if got.Result != "42" {
		t.Errorf("result = %q", got.Result)
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

	pt := postTurn(t, h, "claude", "greet")
	streamURL := fmt.Sprintf("/api/sessions/%s/turns/%s/stream", pt.SessionID, pt.Turn.ID)

	req, _ := http.NewRequest("GET", h.ts.URL+streamURL, nil)
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
			var turn Turn
			if json.Unmarshal(f.data, &turn) == nil && turn.Status == TurnCompleted {
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
		return "end_turn", nil
	}

	pt := postTurn(t, h, "claude", "rm -rf")
	streamURL := fmt.Sprintf("/api/sessions/%s/turns/%s/stream", pt.SessionID, pt.Turn.ID)
	approveURL := fmt.Sprintf("/api/sessions/%s/turns/%s/approve", pt.SessionID, pt.Turn.ID)

	req, _ := http.NewRequest("GET", h.ts.URL+streamURL, nil)
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
				go func() {
					ar := h.do(t, "POST", approveURL, "",
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
		cur, _ := h.store.GetTurn(pt.Turn.ID)
		return cur.Status == TurnCompleted
	}, 3*time.Second)
}

func TestApprove_NoPending(t *testing.T) {
	h := newHarness(t, "")
	h.agents["claude"].promptResponder = func(_ *jsonrpc.Message, _ func(map[string]any)) (string, error) {
		return "end_turn", nil
	}
	pt := postTurn(t, h, "claude", "hi")
	approveURL := fmt.Sprintf("/api/sessions/%s/turns/%s/approve", pt.SessionID, pt.Turn.ID)
	resp := h.do(t, "POST", approveURL, "",
		map[string]string{"toolCallId": "ghost", "optionId": "allow"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", resp.StatusCode)
	}
}

// ---------- helpers ----------

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
			var turn Turn
			if json.Unmarshal(f.data, &turn) == nil &&
				(turn.Status == TurnCompleted || turn.Status == TurnFailed) {
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
