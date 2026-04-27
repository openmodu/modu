package main

import (
	"errors"
	"fmt"
	"time"
)

// SessionStatus is the lifecycle state of a session.
type SessionStatus string

const (
	SessionIdle      SessionStatus = "idle"
	SessionRunning   SessionStatus = "running"
	SessionCancelled SessionStatus = "cancelled"
)

// Session groups multiple turns under one agent+project context.
// The ACP subprocess (or modu CodingSession) is kept alive across turns,
// providing natural conversation continuity.
type Session struct {
	ID        string        `json:"id"`
	ProjectID string        `json:"projectId"`
	Agent     string        `json:"agent"`
	Title     string        `json:"title,omitempty"`
	Status    SessionStatus `json:"status"`
	Cwd       string        `json:"cwd"` // resolved from project.Path
	CreatedAt time.Time     `json:"createdAt"`
	UpdatedAt time.Time     `json:"updatedAt"`
}

// SessionDetail is Session with its full turn history included.
type SessionDetail struct {
	Session
	Turns []*Turn `json:"turns"`
}

// sessionEntry is the in-memory record for a Session.
type sessionEntry struct {
	session *Session
	turns   []string // ordered turn IDs
}

// CreateSession creates a new idle session for (agent, project).
// title may be empty; it is auto-set to the first turn's prompt later.
func (s *Store) CreateSession(projectID, agent, title string) (*Session, error) {
	if projectID == "" || agent == "" {
		return nil, errors.New("projectId and agent are required")
	}
	s.mu.Lock()
	proj, ok := s.projects[projectID]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("project %q not found", projectID)
	}
	n := s.sctr.Add(1)
	now := time.Now().UTC()
	sess := &Session{
		ID:        fmt.Sprintf("sess-%d", n),
		ProjectID: projectID,
		Agent:     agent,
		Title:     title,
		Status:    SessionIdle,
		Cwd:       proj.Path,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.sessions[sess.ID] = &sessionEntry{session: sess}
	cp := *sess
	s.mu.Unlock()

	dbInsertSession(s.db, sess)
	return &cp, nil
}

// GetSession returns a copy of the session header (no turns).
func (s *Store) GetSession(id string) (*Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.sessions[id]
	if !ok {
		return nil, false
	}
	cp := *e.session
	return &cp, true
}

// GetSessionDetail returns the session with all its turns.
func (s *Store) GetSessionDetail(id string) (*SessionDetail, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.sessions[id]
	if !ok {
		return nil, false
	}
	turns := make([]*Turn, 0, len(e.turns))
	for _, tid := range e.turns {
		if te, ok := s.turns[tid]; ok {
			cp := *te.turn
			turns = append(turns, &cp)
		}
	}
	d := &SessionDetail{Session: *e.session, Turns: turns}
	return d, true
}

// ListSessions returns all sessions, optionally filtered by projectID.
func (s *Store) ListSessions(projectID string) []*Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Session, 0, len(s.sessions))
	for _, e := range s.sessions {
		if projectID != "" && e.session.ProjectID != projectID {
			continue
		}
		cp := *e.session
		out = append(out, &cp)
	}
	return out
}

// DeleteSession removes a session and its turns from memory.
func (s *Store) DeleteSession(id string) bool {
	s.mu.Lock()
	e, ok := s.sessions[id]
	if !ok {
		s.mu.Unlock()
		return false
	}
	turnIDs := append([]string(nil), e.turns...)
	delete(s.sessions, id)
	for _, tid := range turnIDs {
		delete(s.turns, tid)
	}
	s.mu.Unlock()
	dbDeleteSession(s.db, id)
	return true
}

// CancelSession cancels the currently running turn (if any).
func (s *Store) CancelSession(id string) bool {
	s.mu.Lock()
	e, ok := s.sessions[id]
	if !ok || e.session.Status != SessionRunning {
		s.mu.Unlock()
		return false
	}
	// Find the running turn and cancel it.
	var cancelFn func()
	for i := len(e.turns) - 1; i >= 0; i-- {
		te, ok := s.turns[e.turns[i]]
		if ok && te.turn.Status == TurnRunning && te.cancel != nil {
			cancelFn = te.cancel
			break
		}
	}
	s.mu.Unlock()
	if cancelFn != nil {
		cancelFn()
		return true
	}
	return false
}

// AddTurn creates a new turn inside the session and enqueues it.
// If the session title is empty, it is set to the first prompt.
func (s *Store) AddTurn(sessionID, prompt string) (*Turn, error) {
	if prompt == "" {
		return nil, errors.New("prompt is required")
	}
	s.mu.Lock()
	se, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("session %q not found", sessionID)
	}
	if se.session.Status == SessionRunning {
		s.mu.Unlock()
		return nil, errors.New("session already has a running turn")
	}

	n := s.tctr.Add(1)
	now := time.Now().UTC()
	turn := &Turn{
		ID:        fmt.Sprintf("turn-%d", n),
		SessionID: sessionID,
		Agent:     se.session.Agent,
		Cwd:       se.session.Cwd,
		Prompt:    prompt,
		Status:    TurnPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if se.session.Title == "" {
		title := prompt
		if len(title) > 80 {
			title = title[:80] + "…"
		}
		se.session.Title = title
		se.session.UpdatedAt = now
	}
	se.turns = append(se.turns, turn.ID)
	s.turns[turn.ID] = &turnEntry{turn: turn, subs: make(map[int]chan SSEEvent)}
	snap := *turn
	s.mu.Unlock()

	dbInsertTurn(s.db, turn)

	select {
	case s.queue <- turn.ID:
	default:
		s.FailTurn(turn.ID, "queue full")
		return nil, errors.New("queue full")
	}
	return &snap, nil
}
