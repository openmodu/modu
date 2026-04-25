package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/openmodu/modu/pkg/types"
)

//go:embed web/index.html
var webFS embed.FS

// buildRouter wires all HTTP routes + auth middleware.
func (s *Server) buildRouter() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /api/agents", s.handleListAgents)
	mux.HandleFunc("GET /api/workdir", s.handleGetWorkdir)
	mux.HandleFunc("GET /api/files", s.handleListFiles)
	mux.HandleFunc("POST /api/tasks", s.handlePostTask)
	mux.HandleFunc("GET /api/tasks/{id}", s.handleGetTask)
	mux.HandleFunc("GET /api/tasks/{id}/stream", s.handleStreamTask)
	mux.HandleFunc("POST /api/tasks/{id}/approve", s.handleApprove)
	mux.HandleFunc("GET /{$}", s.handleIndex)

	exempt := map[string]bool{"/healthz": true, "/": true}
	return authMiddleware(s.token, exempt, mux)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	b, err := fs.ReadFile(webFS, "web/index.html")
	if err != nil {
		http.Error(w, "web assets missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(b)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"agents": s.registry.List()})
}

func (s *Server) handleGetWorkdir(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"workdir": s.workdir})
}

// FileEntry is one item returned by GET /api/files.
type FileEntry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"` // relative to workdir
	IsDir   bool      `json:"isDir"`
	Size    int64     `json:"size,omitempty"`
	ModTime time.Time `json:"modTime"`
}

func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")

	// Resolve and jail to workdir.
	target := filepath.Join(s.workdir, filepath.FromSlash(rel))
	target = filepath.Clean(target)
	if !strings.HasPrefix(target, s.workdir) {
		http.Error(w, "path outside workdir", http.StatusBadRequest)
		return
	}

	entries, err := os.ReadDir(target)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "not found", http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	files := make([]FileEntry, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		entryRel, _ := filepath.Rel(s.workdir, filepath.Join(target, e.Name()))
		fe := FileEntry{
			Name:    e.Name(),
			Path:    filepath.ToSlash(entryRel),
			IsDir:   e.IsDir(),
			ModTime: info.ModTime(),
		}
		if !e.IsDir() {
			fe.Size = info.Size()
		}
		files = append(files, fe)
	}
	writeJSON(w, http.StatusOK, map[string]any{"workdir": s.workdir, "path": filepath.ToSlash(rel), "files": files})
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
	if !slices.Contains(s.registry.List(), body.Agent) {
		http.Error(w, fmt.Sprintf("unknown agent %q", body.Agent), http.StatusBadRequest)
		return
	}

	cwd := body.Cwd
	if cwd == "" {
		cwd = s.workdir
	}
	t, err := s.store.Publish(body.Agent, body.Prompt, cwd)
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

	// Subscribe already buffers past events; the first frame the client sees
	// is the task's initial status or whatever came before it joined.

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
