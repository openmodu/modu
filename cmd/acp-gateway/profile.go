package main

import (
	"errors"
	"fmt"
	"time"
)

// Profile bundles an agent and reusable system prompt for new sessions.
type Profile struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description,omitempty"`
	AgentID      string    `json:"agentId"`
	SystemPrompt string    `json:"systemPrompt"`
	Icon         string    `json:"icon,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// CreateProfile adds a reusable agent profile.
func (s *Store) CreateProfile(name, agentID, systemPrompt, description, icon string) (*Profile, error) {
	if name == "" || agentID == "" {
		return nil, errors.New("name and agentId are required")
	}
	n := s.prctr.Add(1)
	now := time.Now().UTC()
	p := &Profile{
		ID:           fmt.Sprintf("prof-%d", n),
		Name:         name,
		Description:  description,
		AgentID:      agentID,
		SystemPrompt: systemPrompt,
		Icon:         icon,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	s.mu.Lock()
	s.profiles[p.ID] = p
	cp := *p
	s.mu.Unlock()
	dbInsertProfile(s.db, p)
	return &cp, nil
}

// GetProfile returns a copy of the profile or (nil, false).
func (s *Store) GetProfile(id string) (*Profile, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.profiles[id]
	if !ok {
		return nil, false
	}
	cp := *p
	return &cp, true
}

// ListProfiles returns a snapshot of all profiles.
func (s *Store) ListProfiles() []*Profile {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Profile, 0, len(s.profiles))
	for _, p := range s.profiles {
		cp := *p
		out = append(out, &cp)
	}
	return out
}

// UpdateProfile replaces editable profile fields.
func (s *Store) UpdateProfile(id, name, agentID, systemPrompt, description, icon string) (*Profile, error) {
	if name == "" || agentID == "" {
		return nil, errors.New("name and agentId are required")
	}
	s.mu.Lock()
	p, ok := s.profiles[id]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("profile %q not found", id)
	}
	p.Name = name
	p.Description = description
	p.AgentID = agentID
	p.SystemPrompt = systemPrompt
	p.Icon = icon
	p.UpdatedAt = time.Now().UTC()
	cp := *p
	s.mu.Unlock()
	dbUpdateProfile(s.db, &cp)
	return &cp, nil
}

// DeleteProfile removes a profile. Existing sessions keep their profileId.
func (s *Store) DeleteProfile(id string) bool {
	s.mu.Lock()
	_, ok := s.profiles[id]
	if ok {
		delete(s.profiles, id)
	}
	s.mu.Unlock()
	if ok {
		dbDeleteProfile(s.db, id)
	}
	return ok
}

// parseProfileSeq extracts the numeric suffix from "prof-N".
func parseProfileSeq(id string) uint64 {
	var n uint64
	fmt.Sscanf(id, "prof-%d", &n)
	return n
}
