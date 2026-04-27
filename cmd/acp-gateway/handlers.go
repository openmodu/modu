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
	mux.HandleFunc("GET /", s.handleIndex)

	// Agents + workspace
	mux.HandleFunc("GET /api/agents", s.handleListAgents)
	mux.HandleFunc("GET /api/workdir", s.handleGetWorkdir)
	mux.HandleFunc("GET /api/files", s.handleListFiles)
	mux.HandleFunc("GET /api/browse", s.handleBrowse)

	// Projects
	mux.HandleFunc("GET /api/projects", s.handleListProjects)
	mux.HandleFunc("POST /api/projects", s.handleCreateProject)
	mux.HandleFunc("GET /api/projects/{id}", s.handleGetProject)
	mux.HandleFunc("DELETE /api/projects/{id}", s.handleDeleteProject)
	mux.HandleFunc("GET /api/projects/{id}/files", s.handleProjectFiles)

	// Sessions
	mux.HandleFunc("GET /api/sessions", s.handleListSessions)
	mux.HandleFunc("POST /api/sessions", s.handleCreateSession)
	mux.HandleFunc("GET /api/sessions/{id}", s.handleGetSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.handleDeleteSession)
	mux.HandleFunc("POST /api/sessions/{id}/cancel", s.handleCancelSession)

	// Turns (within a session)
	mux.HandleFunc("POST /api/sessions/{id}/turns", s.handleAddTurn)
	mux.HandleFunc("GET /api/sessions/{id}/turns/{turnId}/stream", s.handleStreamTurn)
	mux.HandleFunc("POST /api/sessions/{id}/turns/{turnId}/approve", s.handleApproveTurn)

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

// ---------- files ----------

// FileEntry is one item returned by GET /api/files or /api/projects/{id}/files.
type FileEntry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	IsDir   bool      `json:"isDir"`
	Size    int64     `json:"size,omitempty"`
	ModTime time.Time `json:"modTime"`
}

func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	s.listFilesIn(w, r, s.workdir)
}

// handleBrowse lists any absolute path on the machine so remote clients can
// pick a project directory without knowing the filesystem layout in advance.
// Query params:
//
//	path — absolute path to list (default: user home directory)
//	dirs — if "true", return only directories (useful for project picker)
func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("path")
	if target == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			http.Error(w, "cannot determine home dir", http.StatusInternalServerError)
			return
		}
		target = home
	}
	if !filepath.IsAbs(target) {
		http.Error(w, "path must be absolute", http.StatusBadRequest)
		return
	}
	target = filepath.Clean(target)

	entries, err := os.ReadDir(target)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "not found", http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	dirsOnly := r.URL.Query().Get("dirs") == "true"
	files := make([]FileEntry, 0, len(entries))
	for _, e := range entries {
		if dirsOnly && !e.IsDir() {
			continue
		}
		// Skip hidden entries (dot-files) unless the caller explicitly
		// navigated into a hidden directory.
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		fe := FileEntry{
			Name:    e.Name(),
			Path:    filepath.ToSlash(filepath.Join(target, e.Name())),
			IsDir:   e.IsDir(),
			ModTime: info.ModTime(),
		}
		if !e.IsDir() {
			fe.Size = info.Size()
		}
		files = append(files, fe)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":   filepath.ToSlash(target),
		"parent": filepath.ToSlash(filepath.Dir(target)),
		"files":  files,
	})
}

func (s *Server) handleProjectFiles(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	proj, ok := s.store.GetProject(id)
	if !ok {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}
	s.listFilesIn(w, r, proj.Path)
}

func (s *Server) listFilesIn(w http.ResponseWriter, r *http.Request, root string) {
	rel := r.URL.Query().Get("path")
	target := filepath.Clean(filepath.Join(root, filepath.FromSlash(rel)))
	if !strings.HasPrefix(target, root) {
		http.Error(w, "path outside root", http.StatusBadRequest)
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
		entryRel, _ := filepath.Rel(root, filepath.Join(target, e.Name()))
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
	writeJSON(w, http.StatusOK, map[string]any{
		"root":  root,
		"path":  filepath.ToSlash(rel),
		"files": files,
	})
}

// ---------- projects ----------

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"projects": s.store.ListProjects()})
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	p, err := s.store.CreateProject(body.Name, body.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := s.store.GetProject(id)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.store.DeleteProject(id) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- sessions ----------

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("projectId")
	writeJSON(w, http.StatusOK, map[string]any{"sessions": s.store.ListSessions(projectID)})
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProjectID string `json:"projectId"`
		Agent     string `json:"agent"`
		Title     string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.ProjectID == "" || body.Agent == "" {
		http.Error(w, "projectId and agent are required", http.StatusBadRequest)
		return
	}
	if !slices.Contains(s.registry.List(), body.Agent) {
		http.Error(w, fmt.Sprintf("unknown agent %q", body.Agent), http.StatusBadRequest)
		return
	}
	sess, err := s.store.CreateSession(body.ProjectID, body.Agent, body.Title)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusCreated, sess)
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	detail, ok := s.store.GetSessionDetail(id)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.store.DeleteSession(id) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCancelSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.store.GetSession(id); !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	s.store.CancelSession(id)
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancel requested"})
}

// ---------- turns ----------

func (s *Server) handleAddTurn(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if _, ok := s.store.GetSession(sessionID); !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	var body struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	turn, err := s.store.AddTurn(sessionID, body.Prompt)
	if err != nil {
		if err.Error() == "session already has a running turn" {
			http.Error(w, err.Error(), http.StatusConflict)
		} else {
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
		return
	}
	writeJSON(w, http.StatusAccepted, turn)
}

func (s *Server) handleStreamTurn(w http.ResponseWriter, r *http.Request) {
	turnID := r.PathValue("turnId")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	ch, cancel, found := s.store.Subscribe(turnID)
	if !found {
		http.Error(w, "turn not found", http.StatusNotFound)
		return
	}
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

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

func (s *Server) handleApproveTurn(w http.ResponseWriter, r *http.Request) {
	turnID := r.PathValue("turnId")
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
	if !s.store.Approve(turnID, body.ToolCallID, body.OptionID) {
		http.Error(w, "no pending permission for this toolCallId", http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "approved"})
}

// ---------- helpers ----------

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
