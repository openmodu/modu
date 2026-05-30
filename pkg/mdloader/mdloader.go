// Package mdloader provides the shared discovery skeleton for Markdown-based
// resource managers (e.g. prompt templates and skills). It owns the repetitive
// parts — ordered built-in roots, caller-supplied extra paths, re-scan on every
// access, locking, and the atomic map swap — while each domain plugs in a
// Parser that knows how to scan directories and parse files into its own item
// type and decide override precedence.
package mdloader

import (
	"os"
	"sync"
)

// Ref points to a file or directory to discover, tagged with a source label
// for provenance (e.g. "user", "project", "package").
type Ref struct {
	Path   string
	Source string
}

// Parser turns discovered files into named domain items. Both methods write
// into dst keyed by item name; the chosen write rule (overwrite vs. keep-first)
// is how a domain expresses its override precedence.
type Parser[T any] interface {
	// ParseDir scans a built-in discovery root directory.
	ParseDir(dst map[string]*T, dir, source string) error
	// ParsePath scans an explicit extra file or directory.
	ParsePath(dst map[string]*T, path, source string) error
}

// Manager discovers domain items from an ordered set of built-in roots plus
// caller-supplied extra paths. Every accessor re-scans disk first, so added,
// edited, or removed files are reflected without restarting the session.
type Manager[T any] struct {
	roots  []Ref
	parser Parser[T]

	mu    sync.RWMutex
	items map[string]*T
	extra []Ref
}

// New creates a Manager. roots are scanned in order on every Discover; the
// parser decides how same-named items from later roots are merged.
func New[T any](roots []Ref, parser Parser[T]) *Manager[T] {
	return &Manager[T]{
		roots:  roots,
		parser: parser,
		items:  make(map[string]*T),
	}
}

// SetExtraRefs registers additional files or directories (e.g. from resource
// packages) to include in discovery after the built-in roots.
func (m *Manager[T]) SetExtraRefs(refs []Ref) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.extra = append([]Ref(nil), refs...)
}

// Discover scans the built-in roots in order, then the extra paths, and
// atomically replaces the in-memory item map. A missing root directory is not
// an error; any other ParseDir error aborts the scan. ParsePath errors on extra
// paths are ignored, matching the lenient handling of optional package paths.
func (m *Manager[T]) Discover() error {
	fresh := make(map[string]*T)
	for _, r := range m.roots {
		if err := m.parser.ParseDir(fresh, r.Path, r.Source); err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	m.mu.RLock()
	extra := append([]Ref(nil), m.extra...)
	m.mu.RUnlock()
	for _, r := range extra {
		_ = m.parser.ParsePath(fresh, r.Path, r.Source)
	}

	m.mu.Lock()
	m.items = fresh
	m.mu.Unlock()
	return nil
}

// Lookup re-discovers, then returns the stored item by name. The returned
// pointer is the stored value; callers that mutate it must clone first.
func (m *Manager[T]) Lookup(name string) (*T, bool) {
	_ = m.Discover()
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.items[name]
	return t, ok
}

// Snapshot re-discovers, then returns all stored items (unsorted).
func (m *Manager[T]) Snapshot() []*T {
	_ = m.Discover()
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*T, 0, len(m.items))
	for _, t := range m.items {
		out = append(out, t)
	}
	return out
}
