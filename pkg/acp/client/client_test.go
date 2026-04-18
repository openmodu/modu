package client

import (
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openmodu/modu/pkg/acp/jsonrpc"
)

// fakeTransport is an in-memory stand-in for process.Process. It models
// two pipes: lines (agent → client) and writes (client → agent). Tests
// drive the "agent" side with agentRecv / agentSend.
type fakeTransport struct {
	lines  chan []byte
	done   chan struct{}
	writes chan []byte

	mu      sync.Mutex
	stopped bool
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{
		lines:  make(chan []byte, 64),
		done:   make(chan struct{}),
		writes: make(chan []byte, 64),
	}
}

func (f *fakeTransport) Start() error { return nil }

func (f *fakeTransport) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopped {
		return nil
	}
	f.stopped = true
	close(f.lines)
	close(f.done)
	return nil
}

func (f *fakeTransport) Write(line []byte) error {
	cp := make([]byte, len(line))
	copy(cp, line)
	f.writes <- cp
	return nil
}

func (f *fakeTransport) Lines() <-chan []byte    { return f.lines }
func (f *fakeTransport) Done() <-chan struct{}   { return f.done }

// agentSend delivers a line to the client as if it came from the agent.
func (f *fakeTransport) agentSend(line []byte) {
	f.mu.Lock()
	if f.stopped {
		f.mu.Unlock()
		return
	}
	f.mu.Unlock()
	f.lines <- line
}

// agentRecv waits for the client to write a frame.
func (f *fakeTransport) agentRecv(t *testing.T) *jsonrpc.Message {
	t.Helper()
	select {
	case raw := <-f.writes:
		var m jsonrpc.Message
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("agentRecv: unmarshal %q: %v", raw, err)
		}
		return &m
	case <-time.After(2 * time.Second):
		t.Fatal("agentRecv: timeout waiting for client write")
		return nil
	}
}

func mustStart(t *testing.T, c *Client) {
	t.Helper()
	if err := c.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
}

func TestRequest_Response(t *testing.T) {
	tx := newFakeTransport()
	c := New(Config{Transport: tx})
	mustStart(t, c)
	t.Cleanup(func() { _ = c.Stop() })

	go func() {
		m := tx.agentRecv(t)
		resp := jsonrpc.NewResponse(*m.ID, map[string]string{"echo": "hi"})
		b, _ := json.Marshal(resp)
		tx.agentSend(b)
	}()

	msg, err := c.Request("ping", map[string]any{"n": 1})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	var got map[string]string
	if err := msg.ParseResult(&got); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got["echo"] != "hi" {
		t.Errorf("got %v", got)
	}
}

func TestRequest_ErrorResponse(t *testing.T) {
	tx := newFakeTransport()
	c := New(Config{Transport: tx})
	mustStart(t, c)
	t.Cleanup(func() { _ = c.Stop() })

	go func() {
		m := tx.agentRecv(t)
		resp := jsonrpc.NewErrorResponse(*m.ID, jsonrpc.MethodNotFound, "nope")
		b, _ := json.Marshal(resp)
		tx.agentSend(b)
	}()

	_, err := c.Request("bad", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var rpcErr *jsonrpc.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("want *jsonrpc.Error, got %T", err)
	}
	if rpcErr.Code != jsonrpc.MethodNotFound {
		t.Errorf("code = %d", rpcErr.Code)
	}
}

func TestRequest_ConcurrentIDs(t *testing.T) {
	tx := newFakeTransport()
	c := New(Config{Transport: tx})
	mustStart(t, c)
	t.Cleanup(func() { _ = c.Stop() })

	const N = 10
	// Fake agent: for every inbound request, echo the ID back in the result.
	go func() {
		for i := 0; i < N; i++ {
			m := tx.agentRecv(t)
			resp := jsonrpc.NewResponse(*m.ID, map[string]int{"id": *m.ID})
			b, _ := json.Marshal(resp)
			tx.agentSend(b)
		}
	}()

	var wg sync.WaitGroup
	errs := make(chan error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			msg, err := c.Request("ping", map[string]int{"n": i})
			if err != nil {
				errs <- err
				return
			}
			var r map[string]int
			if err := msg.ParseResult(&r); err != nil {
				errs <- err
				return
			}
			if _, ok := r["id"]; !ok {
				errs <- errors.New("missing id in result")
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("request: %v", err)
	}
}

func TestNotification_Broadcast(t *testing.T) {
	tx := newFakeTransport()
	c := New(Config{Transport: tx})
	mustStart(t, c)
	t.Cleanup(func() { _ = c.Stop() })

	var a, b atomic.Int32
	done := make(chan struct{}, 2)
	c.OnNotification(func(m *jsonrpc.Message) { a.Add(1); done <- struct{}{} })
	c.OnNotification(func(m *jsonrpc.Message) { b.Add(1); done <- struct{}{} })

	tx.agentSend([]byte(`{"jsonrpc":"2.0","method":"bell"}`))
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("timeout; a=%d b=%d", a.Load(), b.Load())
		}
	}
	if a.Load() != 1 || b.Load() != 1 {
		t.Errorf("a=%d b=%d, want 1/1", a.Load(), b.Load())
	}
}

