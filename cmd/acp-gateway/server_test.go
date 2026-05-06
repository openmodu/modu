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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openmodu/modu/pkg/acp/client"
	"github.com/openmodu/modu/pkg/acp/jsonrpc"
	"github.com/openmodu/modu/pkg/acp/manager"
	"github.com/openmodu/modu/pkg/tokenkit"
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
		Agents []AgentDetail `json:"agents"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Agents) != 2 {
		t.Errorf("agents = %v", body.Agents)
	}
	if body.Agents[0].ID != "claude" || body.Agents[1].ID != "codex" {
		t.Fatalf("agent order = %q, %q; want claude, codex", body.Agents[0].ID, body.Agents[1].ID)
	}
}

func TestAddAgent_WorkerStopsOnClose(t *testing.T) {
	h := newHarness(t, "")
	resp := h.do(t, "POST", "/api/agents", "", map[string]any{
		"id":      "dynamic",
		"name":    "dynamic",
		"command": "ignored",
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add agent status = %d, want 201", resp.StatusCode)
	}

	done := make(chan error, 1)
	go func() { done <- h.srv.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server close hung after adding dynamic agent")
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

func TestListProfiles_SortsByCreatedAt(t *testing.T) {
	store := NewStore(8, nil)
	first, err := store.CreateProfile("first", "claude", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateProfile("second", "claude", "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	store.profiles[first.ID].CreatedAt = time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	store.profiles[second.ID].CreatedAt = time.Date(2026, 5, 1, 11, 0, 0, 0, time.UTC)
	store.mu.Unlock()

	got := store.ListProfiles()
	if len(got) != 2 {
		t.Fatalf("profiles len = %d, want 2", len(got))
	}
	if got[0].ID != first.ID || got[1].ID != second.ID {
		t.Fatalf("profile order = %s, %s; want %s, %s", got[0].ID, got[1].ID, first.ID, second.ID)
	}
}

func TestListSessions_SortsByUpdatedAtDesc(t *testing.T) {
	store := NewStore(8, nil)
	proj, err := store.CreateProject("p", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	older, err := store.CreateSession(proj.ID, "claude", "older", "")
	if err != nil {
		t.Fatal(err)
	}
	newer, err := store.CreateSession(proj.ID, "claude", "newer", "")
	if err != nil {
		t.Fatal(err)
	}

	got := store.ListSessions(proj.ID)
	if len(got) != 2 {
		t.Fatalf("sessions len = %d, want 2", len(got))
	}
	if got[0].ID != newer.ID || got[1].ID != older.ID {
		t.Fatalf("session order = %s, %s; want %s, %s", got[0].ID, got[1].ID, newer.ID, older.ID)
	}

	turn, err := store.AddTurn(older.ID, "touch older")
	if err != nil {
		t.Fatal(err)
	}
	store.StartTurn(turn.ID, func() {})
	got = store.ListSessions(proj.ID)
	if got[0].ID != older.ID || got[1].ID != newer.ID {
		t.Fatalf("session order after update = %s, %s; want %s, %s", got[0].ID, got[1].ID, older.ID, newer.ID)
	}
}

func TestProjectFiles_RejectsSiblingPrefixEscape(t *testing.T) {
	h := newHarness(t, "")
	base := t.TempDir()
	root := filepath.Join(base, "root")
	sibling := filepath.Join(base, "root-evil")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(sibling, 0755); err != nil {
		t.Fatal(err)
	}

	resp := h.do(t, "POST", "/api/projects", "", map[string]any{"name": "p", "path": root})
	var proj Project
	_ = json.NewDecoder(resp.Body).Decode(&proj)
	resp.Body.Close()

	resp = h.do(t, "GET", "/api/projects/"+proj.ID+"/files?path=../root-evil", "", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestProjectFiles_RejectsSymlinkEscape(t *testing.T) {
	h := newHarness(t, "")
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}

	resp := h.do(t, "POST", "/api/projects", "", map[string]any{"name": "p", "path": root})
	var proj Project
	_ = json.NewDecoder(resp.Body).Decode(&proj)
	resp.Body.Close()

	resp = h.do(t, "GET", "/api/projects/"+proj.ID+"/files?path=link", "", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
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

func TestDeleteSession_CleansRunningTurnResources(t *testing.T) {
	store := NewStore(8, nil)
	proj, err := store.CreateProject("p", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.CreateSession(proj.ID, "claude", "", "")
	if err != nil {
		t.Fatal(err)
	}
	turn, err := store.AddTurn(sess.ID, "needs approval")
	if err != nil {
		t.Fatal(err)
	}

	cancelled := make(chan struct{})
	store.StartTurn(turn.ID, func() { close(cancelled) })
	sub, cancelSub, ok := store.Subscribe(turn.ID)
	if !ok {
		t.Fatal("subscribe failed")
	}
	defer cancelSub()

	permissionResult := make(chan string, 1)
	go func() {
		permissionResult <- store.AwaitPermission(context.Background(), turn.ID, PermissionPrompt{
			ToolCallID: "tc-1",
			Options: []client.PermissionOption{
				{OptionID: "deny", Kind: "reject_once"},
			},
		})
	}()
	waitFor(t, func() bool {
		events, _ := store.Events(turn.ID)
		return len(events) > 0 && events[len(events)-1].Type == "permission"
	}, time.Second)

	if !store.DeleteSession(sess.ID) {
		t.Fatal("delete session failed")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("running turn was not cancelled")
	}
	select {
	case <-time.After(time.Second):
		t.Fatal("subscription did not close")
	default:
		for {
			select {
			case _, ok := <-sub:
				if !ok {
					goto subscriptionClosed
				}
			case <-time.After(time.Second):
				t.Fatal("subscription did not close")
			}
		}
	}
subscriptionClosed:
	select {
	case got := <-permissionResult:
		if got != "deny" {
			t.Fatalf("permission result = %q, want deny", got)
		}
	case <-time.After(time.Second):
		t.Fatal("permission wait did not unblock")
	}
}

func TestCancelTurnsForAgent_CleansPendingAndRunningTurns(t *testing.T) {
	store := NewStore(8, nil)
	proj, err := store.CreateProject("p", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pendingSess, err := store.CreateSession(proj.ID, "claude", "", "")
	if err != nil {
		t.Fatal(err)
	}
	pendingTurn, err := store.AddTurn(pendingSess.ID, "pending")
	if err != nil {
		t.Fatal(err)
	}
	runningSess, err := store.CreateSession(proj.ID, "claude", "", "")
	if err != nil {
		t.Fatal(err)
	}
	runningTurn, err := store.AddTurn(runningSess.ID, "running")
	if err != nil {
		t.Fatal(err)
	}

	cancelled := make(chan struct{})
	store.StartTurn(runningTurn.ID, func() { close(cancelled) })
	permissionResult := make(chan string, 1)
	go func() {
		permissionResult <- store.AwaitPermission(context.Background(), runningTurn.ID, PermissionPrompt{
			ToolCallID: "tc-1",
			Options: []client.PermissionOption{
				{OptionID: "deny", Kind: "reject_once"},
			},
		})
	}()
	waitFor(t, func() bool {
		events, _ := store.Events(runningTurn.ID)
		return len(events) > 0 && events[len(events)-1].Type == "permission"
	}, time.Second)

	if got := store.CancelTurnsForAgent("claude", "agent deleted"); got != 2 {
		t.Fatalf("cancelled turns = %d, want 2", got)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("running turn was not cancelled")
	}
	select {
	case got := <-permissionResult:
		if got != "deny" {
			t.Fatalf("permission result = %q, want deny", got)
		}
	case <-time.After(time.Second):
		t.Fatal("permission wait did not unblock")
	}
	for _, id := range []string{pendingTurn.ID, runningTurn.ID} {
		cur, ok := store.GetTurn(id)
		if !ok {
			t.Fatalf("turn %s missing", id)
		}
		if cur.Status != TurnFailed || cur.Error != "agent deleted" {
			t.Fatalf("turn %s = status %q error %q, want failed agent deleted", id, cur.Status, cur.Error)
		}
		store.CompleteTurn(id, "late result", nil, 1)
		cur, _ = store.GetTurn(id)
		if cur.Status != TurnFailed {
			t.Fatalf("turn %s was overwritten after cancellation: %q", id, cur.Status)
		}
	}
}

func TestAwaitPermission_ContextCancelRejectsAndCleansPending(t *testing.T) {
	store := NewStore(8, nil)
	proj, err := store.CreateProject("p", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.CreateSession(proj.ID, "claude", "", "")
	if err != nil {
		t.Fatal(err)
	}
	turn, err := store.AddTurn(sess.ID, "permission")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan string, 1)
	go func() {
		result <- store.AwaitPermission(ctx, turn.ID, PermissionPrompt{
			ToolCallID: "tc-1",
			Options: []client.PermissionOption{
				{OptionID: "allow", Kind: "allow_once"},
				{OptionID: "deny", Kind: "reject_once"},
			},
		})
	}()
	waitFor(t, func() bool {
		events, _ := store.Events(turn.ID)
		return len(events) > 0 && events[len(events)-1].Type == "permission"
	}, time.Second)

	cancel()
	select {
	case got := <-result:
		if got != "deny" {
			t.Fatalf("permission result = %q, want deny", got)
		}
	case <-time.After(time.Second):
		t.Fatal("permission wait did not unblock after context cancel")
	}
	if store.Approve(turn.ID, "tc-1", "allow") {
		t.Fatal("approve succeeded after permission context was cancelled")
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

func TestAddTurn_AgentDeleted(t *testing.T) {
	h := newHarness(t, "")

	resp := h.do(t, "POST", "/api/projects", "", map[string]any{"name": "p", "path": t.TempDir()})
	var proj Project
	_ = json.NewDecoder(resp.Body).Decode(&proj)
	resp.Body.Close()

	resp = h.do(t, "POST", "/api/sessions", "", map[string]any{
		"projectId": proj.ID,
		"agent":     "claude",
	})
	var sess Session
	_ = json.NewDecoder(resp.Body).Decode(&sess)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create session status = %d", resp.StatusCode)
	}

	resp = h.do(t, "DELETE", "/api/agents/claude", "", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete agent status = %d", resp.StatusCode)
	}

	resp = h.do(t, "POST", "/api/sessions/"+sess.ID+"/turns", "", map[string]any{"prompt": "hi"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("add turn status = %d, want 400", resp.StatusCode)
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

func TestTokenkitAPIRecordsTotalsAndStatus(t *testing.T) {
	h := newHarness(t, "")
	tk, err := tokenkit.OpenStore(filepath.Join(t.TempDir(), "tokenkit.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tk.Close() })
	h.srv.tokenkit = tk

	proj, err := h.store.CreateProject("repo", "/repo")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := h.store.CreateSession(proj.ID, "codex", "codex work", "")
	if err != nil {
		t.Fatal(err)
	}

	startedAt := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)
	if err := tk.UpsertUsageRecord(context.Background(), tokenkit.UsageRecord{
		Source:            "codex:cli",
		App:               tokenkit.AppCodex,
		ExternalID:        "codex-test-1",
		StartedAt:         startedAt,
		LocalDate:         "2026-05-04",
		MeasurementMethod: tokenkit.MethodExact,
		Model:             "gpt-5.5",
		InputTokens:       100,
		OutputTokens:      7,
		TotalTokens:       107,
		Workspace:         "/repo",
		Metadata:          map[string]any{"session_id": sess.ID},
	}); err != nil {
		t.Fatal(err)
	}

	resp := h.do(t, "GET", "/api/tokenkit/totals?app=codex&start=2026-05-04&end=2026-05-04", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("totals status = %d", resp.StatusCode)
	}
	var totals tokenkit.SummaryRow
	if err := json.NewDecoder(resp.Body).Decode(&totals); err != nil {
		t.Fatal(err)
	}
	if totals.Records != 1 || totals.TotalTokens != 107 || totals.InputTokens != 100 {
		t.Fatalf("unexpected totals: %+v", totals)
	}

	resp = h.do(t, "GET", "/api/tokenkit/records?app=codex&limit=10", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("records status = %d", resp.StatusCode)
	}
	var recordsBody struct {
		Records []tokenkit.UsageRecord `json:"records"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&recordsBody); err != nil {
		t.Fatal(err)
	}
	if len(recordsBody.Records) != 1 || recordsBody.Records[0].ExternalID != "codex-test-1" {
		t.Fatalf("unexpected records: %+v", recordsBody.Records)
	}

	resp = h.do(t, "GET", "/api/tokenkit/overview?start=2026-05-04&end=2026-05-04", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("overview status = %d", resp.StatusCode)
	}
	var overviewBody struct {
		Totals   map[string]tokenkit.SummaryRow `json:"totals"`
		Projects []TokenkitScopedUsage          `json:"projects"`
		Sessions []TokenkitScopedUsage          `json:"sessions"`
		Timeline []tokenkit.TimeBucketRow       `json:"timeline"`
		Records  []tokenkit.UsageRecord         `json:"records"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&overviewBody); err != nil {
		t.Fatal(err)
	}
	if overviewBody.Totals[tokenkit.AppCodex].TotalTokens != 107 || len(overviewBody.Records) != 1 {
		t.Fatalf("unexpected overview: %+v", overviewBody)
	}
	if len(overviewBody.Projects) != 1 || overviewBody.Projects[0].Totals.TotalTokens != 107 {
		t.Fatalf("unexpected overview projects: %+v", overviewBody.Projects)
	}
	if len(overviewBody.Sessions) != 1 || overviewBody.Sessions[0].Totals.TotalTokens != 107 || overviewBody.Sessions[0].Match != "session-id" {
		t.Fatalf("unexpected overview sessions: %+v", overviewBody.Sessions)
	}
	if len(overviewBody.Timeline) != 1 || overviewBody.Timeline[0].LocalDate != "2026-05-04" || overviewBody.Timeline[0].TotalTokens != 107 {
		t.Fatalf("unexpected overview timeline: %+v", overviewBody.Timeline)
	}

	resp = h.do(t, "GET", "/api/tokenkit/timeline?app=codex&start=2026-05-04&end=2026-05-04", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("timeline status = %d", resp.StatusCode)
	}
	var timelineBody struct {
		Timeline []tokenkit.TimeBucketRow `json:"timeline"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&timelineBody); err != nil {
		t.Fatal(err)
	}
	if len(timelineBody.Timeline) != 1 || timelineBody.Timeline[0].TotalTokens != 107 {
		t.Fatalf("unexpected timeline: %+v", timelineBody.Timeline)
	}

	if err := tk.UpsertUsageRecord(context.Background(), tokenkit.UsageRecord{
		Source:            "codex:cli",
		App:               tokenkit.AppCodex,
		ExternalID:        "codex-old",
		StartedAt:         time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC),
		LocalDate:         "2026-04-20",
		MeasurementMethod: tokenkit.MethodExact,
		Model:             "gpt-5.5",
		InputTokens:       40,
		OutputTokens:      10,
		TotalTokens:       50,
		Workspace:         "/repo",
	}); err != nil {
		t.Fatal(err)
	}
	oldTokenkitNow := tokenkitNow
	tokenkitNow = func() time.Time { return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { tokenkitNow = oldTokenkitNow })

	resp = h.do(t, "GET", "/api/tokenkit/overview?days=7", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("overview days=7 status = %d", resp.StatusCode)
	}
	var weekOverview struct {
		Totals   map[string]tokenkit.SummaryRow `json:"totals"`
		Timeline []tokenkit.TimeBucketRow       `json:"timeline"`
		Records  []tokenkit.UsageRecord         `json:"records"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&weekOverview); err != nil {
		t.Fatal(err)
	}
	if weekOverview.Totals[tokenkit.AppCodex].TotalTokens != 107 || len(weekOverview.Timeline) != 1 || len(weekOverview.Records) != 1 {
		t.Fatalf("unexpected 7 day overview: %+v", weekOverview)
	}

	resp = h.do(t, "GET", "/api/tokenkit/overview?days=30", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("overview days=30 status = %d", resp.StatusCode)
	}
	var monthOverview struct {
		Totals   map[string]tokenkit.SummaryRow `json:"totals"`
		Timeline []tokenkit.TimeBucketRow       `json:"timeline"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&monthOverview); err != nil {
		t.Fatal(err)
	}
	if monthOverview.Totals[tokenkit.AppCodex].TotalTokens != 157 || len(monthOverview.Timeline) != 2 {
		t.Fatalf("unexpected 30 day overview: %+v", monthOverview)
	}

	rawStatus := `>_ OpenAI Codex (v0.128.0)
│  Model:                       gpt-5.5 (reasoning medium, summaries auto)
│  Account:                     test@example.com (Pro Lite)
│  Session:                     sess-1
│  Context window:              66% left (95.1K used / 258K)
│  5h limit:                    [████████████████████] 99% left (resets 20:26)`
	resp = h.do(t, "POST", "/api/tokenkit/codex-status", "", map[string]string{"text": rawStatus})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("codex-status status = %d", resp.StatusCode)
	}

	resp = h.do(t, "GET", "/api/tokenkit/codex-status/latest", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("latest status = %d", resp.StatusCode)
	}
	var latestBody struct {
		Status *tokenkit.CodexStatusSnapshot `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&latestBody); err != nil {
		t.Fatal(err)
	}
	if latestBody.Status == nil || latestBody.Status.AccountEmail != "test@example.com" || latestBody.Status.ContextWindow.UsedTokens != 95100 {
		t.Fatalf("unexpected latest status: %+v", latestBody.Status)
	}
}

func TestTokenkitOverviewAssignsWorkspaceAgentSessionFallback(t *testing.T) {
	h := newHarness(t, "", "codex")
	tk, err := tokenkit.OpenStore(filepath.Join(t.TempDir(), "tokenkit.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tk.Close() })
	h.srv.tokenkit = tk

	proj, err := h.store.CreateProject("repo", "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.CreateSession(proj.ID, "codex", "codex work", ""); err != nil {
		t.Fatal(err)
	}
	if err := tk.UpsertUsageRecord(context.Background(), tokenkit.UsageRecord{
		Source:            "codex:cli",
		App:               tokenkit.AppCodex,
		ExternalID:        "codex-native-session:2026-05-04T10:00:00Z",
		StartedAt:         time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC),
		LocalDate:         "2026-05-04",
		MeasurementMethod: tokenkit.MethodExact,
		InputTokens:       10,
		OutputTokens:      5,
		TotalTokens:       15,
		Workspace:         "/repo",
		Metadata:          map[string]any{"session_id": "codex-native-session"},
	}); err != nil {
		t.Fatal(err)
	}

	resp := h.do(t, "GET", "/api/tokenkit/overview", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("overview status = %d", resp.StatusCode)
	}
	var body struct {
		Sessions []TokenkitScopedUsage `json:"sessions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Sessions) != 1 || body.Sessions[0].Totals.TotalTokens != 15 || body.Sessions[0].Match != "workspace-agent" {
		t.Fatalf("unexpected sessions: %+v", body.Sessions)
	}
}

