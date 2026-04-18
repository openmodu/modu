package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/openmodu/modu/pkg/types"
)

// buildRouter wires all HTTP routes + auth middleware.
func (s *Server) buildRouter() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /api/agents", s.handleListAgents)
	mux.HandleFunc("POST /api/tasks", s.handlePostTask)
	mux.HandleFunc("GET /api/tasks/{id}", s.handleGetTask)
	mux.HandleFunc("GET /api/tasks/{id}/stream", s.handleStreamTask)
	mux.HandleFunc("POST /api/tasks/{id}/approve", s.handleApprove)

	return authMiddleware(s.token, map[string]bool{"/healthz": true}, mux)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"agents": s.mgr.List()})
}

func (s *Server) handlePostTask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Agent  string `json:"agent"`
		Prompt string `json:"prompt"`
		Cwd    string `json:"cwd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.Agent == "" || body.Prompt == "" {
		http.Error(w, "agent and prompt are required", http.StatusBadRequest)
		return
	}
	known := false
	for _, id := range s.mgr.List() {
		if id == body.Agent {
			known = true
			break
		}
	}
	if !known {
		http.Error(w, fmt.Sprintf("unknown agent %q", body.Agent), http.StatusBadRequest)
		return
	}

	t, err := s.store.Publish(body.Agent, body.Prompt, body.Cwd)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusAccepted, t)
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t, ok := s.store.Get(id)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleStreamTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	ch, cancel, found := s.store.Subscribe(id)
	if !found {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Send an initial status frame so the client immediately knows where the
	// task stands, even if no further events have landed yet.
	if t, ok := s.store.Get(id); ok {
		writeSSE(w, flusher, "status", t)
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			writeSSE(w, flusher, ev.Type, ev.Data)
		}
	}
}

func (s *Server) handleApprove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		ToolCallID string `json:"toolCallId"`
		OptionID   string `json:"optionId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.ToolCallID == "" || body.OptionID == "" {
		http.Error(w, "toolCallId and optionId are required", http.StatusBadRequest)
		return
	}
	if !s.store.Approve(id, body.ToolCallID, body.OptionID) {
		http.Error(w, "no pending permission for this toolCallId", http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "approved"})
}

// assistantText concatenates all text blocks in an AssistantMessage.
func assistantText(msg *types.AssistantMessage) string {
	if msg == nil {
		return ""
	}
	var sb strings.Builder
	for _, b := range msg.Content {
		if tc, ok := b.(*types.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeSSE(w http.ResponseWriter, f http.Flusher, event string, data any) {
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	if event != "" {
		fmt.Fprintf(w, "event: %s\n", event)
	}
	fmt.Fprintf(w, "data: %s\n\n", b)
	f.Flush()
}
