package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/openmodu/modu/pkg/acp/manager"
	"github.com/openmodu/modu/pkg/types"
)

//go:embed web/index.html
var webFS embed.FS

// buildRouter wires all HTTP routes + auth middleware.
func (s *Server) buildRouter() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /", s.handleIndex)

	// Gateway info
	mux.HandleFunc("GET /api/info", s.handleInfo)
	mux.HandleFunc("GET /api/system", s.handleSystem)

	// Agents + workspace
	mux.HandleFunc("GET /api/agents", s.handleListAgents)
	mux.HandleFunc("POST /api/agents", s.handleAddAgent)
	mux.HandleFunc("GET /api/agents/{id}", s.handleGetAgent)
	mux.HandleFunc("PUT /api/agents/{id}", s.handleUpdateAgent)
	mux.HandleFunc("DELETE /api/agents/{id}", s.handleDeleteAgent)
	mux.HandleFunc("POST /api/agents/{id}/restart", s.handleRestartAgent)
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

	// Global turn list
	mux.HandleFunc("GET /api/turns", s.handleListTurns)

	// Turns (within a session)
	mux.HandleFunc("POST /api/sessions/{id}/turns", s.handleAddTurn)
	mux.HandleFunc("GET /api/sessions/{id}/turns/{turnId}/stream", s.handleStreamTurn)
	mux.HandleFunc("POST /api/sessions/{id}/turns/{turnId}/stream", s.handleStreamTurn) // EventSource workaround
	mux.HandleFunc("POST /api/sessions/{id}/turns/{turnId}/approve", s.handleApproveTurn)
	mux.HandleFunc("POST /api/sessions/{id}/turns/{turnId}/cancel", s.handleCancelTurn)
	mux.HandleFunc("GET /api/sessions/{id}/turns/{turnId}/events", s.handleTurnEvents)

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

// ---------- gateway info ----------

// GatewayInfo is returned by GET /api/info.
type GatewayInfo struct {
	Version     string  `json:"version"`
	StartTime   string  `json:"startTime"`
	UptimeSec   float64 `json:"uptimeSec"`
	Connections int64   `json:"connections"`
	Agents      int     `json:"agents"`
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, GatewayInfo{
		Version:     Version,
		StartTime:   s.startTime.UTC().Format(time.RFC3339),
		UptimeSec:   time.Since(s.startTime).Seconds(),
		Connections: s.connections.Load(),
		Agents:      len(s.registry.List()),
	})
}

// SystemInfo is returned by GET /api/system.
type SystemInfo struct {
	Goroutines int    `json:"goroutines"`
	HeapMB     uint64 `json:"heapMB"`
	AllocMB    uint64 `json:"allocMB"`
	DiskTotalGB float64 `json:"diskTotalGB"`
	DiskFreeGB  float64 `json:"diskFreeGB"`
	DiskUsedPct float64 `json:"diskUsedPct"`
}

func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	info := SystemInfo{
		Goroutines: runtime.NumGoroutine(),
		HeapMB:     ms.HeapSys / 1024 / 1024,
		AllocMB:    ms.Alloc / 1024 / 1024,
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(s.workdir, &stat); err == nil {
		total := float64(stat.Blocks) * float64(stat.Bsize)
		free := float64(stat.Bavail) * float64(stat.Bsize)
		info.DiskTotalGB = total / 1e9
		info.DiskFreeGB = free / 1e9
		if total > 0 {
			info.DiskUsedPct = (total - free) / total * 100
		}
	}
	writeJSON(w, http.StatusOK, info)
}

// ---------- agents ----------

// AgentDetail is returned by GET /api/agents and GET /api/agents/{id}.
type AgentDetail struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Command       string `json:"command"`
	Args          []string `json:"args,omitempty"`
	Status        string `json:"status"` // "running" | "idle" | "offline"
	ActiveTurns   int    `json:"activeTurns"`
	TotalSessions int    `json:"totalSessions"`
	TotalTurns    int    `json:"totalTurns"`
}

func (s *Server) agentDetail(id string) (AgentDetail, bool) {
	cfg := s.mgr.Config()
	var ac *manager.AgentConfig
	for i := range cfg.Agents {
		if cfg.Agents[i].ID == id {
			ac = &cfg.Agents[i]
			break
		}
	}
	if ac == nil {
		return AgentDetail{}, false
	}
	active, sessions, turns := s.store.AgentStats(id)
	status := "offline"
	if active > 0 {
		status = "running"
	} else if s.mgr.HasProcess(id) {
		status = "idle"
	}
	return AgentDetail{
		ID:            ac.ID,
		Name:          ac.Name,
		Command:       ac.Command,
		Args:          ac.Args,
		Status:        status,
		ActiveTurns:   active,
		TotalSessions: sessions,
		TotalTurns:    turns,
	}, true
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	ids := s.registry.List()
	details := make([]AgentDetail, 0, len(ids))
	for _, id := range ids {
		if d, ok := s.agentDetail(id); ok {
			details = append(details, d)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": details})
}

func (s *Server) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d, ok := s.agentDetail(id)
	if !ok {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleAddAgent(w http.ResponseWriter, r *http.Request) {
	var body manager.AgentConfig
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.ID == "" || body.Command == "" {
		http.Error(w, "id and command are required", http.StatusBadRequest)
		return
	}
	if err := s.mgr.AddAgent(body); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	s.registry.Register(newACPRunner(body.ID, s.mgr))
	_ = s.saveConfig()
	// Workers run under the server's lifetime context stored via cancel;
	// use a background context here since we don't have access to the
	// server's root ctx. Dynamic agents run until server shutdown.
	workerCtx := context.Background()
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		runWorker(workerCtx, body.ID, s.store, s.registry)
	}()
	d, _ := s.agentDetail(body.ID)
	writeJSON(w, http.StatusCreated, d)
}

func (s *Server) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body manager.AgentConfig
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	body.ID = id
	if err := s.mgr.UpdateAgent(id, body); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	_ = s.saveConfig()
	d, _ := s.agentDetail(id)
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.mgr.RemoveAgent(id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	s.registry.Unregister(id)
	_ = s.saveConfig()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRestartAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.agentDetail(id); !ok {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}
	s.mgr.RestartAgent(id)
	writeJSON(w, http.StatusOK, map[string]string{"status": "restarted"})
}

// ---------- global turn list ----------

func (s *Server) handleListTurns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 50
	fmt.Sscan(q.Get("limit"), &limit)
	turns := s.store.ListTurns(TurnFilter{
		Status:    q.Get("status"),
		Agent:     q.Get("agent"),
		SessionID: q.Get("sessionId"),
		Limit:     limit,
	})
	writeJSON(w, http.StatusOK, map[string]any{"turns": turns})
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

	s.connections.Add(1)
	defer s.connections.Add(-1)

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

func (s *Server) handleCancelTurn(w http.ResponseWriter, r *http.Request) {
	turnID := r.PathValue("turnId")
	if !s.store.CancelTurn(turnID) {
		http.Error(w, "turn not running or not found", http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancel requested"})
}

func (s *Server) handleTurnEvents(w http.ResponseWriter, r *http.Request) {
	turnID := r.PathValue("turnId")
	events, ok := s.store.Events(turnID)
	if !ok {
		http.Error(w, "turn not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
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
