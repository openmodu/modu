package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/openmodu/modu/pkg/mailbox"
)

// AgentTeamsServer wraps a mailbox.Hub with a full HTTP API + SSE + embedded SPA.
type AgentTeamsServer struct {
	hub     *mailbox.Hub
	cfg     ContentConfig
	mu      sync.Mutex
	clients map[chan string]struct{}
}

func NewAgentTeamsServer(hub *mailbox.Hub, cfg ContentConfig) *AgentTeamsServer {
	return &AgentTeamsServer{
		hub:     hub,
		cfg:     cfg,
		clients: make(map[chan string]struct{}),
	}
}

func (s *AgentTeamsServer) Start(ctx context.Context, addr string) error {
	// Fan out hub events → SSE clients
	sub := s.hub.Subscribe()
	go func() {
		for {
			select {
			case <-ctx.Done():
				s.hub.Unsubscribe(sub)
				return
			case e, ok := <-sub:
				if !ok {
					return
				}
				b, _ := json.Marshal(e)
				s.broadcast(string(e.Type), string(b))
			}
		}
	}()

	// Heartbeat loop for seeded agents so they are never evicted
	go func() {
		tick := time.NewTicker(10 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				for _, id := range s.hub.ListAgents() {
					_ = s.hub.Heartbeat(id)
				}
			}
		}
	}()

	mux := http.NewServeMux()

	// SPA
	mux.HandleFunc("/", s.handleIndex)

	// Agents
	mux.HandleFunc("/api/agents", s.handleAgents)
	mux.HandleFunc("/api/agents/", s.handleAgent)

	// Projects
	mux.HandleFunc("/api/projects", s.handleProjects)
	mux.HandleFunc("/api/projects/", s.handleProject)

	// Tasks
	mux.HandleFunc("/api/tasks", s.handleTasks)
	mux.HandleFunc("/api/tasks/", s.handleTask)

	// Conversations
	mux.HandleFunc("/api/conversations/", s.handleConversation)

	// Messages (mailbox send)
	mux.HandleFunc("/api/messages", s.handleMessages)

	// Demo simulation
	mux.HandleFunc("/api/demo/run", s.handleDemoRun)

	// WeChat content article workflow
	mux.HandleFunc("/api/article/run", s.handleArticleRun)

	// SSE
	mux.HandleFunc("/events", s.handleSSE)

	srv := &http.Server{
		Addr:    addr,
		Handler: corsMiddleware(mux),
	}
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// ── CORS middleware ──────────────────────────────────────────────────────────

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PATCH,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ── SSE ─────────────────────────────────────────────────────────────────────

func (s *AgentTeamsServer) handleSSE(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := make(chan string, 64)
	s.addClient(ch)
	defer s.removeClient(ch)

	// send snapshot on connect
	s.sendSnapshot(w, fl)

	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			parts := strings.SplitN(msg, "\n", 2)
			if len(parts) == 2 {
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", parts[0], parts[1])
			} else {
				fmt.Fprintf(w, "data: %s\n\n", msg)
			}
			fl.Flush()
		}
	}
}

func (s *AgentTeamsServer) sendSnapshot(w http.ResponseWriter, fl http.Flusher) {
	if b, _ := json.Marshal(s.hub.ListAgentInfos()); b != nil {
		fmt.Fprintf(w, "event: snapshot.agents\ndata: %s\n\n", b)
	}
	if b, _ := json.Marshal(s.hub.ListTasks()); b != nil {
		fmt.Fprintf(w, "event: snapshot.tasks\ndata: %s\n\n", b)
	}
	if b, _ := json.Marshal(s.hub.ListProjects()); b != nil {
		fmt.Fprintf(w, "event: snapshot.projects\ndata: %s\n\n", b)
	}
	fl.Flush()
}