func TestTokenkitManualScanUpdatesSyncStatus(t *testing.T) {
	h := newHarness(t, "")
	tk, err := tokenkit.OpenStore(filepath.Join(t.TempDir(), "tokenkit.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tk.Close() })
	h.srv.tokenkit = tk

	codexHome := writeTokenkitCodexFixture(t)
	resp := h.do(t, "POST", "/api/tokenkit/scan?target=codex&codexHome="+codexHome+"&timezone=UTC", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("scan status = %d", resp.StatusCode)
	}

	resp = h.do(t, "GET", "/api/tokenkit/sync", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sync status = %d", resp.StatusCode)
	}
	var status TokenkitSyncStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.Running || status.LastFinishedAt == "" || status.LastStats[tokenkit.AppCodex].RecordsSeen != 1 {
		t.Fatalf("unexpected sync status: %+v", status)
	}
}

func TestTokenkitAutoSyncScansConfiguredHomes(t *testing.T) {
	codexHome := writeTokenkitCodexFixture(t)
	claudeHome := t.TempDir()
	geminiLog := filepath.Join(t.TempDir(), "missing-gemini.log")
	tk, err := tokenkit.OpenStore(filepath.Join(t.TempDir(), "tokenkit.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tk.Close() })

	cfg := &manager.Config{}
	store := NewStore(8, nil)
	mgr := manager.New(cfg, hooksFor(store))
	srv := NewServer(Options{
		Manager:  mgr,
		Store:    store,
		Tokenkit: tk,
		TokenkitScannerOptions: tokenkit.ScannerOptions{
			CodexHome:          codexHome,
			ClaudeHome:         claudeHome,
			GeminiTelemetryLog: geminiLog,
			Location:           time.UTC,
		},
		TokenkitScanInterval: 20 * time.Millisecond,
	})
	t.Cleanup(func() { _ = srv.Close() })

	waitFor(t, func() bool {
		totals, err := tk.Totals(context.Background(), tokenkit.SummaryFilter{App: tokenkit.AppCodex})
		return err == nil && totals.Records == 1 && totals.TotalTokens == 107
	}, 2*time.Second)

	status := srv.currentTokenkitSyncStatus()
	if !status.Enabled || status.LastFinishedAt == "" || status.LastStats[tokenkit.AppCodex].RecordsSeen != 1 {
		t.Fatalf("unexpected auto sync status: %+v", status)
	}
}

// ---------- helpers ----------

func writeTokenkitCodexFixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	sessionFile := filepath.Join(home, "sessions", "2026", "05", "04", "rollout.jsonl")
	if err := os.MkdirAll(filepath.Dir(sessionFile), 0o755); err != nil {
		t.Fatal(err)
	}
	lines := `{"type":"session_meta","payload":{"id":"sess-1","source":"cli","cwd":"/repo"}}
{"type":"turn_context","payload":{"model":"gpt-5.5"}}
{"timestamp":"2026-05-04T10:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":100,"output_tokens":7,"total_tokens":107}}}}
`
	if err := os.WriteFile(sessionFile, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
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
