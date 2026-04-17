package jsonrpc

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNewRequest_Marshal(t *testing.T) {
	req := NewRequest(7, "ping", map[string]any{"x": 1})
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	for _, want := range []string{`"jsonrpc":"2.0"`, `"id":7`, `"method":"ping"`, `"x":1`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in %s", want, s)
		}
	}
}

func TestNewNotification_Marshal_NoID(t *testing.T) {
	n := NewNotification("bell", map[string]any{"k": "v"})
	b, _ := json.Marshal(n)
	s := string(b)
	if strings.Contains(s, `"id"`) {
		t.Errorf("notification must not carry id, got %s", s)
	}
	if !strings.Contains(s, `"method":"bell"`) {
		t.Errorf("missing method: %s", s)
	}
}

func TestNewResponse_NilResult_IsJSONNull(t *testing.T) {
	r := NewResponse(1, nil)
	if string(r.Result) != "null" {
		t.Errorf("expected null, got %q", string(r.Result))
	}
	b, _ := json.Marshal(r)
	if !strings.Contains(string(b), `"result":null`) {
		t.Errorf("expected result:null in %s", string(b))
	}
}

func TestNewResponse_NonNilResult_Marshals(t *testing.T) {
	r := NewResponse(42, map[string]string{"ok": "yes"})
	b, _ := json.Marshal(r)
	s := string(b)
	if !strings.Contains(s, `"ok":"yes"`) {
		t.Errorf("result missing in %s", s)
	}
	if !strings.Contains(s, `"id":42`) {
		t.Errorf("id missing in %s", s)
	}
}

func TestNewErrorResponse_Shape(t *testing.T) {
	r := NewErrorResponse(3, MethodNotFound, "no such method")
	b, _ := json.Marshal(r)
	s := string(b)
	if !strings.Contains(s, `"code":-32601`) {
		t.Errorf("error code missing: %s", s)
	}
	if !strings.Contains(s, `"message":"no such method"`) {
		t.Errorf("error message missing: %s", s)
	}
	if strings.Contains(s, `"result"`) {
		t.Errorf("error response must not carry result: %s", s)
	}
}

func TestMessage_Judgement(t *testing.T) {
	cases := []struct {
		name                        string
		raw                         string
		isReq, isResp, isNotif      bool
	}{
		{"request", `{"jsonrpc":"2.0","id":1,"method":"m"}`, true, false, false},
		{"response_result", `{"jsonrpc":"2.0","id":1,"result":{"a":1}}`, false, true, false},
		{"response_error", `{"jsonrpc":"2.0","id":1,"error":{"code":-1,"message":"e"}}`, false, true, false},
		{"notification", `{"jsonrpc":"2.0","method":"m"}`, false, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var m Message
			if err := json.Unmarshal([]byte(c.raw), &m); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := m.IsRequest(); got != c.isReq {
				t.Errorf("IsRequest = %v, want %v", got, c.isReq)
			}
			if got := m.IsResponse(); got != c.isResp {
				t.Errorf("IsResponse = %v, want %v", got, c.isResp)
			}
			if got := m.IsNotification(); got != c.isNotif {
				t.Errorf("IsNotification = %v, want %v", got, c.isNotif)
			}
		})
	}
}

func TestMessage_ParseParams_Nil(t *testing.T) {
	var m Message
	var target map[string]any
	if err := m.ParseParams(&target); err != nil {
		t.Errorf("expected nil error for empty params, got %v", err)
	}
	if target != nil {
		t.Errorf("target should remain nil, got %v", target)
	}
}

func TestMessage_ParseParams_Valid(t *testing.T) {
	m := Message{Params: json.RawMessage(`{"k":"v"}`)}
	var got map[string]string
	if err := m.ParseParams(&got); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got["k"] != "v" {
		t.Errorf("got %v", got)
	}
}

func TestMessage_ParseResult_Nil(t *testing.T) {
	var m Message
	var target int
	if err := m.ParseResult(&target); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestMessage_ParseResult_Valid(t *testing.T) {
	m := Message{Result: json.RawMessage(`42`)}
	var got int
	if err := m.ParseResult(&got); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got != 42 {
		t.Errorf("got %d", got)
	}
}

func TestError_Error_WithDetails(t *testing.T) {
	e := &Error{Code: -32000, Message: "oops", Data: map[string]any{"details": "disk full"}}
	if got := e.Error(); got != "oops: disk full" {
		t.Errorf("got %q", got)
	}
}

func TestError_Error_WithoutDetails(t *testing.T) {
	e := &Error{Code: -32000, Message: "oops"}
	if got := e.Error(); got != "oops" {
		t.Errorf("got %q", got)
	}
}

func TestError_Error_Nil(t *testing.T) {
	var e *Error
	if got := e.Error(); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}