func (s *AgentTeamsServer) broadcast(eventType, data string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg := eventType + "\n" + data
	for ch := range s.clients {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (s *AgentTeamsServer) addClient(ch chan string) {
	s.mu.Lock()
	s.clients[ch] = struct{}{}
	s.mu.Unlock()
}

func (s *AgentTeamsServer) removeClient(ch chan string) {
	s.mu.Lock()
	delete(s.clients, ch)
	s.mu.Unlock()
	close(ch)
}

// ── Agents ──────────────────────────────────────────────────────────────────

func (s *AgentTeamsServer) handleAgents(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, s.hub.ListAgentInfos())

	case http.MethodPost:
		var body struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		}
		if err := decodeJSON(r, &body); err != nil || body.ID == "" {
			http.Error(w, "id required", http.StatusBadRequest)
			return
		}
		s.hub.Register(body.ID)
		if body.Role != "" {
			_ = s.hub.SetAgentRole(body.ID, body.Role)
		}
		info, _ := s.hub.GetAgentInfo(body.ID)
		writeJSON(w, info)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// /api/agents/:id  or  /api/agents/:id/status
func (s *AgentTeamsServer) handleAgent(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/agents/")
	parts := strings.SplitN(path, "/", 2)
	agentID := parts[0]
	sub := ""
	if len(parts) == 2 {
		sub = parts[1]
	}

	switch {
	case r.Method == http.MethodDelete && sub == "":
		// No native "deregister" on Hub — just set status for now
		http.Error(w, "not implemented", http.StatusNotImplemented)

	case r.Method == http.MethodPatch && sub == "status":
		var body struct {
			Status string `json:"status"`
			TaskID string `json:"task_id"`
		}
		if err := decodeJSON(r, &body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if err := s.hub.SetAgentStatus(agentID, body.Status, body.TaskID); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]string{"ok": "true"})

	case r.Method == http.MethodGet && sub == "":
		info, err := s.hub.GetAgentInfo(agentID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, info)

	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

// ── Projects ────────────────────────────────────────────────────────────────

func (s *AgentTeamsServer) handleProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, s.hub.ListProjects())

	case http.MethodPost:
		var body struct {
			CreatorID string `json:"creator_id"`
			Name      string `json:"name"`
		}
		if err := decodeJSON(r, &body); err != nil || body.CreatorID == "" || body.Name == "" {
			http.Error(w, "creator_id and name required", http.StatusBadRequest)
			return
		}
		id, err := s.hub.CreateProject(body.CreatorID, body.Name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		proj, _ := s.hub.GetProject(id)
		writeJSON(w, proj)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// /api/projects/:id/complete
func (s *AgentTeamsServer) handleProject(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/projects/")
	parts := strings.SplitN(path, "/", 2)
	projID := parts[0]

	if len(parts) == 2 && parts[1] == "complete" && r.Method == http.MethodPatch {
		if err := s.hub.CompleteProject(projID); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]string{"ok": "true"})
		return
	}

	if r.Method == http.MethodGet {
		proj, err := s.hub.GetProject(projID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, proj)
		return
	}

	http.Error(w, "not found", http.StatusNotFound)
}

// ── Tasks ───────────────────────────────────────────────────────────────────

