// Package todo is the session todo-list service: a small self-contained store
// that notifies the host of changes through an OnChange callback. It has no
// dependency on the session.
package todo

import "sync"

// Item is one task tracked during a coding session.
type Item struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}

// Store owns the session todo list. State changes invoke OnChange (if set)
// rather than reaching back into the session.
type Store struct {
	mu       sync.RWMutex
	items    []Item
	OnChange func()
}

// NewStore creates an empty todo store.
func NewStore() *Store { return &Store{} }

// Get returns a copy of the current todo list.
func (s *Store) Get() []Item {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Item, len(s.items))
	copy(out, s.items)
	return out
}

// Set replaces the todo list and fires OnChange.
func (s *Store) Set(items []Item) {
	s.mu.Lock()
	s.items = make([]Item, len(items))
	copy(s.items, items)
	s.mu.Unlock()
	if s.OnChange != nil {
		s.OnChange()
	}
}
