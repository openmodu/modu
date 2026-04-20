// Command modu-code is a modu-powered ACP agent that speaks the JSON-RPC 2.0
// LDJSON stdio protocol used by Claude Code, Codex, and Gemini CLI. Add it
// to your acp.config.json to use it from acp-gateway:
//
//	{
//	  "id": "modu",
//	  "command": "modu-code",
//	  "env": {"MODU_CODE_PROVIDER": "anthropic"}
//	}
//
// Provider selection (first match wins):
//
//	MODU_CODE_PROVIDER=anthropic  + ANTHROPIC_API_KEY
//	MODU_CODE_PROVIDER=gemini     + GOOGLE_API_KEY
//	MODU_CODE_PROVIDER=openai     + OPENAI_API_KEY   (also any OpenAI-compatible endpoint)
//	Auto-detect from whichever API key env var is set.
//
// Model override: MODU_CODE_MODEL (defaults to provider default).
// OpenAI base URL override: OPENAI_BASE_URL.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/providers/anthropic"
	"github.com/openmodu/modu/pkg/providers/gemini"
	"github.com/openmodu/modu/pkg/providers/openai"
	"github.com/openmodu/modu/pkg/types"
)

// ─── wire ──────────────────────────────────────────────────────────────────

func buildProvider() (providers.Provider, string) {
	pname := os.Getenv("MODU_CODE_PROVIDER")
	model := os.Getenv("MODU_CODE_MODEL")

	switch pname {
	case "anthropic":
		key := os.Getenv("ANTHROPIC_API_KEY")
		if key == "" {
			log.Fatal("modu-code: ANTHROPIC_API_KEY is required for provider=anthropic")
		}
		return anthropic.New(key, model), model
	case "gemini":
		key := os.Getenv("GOOGLE_API_KEY")
		if key == "" {
			log.Fatal("modu-code: GOOGLE_API_KEY is required for provider=gemini")
		}
		p, err := gemini.New(context.Background(), key, "modu", model)
		if err != nil {
			log.Fatalf("modu-code: init gemini: %v", err)
		}
		if model == "" {
			model = gemini.DefaultModel
		}
		return p, model
	case "openai", "":
		key := os.Getenv("OPENAI_API_KEY")
		baseURL := os.Getenv("OPENAI_BASE_URL")
		// Auto-detect: try ANTHROPIC_API_KEY, then GOOGLE_API_KEY if no openai key.
		if key == "" && pname == "" {
			if k := os.Getenv("ANTHROPIC_API_KEY"); k != "" {
				return anthropic.New(k, model), model
			}
			if k := os.Getenv("GOOGLE_API_KEY"); k != "" {
				p, err := gemini.New(context.Background(), k, "modu", model)
				if err != nil {
					log.Fatalf("modu-code: init gemini: %v", err)
				}
				if model == "" {
					model = gemini.DefaultModel
				}
				return p, model
			}
			log.Fatal("modu-code: no API key found; set ANTHROPIC_API_KEY, GOOGLE_API_KEY, or OPENAI_API_KEY")
		}
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		if model == "" {
			model = "gpt-4o"
		}
		return openai.New("openai",
			openai.WithBaseURL(baseURL),
			openai.WithAPIKey(key),
		), model
	default:
		log.Fatalf("modu-code: unknown MODU_CODE_PROVIDER %q", pname)
		return nil, ""
	}
}

// ─── ACP server ────────────────────────────────────────────────────────────

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

type server struct {
	provider providers.Provider
	model    string

	outMu sync.Mutex
	out   *bufio.Writer

	sessCounter atomic.Int64
	msgCounter  atomic.Int64

	// sessions tracks per-session conversation history for multi-turn.
	mu       sync.Mutex
	sessions map[string][]providers.Message

	// reverse tracks outbound requests (currently unused for permission).
	revMu   sync.Mutex
	reverse map[int64]chan *rpcMsg
}