func (s *AgentTeamsServer) handleTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		pid := r.URL.Query().Get("project_id")
		if pid != "" {
			writeJSON(w, s.hub.ListTasks(pid))
		} else {
			writeJSON(w, s.hub.ListTasks())
		}

	case http.MethodPost:
		var body struct {
			CreatorID   string `json:"creator_id"`
			Description string `json:"description"`
			ProjectID   string `json:"project_id"`
		}
		if err := decodeJSON(r, &body); err != nil || body.CreatorID == "" || body.Description == "" {
			http.Error(w, "creator_id and description required", http.StatusBadRequest)
			return
		}
		var id string
		var err error
		if body.ProjectID != "" {
			id, err = s.hub.CreateTask(body.CreatorID, body.Description, body.ProjectID)
		} else {
			id, err = s.hub.CreateTask(body.CreatorID, body.Description)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		task, _ := s.hub.GetTask(id)
		writeJSON(w, task)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// /api/tasks/:id[/assign|/start|/complete|/fail]
func (s *AgentTeamsServer) handleTask(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
	parts := strings.SplitN(path, "/", 2)
	taskID := parts[0]
	sub := ""
	if len(parts) == 2 {
		sub = parts[1]
	}

	switch {
	case r.Method == http.MethodGet && sub == "":
		t, err := s.hub.GetTask(taskID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, t)

	case r.Method == http.MethodPost && sub == "assign":
		var body struct {
			AgentID string `json:"agent_id"`
		}
		if err := decodeJSON(r, &body); err != nil || body.AgentID == "" {
			http.Error(w, "agent_id required", http.StatusBadRequest)
			return
		}
		if err := s.hub.AssignTask(taskID, body.AgentID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Also send a task_assign mailbox message
		msg, _ := mailbox.NewTaskAssignMessage("system", taskID, "")
		if t, e := s.hub.GetTask(taskID); e == nil {
			msg, _ = mailbox.NewTaskAssignMessage("system", taskID, t.Description)
		}
		_ = s.hub.Send(body.AgentID, msg)
		t, _ := s.hub.GetTask(taskID)
		writeJSON(w, t)

	case r.Method == http.MethodPost && sub == "start":
		if err := s.hub.StartTask(taskID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		t, _ := s.hub.GetTask(taskID)
		writeJSON(w, t)

	case r.Method == http.MethodPost && sub == "complete":
		var body struct {
			AgentID string `json:"agent_id"`
			Result  string `json:"result"`
		}
		if err := decodeJSON(r, &body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if err := s.hub.CompleteTask(taskID, body.AgentID, body.Result); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Send result message back to creator
		if t, e := s.hub.GetTask(taskID); e == nil && t.CreatedBy != "" {
			reply, _ := mailbox.NewTaskResultMessage(body.AgentID, taskID, body.Result, "")
			_ = s.hub.Send(t.CreatedBy, reply)
		}
		t, _ := s.hub.GetTask(taskID)
		writeJSON(w, t)

	case r.Method == http.MethodPost && sub == "fail":
		var body struct {
			Error string `json:"error"`
		}
		if err := decodeJSON(r, &body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if err := s.hub.FailTask(taskID, body.Error); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		t, _ := s.hub.GetTask(taskID)
		writeJSON(w, t)

	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

// ── Conversations ────────────────────────────────────────────────────────────

func (s *AgentTeamsServer) handleConversation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	taskID := strings.TrimPrefix(r.URL.Path, "/api/conversations/")
	if taskID == "" {
		http.Error(w, "task_id required", http.StatusBadRequest)
		return
	}
	writeJSON(w, s.hub.GetConversation(taskID))
}

// ── Messages (mailbox send) ──────────────────────────────────────────────────

func (s *AgentTeamsServer) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		From   string `json:"from"`
		To     string `json:"to"`
		TaskID string `json:"task_id"`
		Type   string `json:"type"` // "chat" | "task_assign" | "task_result"
		Text   string `json:"text"`
	}
	if err := decodeJSON(r, &body); err != nil || body.From == "" || body.To == "" {
		http.Error(w, "from and to required", http.StatusBadRequest)
		return
	}

	var msg string
	var err error
	switch body.Type {
	case "task_assign":
		msg, err = mailbox.NewTaskAssignMessage(body.From, body.TaskID, body.Text)
	case "task_result":
		msg, err = mailbox.NewTaskResultMessage(body.From, body.TaskID, body.Text, "")
	default:
		msg, err = mailbox.NewChatMessage(body.From, body.TaskID, body.Text)
	}
	if err != nil {
		log.Printf("build message: %v", err)
		http.Error(w, "failed to build message", http.StatusInternalServerError)
		return
	}

	if err := s.hub.Send(body.To, msg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"ok": "true"})
}

// ── SPA ─────────────────────────────────────────────────────────────────────

func (s *AgentTeamsServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, spaHTML)
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func decodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// ── Article run ──────────────────────────────────────────────────────────────

func (s *AgentTeamsServer) handleArticleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req ArticleRequest
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.Topic == "" && req.Niche == "" {
		http.Error(w, "topic or niche required", http.StatusBadRequest)
		return
	}
	go func() {
		ctx := context.Background()
		result, err := s.runArticleWorkflow(ctx, req, s.cfg)
		if err != nil {
			log.Printf("[article] workflow error: %v", err)
		} else {
			log.Printf("[article] done → %s", result.FilePath)
		}
	}()
	writeJSON(w, map[string]string{"ok": "true", "message": "Article workflow started"})
}