func TestNotification_CleanupRemovesSubscriber(t *testing.T) {
	tx := newFakeTransport()
	c := New(Config{Transport: tx})
	mustStart(t, c)
	t.Cleanup(func() { _ = c.Stop() })

	hits := make(chan struct{}, 4)
	cleanup := c.OnNotification(func(m *jsonrpc.Message) { hits <- struct{}{} })

	tx.agentSend([]byte(`{"jsonrpc":"2.0","method":"bell"}`))
	select {
	case <-hits:
	case <-time.After(time.Second):
		t.Fatal("first notification not delivered")
	}

	cleanup()
	tx.agentSend([]byte(`{"jsonrpc":"2.0","method":"bell"}`))
	select {
	case <-hits:
		t.Fatal("notification arrived after cleanup")
	case <-time.After(200 * time.Millisecond):
		// expected — subscriber was removed
	}
}

func TestReversePermission_Selected(t *testing.T) {
	tx := newFakeTransport()
	c := New(Config{
		Transport: tx,
		OnPermission: func(req *PermissionRequest) string {
			// Pick the first allow-style option.
			for _, opt := range req.Options {
				if opt.OptionID == "allow_once" {
					return opt.OptionID
				}
			}
			return req.Options[0].OptionID
		},
	})
	mustStart(t, c)
	t.Cleanup(func() { _ = c.Stop() })

	// Agent sends a permission request.
	permReq := jsonrpc.NewRequest(99, "session/request_permission", map[string]any{
		"sessionId": "s1",
		"toolCall": map[string]any{
			"toolCallId": "tc-1",
			"title":      "write file",
			"kind":       "edit",
		},
		"options": []map[string]string{
			{"optionId": "allow_once", "name": "Allow", "kind": "allow"},
			{"optionId": "reject_once", "name": "Reject", "kind": "reject"},
		},
	})
	b, _ := json.Marshal(permReq)
	tx.agentSend(b)

	// Client should respond.
	resp := tx.agentRecv(t)
	if !resp.IsResponse() {
		t.Fatalf("expected response, got method=%q", resp.Method)
	}
	if *resp.ID != 99 {
		t.Errorf("id = %d", *resp.ID)
	}
	var r struct {
		Outcome struct {
			Outcome  string `json:"outcome"`
			OptionID string `json:"optionId"`
		} `json:"outcome"`
	}
	if err := resp.ParseResult(&r); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if r.Outcome.Outcome != "selected" || r.Outcome.OptionID != "allow_once" {
		t.Errorf("got outcome=%+v", r.Outcome)
	}
}

func TestReversePermission_Rejected(t *testing.T) {
	tx := newFakeTransport()
	c := New(Config{
		Transport:    tx,
		OnPermission: func(req *PermissionRequest) string { return "reject_once" },
	})
	mustStart(t, c)
	t.Cleanup(func() { _ = c.Stop() })

	req := jsonrpc.NewRequest(1, "session/request_permission", map[string]any{
		"sessionId": "s",
		"toolCall":  map[string]any{"toolCallId": "x"},
		"options":   []map[string]string{{"optionId": "reject_once", "name": "No", "kind": "reject"}},
	})
	b, _ := json.Marshal(req)
	tx.agentSend(b)

	resp := tx.agentRecv(t)
	var r struct {
		Outcome struct {
			Outcome  string `json:"outcome"`
			OptionID string `json:"optionId"`
		} `json:"outcome"`
	}
	_ = resp.ParseResult(&r)
	if r.Outcome.Outcome != "rejected" {
		t.Errorf("outcome = %q, want rejected", r.Outcome.Outcome)
	}
}

type fakeFS struct {
	mu    sync.Mutex
	files map[string]string
}

func newFakeFS() *fakeFS { return &fakeFS{files: map[string]string{}} }

func (f *fakeFS) ReadTextFile(path string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.files[path]
	if !ok {
		return "", errors.New("not found")
	}
	return s, nil
}

func (f *fakeFS) WriteTextFile(path, content string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[path] = content
	return nil
}