func newServer(p providers.Provider, model string) *server {
	return &server{
		provider: p,
		model:    model,
		out:      bufio.NewWriter(os.Stdout),
		sessions: make(map[string][]providers.Message),
		reverse:  make(map[int64]chan *rpcMsg),
	}
}

func (s *server) writeFrame(v any) {
	b, _ := json.Marshal(v)
	s.outMu.Lock()
	s.out.Write(b)
	s.out.WriteByte('\n')
	s.out.Flush()
	s.outMu.Unlock()
}

func (s *server) reply(id int64, result any) {
	raw, _ := json.Marshal(result)
	s.writeFrame(rpcMsg{JSONRPC: "2.0", ID: &id, Result: raw})
}

func (s *server) replyErr(id int64, code int, msg string) {
	s.writeFrame(rpcMsg{JSONRPC: "2.0", ID: &id, Error: &rpcError{Code: code, Message: msg}})
}

func (s *server) notify(method string, params any) {
	raw, _ := json.Marshal(params)
	s.writeFrame(rpcMsg{JSONRPC: "2.0", Method: method, Params: raw})
}

func (s *server) handle(msg *rpcMsg) {
	// Response to one of our outbound reverse requests.
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
		// Notification from client (e.g. session/cancel) — ignore for now.
		return
	}
	id := *msg.ID
	switch msg.Method {
	case "initialize":
		s.reply(id, map[string]any{
			"protocolVersion": 1,
			"capabilities":    map[string]any{},
			"serverInfo": map[string]any{
				"name":    "modu-code",
				"version": "0.1.0",
			},
		})
	case "session/new":
		n := s.sessCounter.Add(1)
		sessID := fmt.Sprintf("modu-sess-%d", n)
		s.mu.Lock()
		s.sessions[sessID] = nil
		s.mu.Unlock()
		s.reply(id, map[string]any{"sessionId": sessID})
	case "session/prompt":
		go s.handlePrompt(id, msg)
	default:
		s.replyErr(id, -32601, "method not found")
	}
}

func (s *server) handlePrompt(id int64, msg *rpcMsg) {
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

	s.mu.Lock()
	history := append(s.sessions[p.SessionID], providers.Message{
		Role:    providers.RoleUser,
		Content: promptText.String(),
	})
	s.mu.Unlock()

	req := &providers.ChatRequest{
		Model:    s.model,
		Messages: history,
	}
	es, err := s.provider.Stream(context.Background(), req)
	if err != nil {
		s.replyErr(id, -32603, err.Error())
		return
	}

	var reply strings.Builder
	for ev := range es.Events() {
		if ev.Type == types.EventTextDelta && ev.Delta != "" {
			reply.WriteString(ev.Delta)
			s.notify("session/update", map[string]any{
				"sessionId": p.SessionID,
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content": map[string]any{
						"type": "text",
						"text": ev.Delta,
					},
				},
			})
		}
	}

	_, err = es.Result()
	if err != nil {
		s.replyErr(id, -32603, err.Error())
		return
	}

	// Store assistant reply in session history.
	s.mu.Lock()
	s.sessions[p.SessionID] = append(history, providers.Message{
		Role:    providers.RoleAssistant,
		Content: reply.String(),
	})
	s.mu.Unlock()

	s.reply(id, map[string]any{"stopReason": "end_turn"})
}

func (s *server) run() {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var msg rpcMsg
		if err := json.Unmarshal(line, &msg); err != nil {
			fmt.Fprintf(os.Stderr, "modu-code: parse: %v\n", err)
			continue
		}
		go s.handle(&msg)
	}
	if err := sc.Err(); err != nil && err != io.EOF {
		fmt.Fprintf(os.Stderr, "modu-code: stdin: %v\n", err)
	}
}

// ─── main ──────────────────────────────────────────────────────────────────

func main() {
	log.SetFlags(0)
	log.SetPrefix("modu-code: ")
	p, model := buildProvider()
	newServer(p, model).run()
}
