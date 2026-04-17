// Package jsonrpc implements the minimal subset of JSON-RPC 2.0 needed to
// communicate with ACP (Agent Client Protocol) agents over LDJSON stdio.
//
// It is transport-agnostic: types here describe on-the-wire messages only.
// The actual reading/writing of framed JSON lines lives in pkg/acp/process.
package jsonrpc

import (
	"encoding/json"
	"fmt"
)

const Version = "2.0"

// Standard JSON-RPC 2.0 error codes.
const (
	ParseError     = -32700
	InvalidRequest = -32600
	MethodNotFound = -32601
	InvalidParams  = -32602
	InternalError  = -32603
)

// Request is an outgoing JSON-RPC request with a correlation ID.
type Request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// Response is a reply to a Request, carrying either Result or Error.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Notification is a one-way message with no ID (no response expected).
type Notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// Error describes a JSON-RPC error object.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Error implements the error interface. When Data carries a "details" string
// (a convention used by ACP agents), it is appended for easier debugging.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if m, ok := e.Data.(map[string]any); ok {
		if d, ok := m["details"].(string); ok && d != "" {
			return fmt.Sprintf("%s: %s", e.Message, d)
		}
	}
	return e.Message
}

// Message is a union type for any inbound JSON-RPC frame. It keeps Params and
// Result as RawMessage so callers can decide the concrete type later.
//
// Discrimination rules (from the JSON-RPC 2.0 spec):
//
//	request      : ID != nil && Method != ""
//	response     : ID != nil && Method == ""
//	notification : ID == nil && Method != ""
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

func (m *Message) IsRequest() bool      { return m.ID != nil && m.Method != "" }
func (m *Message) IsResponse() bool     { return m.ID != nil && m.Method == "" }
func (m *Message) IsNotification() bool { return m.ID == nil && m.Method != "" }

// ParseParams unmarshals Params into target. Returns nil if Params is empty.
func (m *Message) ParseParams(target any) error {
	if len(m.Params) == 0 {
		return nil
	}
	return json.Unmarshal(m.Params, target)
}

// ParseResult unmarshals Result into target. Returns nil if Result is empty.
func (m *Message) ParseResult(target any) error {
	if len(m.Result) == 0 {
		return nil
	}
	return json.Unmarshal(m.Result, target)
}

// NewRequest builds a Request with the given id/method/params.
func NewRequest(id int, method string, params any) *Request {
	return &Request{JSONRPC: Version, ID: id, Method: method, Params: params}
}

// NewNotification builds a Notification.
func NewNotification(method string, params any) *Notification {
	return &Notification{JSONRPC: Version, Method: method, Params: params}
}

// NewResponse builds a successful Response. A nil result is serialized as
// JSON null so the receiver always sees a "result" field per spec.
func NewResponse(id int, result any) *Response {
	var raw json.RawMessage
	if result == nil {
		raw = json.RawMessage("null")
	} else {
		b, err := json.Marshal(result)
		if err != nil {
			// Fallback: emit a JSON null rather than panic — callers should
			// have already validated their result shape.
			raw = json.RawMessage("null")
		} else {
			raw = b
		}
	}
	return &Response{JSONRPC: Version, ID: id, Result: raw}
}

// NewErrorResponse builds an error Response.
func NewErrorResponse(id int, code int, message string) *Response {
	return &Response{JSONRPC: Version, ID: id, Error: &Error{Code: code, Message: message}}
}
