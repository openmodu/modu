package main

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/openmodu/modu/pkg/tokenkit"
)

type TokenkitScopedUsage struct {
	ID        string              `json:"id"`
	Name      string              `json:"name,omitempty"`
	Path      string              `json:"path,omitempty"`
	ProjectID string              `json:"projectId,omitempty"`
	Agent     string              `json:"agent,omitempty"`
	Match     string              `json:"match"`
	Totals    tokenkit.SummaryRow `json:"totals"`
}

type tokenkitScopeSnapshot struct {
	Projects []*Project
	Sessions []*Session
	Turns    []*Turn
}

func (s *Store) TokenkitScopeSnapshot() tokenkitScopeSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	projects := make([]*Project, 0, len(s.projects))
	for _, id := range s.projectOrder {
		if p, ok := s.projects[id]; ok {
			cp := *p
			projects = append(projects, &cp)
		}
	}

	sessions := make([]*Session, 0, len(s.sessions))
	for _, id := range s.sessionOrder {
		if se, ok := s.sessions[id]; ok {
			cp := *se.session
			sessions = append(sessions, &cp)
		}
	}

	turns := make([]*Turn, 0, len(s.turns))
	for _, te := range s.turns {
		cp := *te.turn
		turns = append(turns, &cp)
	}

	return tokenkitScopeSnapshot{
		Projects: projects,
		Sessions: sessions,
		Turns:    turns,
	}
}

func buildTokenkitScopedUsage(records []tokenkit.UsageRecord, snap tokenkitScopeSnapshot) ([]TokenkitScopedUsage, []TokenkitScopedUsage) {
	projects := map[string]*TokenkitScopedUsage{}
	sessions := map[string]*TokenkitScopedUsage{}

	for _, project := range snap.Projects {
		projects[project.ID] = &TokenkitScopedUsage{
			ID:    project.ID,
			Name:  project.Name,
			Path:  project.Path,
			Match: "workspace",
		}
	}
	for _, session := range snap.Sessions {
		sessions[session.ID] = &TokenkitScopedUsage{
			ID:        session.ID,
			Name:      session.Title,
			Path:      session.Cwd,
			ProjectID: session.ProjectID,
			Agent:     session.Agent,
			Match:     "none",
		}
	}

	for _, record := range records {
		if project := matchProjectForRecord(record, snap.Projects); project != nil {
			projects[project.ID].Totals.Accumulate(record)
		}
		if session, match := matchSessionForRecord(record, snap.Sessions, snap.Turns); session != nil {
			row := sessions[session.ID]
			row.Totals.Accumulate(record)
			if row.Match == "none" || match == "session-id" {
				row.Match = match
			}
		}
	}

	projectRows := make([]TokenkitScopedUsage, 0, len(projects))
	for _, project := range snap.Projects {
		if row := projects[project.ID]; row != nil {
			projectRows = append(projectRows, *row)
		}
	}
	sessionRows := make([]TokenkitScopedUsage, 0, len(sessions))
	for _, session := range snap.Sessions {
		if row := sessions[session.ID]; row != nil {
			sessionRows = append(sessionRows, *row)
		}
	}
	return projectRows, sessionRows
}

func matchProjectForRecord(record tokenkit.UsageRecord, projects []*Project) *Project {
	workspace := cleanPath(record.Workspace)
	if workspace == "" {
		return nil
	}
	var best *Project
	bestLen := -1
	for _, project := range projects {
		path := cleanPath(project.Path)
		if path == "" || !pathContains(path, workspace) {
			continue
		}
		if len(path) > bestLen {
			best = project
			bestLen = len(path)
		}
	}
	return best
}

func matchSessionForRecord(record tokenkit.UsageRecord, sessions []*Session, turns []*Turn) (*Session, string) {
	if id := record.SessionID(); id != "" {
		for _, session := range sessions {
			if session.ID == id {
				return session, "session-id"
			}
		}
	}

	appAgent := agentForTokenkitApp(record.App)
	workspace := cleanPath(record.Workspace)
	if workspace == "" || appAgent == "" {
		return nil, ""
	}
	var best *Session
	var bestMatch string
	var bestDuration time.Duration
	for _, session := range sessions {
		if session.Agent != appAgent || cleanPath(session.Cwd) != workspace {
			continue
		}
		if inTurnWindow(record.StartedAt, session.ID, turns) {
			return session, "turn-window"
		}
		if !record.StartedAt.IsZero() && !record.StartedAt.Before(session.CreatedAt) && !record.StartedAt.After(session.UpdatedAt.Add(5*time.Minute)) {
			duration := session.UpdatedAt.Sub(session.CreatedAt)
			if best == nil || duration < bestDuration {
				best = session
				bestMatch = "session-window"
				bestDuration = duration
			}
		}
	}
	if best != nil {
		return best, bestMatch
	}

	candidates := sessionsForWorkspaceAgent(sessions, workspace, appAgent)
	if len(candidates) == 1 {
		return candidates[0], "workspace-agent"
	}
	if len(candidates) > 1 {
		if session := nearestSession(record.StartedAt, candidates); session != nil {
			return session, "nearest-session"
		}
	}
	return best, bestMatch
}

func sessionsForWorkspaceAgent(sessions []*Session, workspace, agent string) []*Session {
	var out []*Session
	for _, session := range sessions {
		if session.Agent == agent && cleanPath(session.Cwd) == workspace {
			out = append(out, session)
		}
	}
	return out
}

func nearestSession(startedAt time.Time, sessions []*Session) *Session {
	if startedAt.IsZero() {
		return nil
	}
	var best *Session
	var bestDistance time.Duration
	for _, session := range sessions {
		distance := timeDistance(startedAt, session.CreatedAt)
		if !session.UpdatedAt.IsZero() {
			if d := timeDistance(startedAt, session.UpdatedAt); d < distance {
				distance = d
			}
		}
		if best == nil || distance < bestDistance {
			best = session
			bestDistance = distance
		}
	}
	return best
}

func timeDistance(a, b time.Time) time.Duration {
	if a.After(b) {
		return a.Sub(b)
	}
	return b.Sub(a)
}

func inTurnWindow(startedAt time.Time, sessionID string, turns []*Turn) bool {
	if startedAt.IsZero() {
		return false
	}
	for _, turn := range turns {
		if turn.SessionID != sessionID {
			continue
		}
		end := turn.UpdatedAt
		if end.Before(turn.CreatedAt) {
			end = turn.CreatedAt
		}
		if !startedAt.Before(turn.CreatedAt.Add(-30*time.Second)) && !startedAt.After(end.Add(5*time.Minute)) {
			return true
		}
	}
	return false
}


func agentForTokenkitApp(app string) string {
	switch app {
	case tokenkit.AppCodex:
		return "codex"
	case tokenkit.AppClaudeCode:
		return "claude"
	case tokenkit.AppGemini:
		return "gemini"
	default:
		return ""
	}
}


func cleanPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Clean(path)
}

func pathContains(parent, child string) bool {
	if parent == child {
		return true
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