func TestReverseFS_ReadWrite(t *testing.T) {
	fs := newFakeFS()
	fs.files["/a.txt"] = "hello"

	tx := newFakeTransport()
	c := New(Config{Transport: tx, FS: fs})
	mustStart(t, c)
	t.Cleanup(func() { _ = c.Stop() })

	// Read.
	readReq := jsonrpc.NewRequest(1, "fs/read_text_file", map[string]string{"path": "/a.txt"})
	b, _ := json.Marshal(readReq)
	tx.agentSend(b)

	resp := tx.agentRecv(t)
	var rr map[string]string
	_ = resp.ParseResult(&rr)
	if rr["content"] != "hello" {
		t.Errorf("read content = %q", rr["content"])
	}

	// Write.
	writeReq := jsonrpc.NewRequest(2, "fs/write_text_file", map[string]string{"path": "/b.txt", "content": "world"})
	b, _ = json.Marshal(writeReq)
	tx.agentSend(b)

	resp = tx.agentRecv(t)
	if resp.Error != nil {
		t.Errorf("write returned error: %v", resp.Error)
	}
	if got, _ := fs.ReadTextFile("/b.txt"); got != "world" {
		t.Errorf("after write, got %q", got)
	}
}

func TestReverseFS_Denied_WhenNil(t *testing.T) {
	tx := newFakeTransport()
	c := New(Config{Transport: tx}) // FS intentionally nil
	mustStart(t, c)
	t.Cleanup(func() { _ = c.Stop() })

	readReq := jsonrpc.NewRequest(1, "fs/read_text_file", map[string]string{"path": "/a"})
	b, _ := json.Marshal(readReq)
	tx.agentSend(b)

	resp := tx.agentRecv(t)
	if resp.Error == nil {
		t.Fatal("expected error response when FS is nil")
	}
	if resp.Error.Code != jsonrpc.InternalError {
		t.Errorf("code = %d, want InternalError", resp.Error.Code)
	}
}

func TestReverse_UnknownMethod(t *testing.T) {
	tx := newFakeTransport()
	c := New(Config{Transport: tx})
	mustStart(t, c)
	t.Cleanup(func() { _ = c.Stop() })

	req := jsonrpc.NewRequest(7, "weird/method", nil)
	b, _ := json.Marshal(req)
	tx.agentSend(b)

	resp := tx.agentRecv(t)
	if resp.Error == nil {
		t.Fatal("expected MethodNotFound error")
	}
	if resp.Error.Code != jsonrpc.MethodNotFound {
		t.Errorf("code = %d", resp.Error.Code)
	}
}

func TestRequest_AfterStop_ReturnsError(t *testing.T) {
	tx := newFakeTransport()
	c := New(Config{Transport: tx})
	mustStart(t, c)
	_ = c.Stop()

	if _, err := c.Request("ping", nil); err == nil {
		t.Error("expected error when requesting after stop")
	}
	if err := c.Notify("bell", nil); err == nil {
		t.Error("expected error notifying after stop")
	}
}

func TestRequest_InflightUnblocksOnStop(t *testing.T) {
	tx := newFakeTransport()
	c := New(Config{Transport: tx})
	mustStart(t, c)

	errCh := make(chan error, 1)
	go func() {
		_, err := c.Request("slow", nil)
		errCh <- err
	}()

	// Let the request reach the wait on pending.
	tx.agentRecv(t)
	_ = c.Stop()

	select {
	case err := <-errCh:
		if err == nil {
			t.Error("expected error when stop happens mid-request")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight request never unblocked after Stop")
	}
}

// TestReverse_MalformedFrameIgnored ensures the read loop survives a non-JSON
// line. Without this, a chatty agent that prints banners would kill the loop.
func TestReverse_MalformedFrameIgnored(t *testing.T) {
	tx := newFakeTransport()
	c := New(Config{Transport: tx})
	mustStart(t, c)
	t.Cleanup(func() { _ = c.Stop() })

	tx.agentSend([]byte("this is not JSON"))
	// Follow with a valid notification — subscriber should still fire.
	got := make(chan struct{}, 1)
	c.OnNotification(func(m *jsonrpc.Message) { got <- struct{}{} })
	tx.agentSend([]byte(`{"jsonrpc":"2.0","method":"bell"}`))

	select {
	case <-got:
	case <-time.After(time.Second):
		t.Fatal("read loop died after malformed frame")
	}
}

// Sanity: notifications with numeric params still parse.
func TestNotification_NumericParams(t *testing.T) {
	tx := newFakeTransport()
	c := New(Config{Transport: tx})
	mustStart(t, c)
	t.Cleanup(func() { _ = c.Stop() })

	got := make(chan int, 1)
	c.OnNotification(func(m *jsonrpc.Message) {
		var p struct {
			N int `json:"n"`
		}
		_ = m.ParseParams(&p)
		got <- p.N
	})

	tx.agentSend([]byte(`{"jsonrpc":"2.0","method":"tick","params":{"n":` + strconv.Itoa(42) + `}}`))
	select {
	case n := <-got:
		if n != 42 {
			t.Errorf("got %d", n)
		}
	case <-time.After(time.Second):
		t.Fatal("notification not delivered")
	}
}
