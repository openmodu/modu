package session

// Tree provides tree-based navigation over session entries.
type Tree struct {
	manager *Manager
}

// NewTree creates a new tree navigator for the given session manager.
func NewTree(manager *Manager) *Tree {
	return &Tree{manager: manager}
}

// Branch represents a branch in the session tree.
type Branch struct {
	ID       string
	ParentID string
	Label    string
	Entries  []SessionEntry
}

// NavigateTo sets the current position to the given entry ID.
func (t *Tree) NavigateTo(entryID string) error {
	return t.manager.Fork(entryID)
}

// GetBranches returns all branches (entries that have multiple children).
func (t *Tree) GetBranches() []Branch {
	entries := t.manager.Load()

	// Build children map
	children := make(map[string][]SessionEntry)
	for _, e := range entries {
		if e.ParentID != "" {
			children[e.ParentID] = append(children[e.ParentID], e)
		}
	}

	// Find branch points (entries with > 1 child)
	var branches []Branch
	for parentID, kids := range children {
		if len(kids) > 1 {
			parent, _ := t.manager.GetEntry(parentID)
			for _, kid := range kids {
				branch := Branch{
					ID:       kid.ID,
					ParentID: parentID,
					Entries:  t.getPath(kid.ID, entries),
				}
				if parent.Type == EntryTypeLabel {
					if data, ok := parent.Data.(map[string]any); ok {
						branch.Label, _ = data["text"].(string)
					}
				}
				branches = append(branches, branch)
			}
		}
	}

	return branches
}

// GetPath returns the path from root to the given entry.
func (t *Tree) GetPath(entryID string) []SessionEntry {
	entries := t.manager.Load()
	return t.getPath(entryID, entries)
}

func (t *Tree) getPath(entryID string, entries []SessionEntry) []SessionEntry {
	// Build lookup map
	lookup := make(map[string]SessionEntry)
	for _, e := range entries {
		lookup[e.ID] = e
	}

	// Walk up from target to root
	var path []SessionEntry
	current := entryID
	for current != "" {
		entry, ok := lookup[current]
		if !ok {
			break
		}
		path = append([]SessionEntry{entry}, path...)
		current = entry.ParentID
	}

	return path
}

// GetCurrentPath returns the path from root to the current position.
func (t *Tree) GetCurrentPath() []SessionEntry {
	lastID := t.manager.LastID()
	if lastID == "" {
		return nil
	}
	return t.GetPath(lastID)
}
