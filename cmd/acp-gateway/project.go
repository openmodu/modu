package main

import (
	"errors"
	"fmt"
	"time"
)

// Project is a named working directory. Sessions are scoped to a project.
type Project struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	CreatedAt time.Time `json:"createdAt"`
}

// CreateProject adds a new project and returns it.
func (s *Store) CreateProject(name, path string) (*Project, error) {
	if name == "" || path == "" {
		return nil, errors.New("name and path are required")
	}
	n := s.pctr.Add(1)
	p := &Project{
		ID:        fmt.Sprintf("proj-%d", n),
		Name:      name,
		Path:      path,
		CreatedAt: time.Now().UTC(),
	}
	s.mu.Lock()
	s.projects[p.ID] = p
	s.mu.Unlock()
	dbInsertProject(s.db, p)
	return p, nil
}

// GetProject returns a copy of the project or (nil, false).
func (s *Store) GetProject(id string) (*Project, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[id]
	if !ok {
		return nil, false
	}
	cp := *p
	return &cp, true
}

// ListProjects returns a snapshot of all projects.
func (s *Store) ListProjects() []*Project {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Project, 0, len(s.projects))
	for _, p := range s.projects {
		cp := *p
		out = append(out, &cp)
	}
	return out
}

// DeleteProject removes a project and all its sessions and turns.
// Returns false if it did not exist.
func (s *Store) DeleteProject(id string) bool {
	s.mu.Lock()
	_, ok := s.projects[id]
	if ok {
		delete(s.projects, id)
		// Remove all sessions (and their turns) that belong to this project.
		for sid, e := range s.sessions {
			if e.session.ProjectID == id {
				for _, tid := range e.turns {
					delete(s.turns, tid)
				}
				delete(s.sessions, sid)
			}
		}
	}
	s.mu.Unlock()
	if ok {
		dbDeleteProject(s.db, id)
	}
	return ok
}
