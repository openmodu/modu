package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

type workflowAgentCache struct {
	mu      sync.Mutex
	entries map[string][]workflowAgentCacheEntry
}

type workflowAgentCacheEntry struct {
	Key        string
	Label      string
	Phase      string
	Prompt     string
	Text       string
	Value      any
	Spent      int
	StartedAt  time.Time
	EndedAt    time.Time
	DurationMs int64
}

func newWorkflowAgentCache() *workflowAgentCache {
	return &workflowAgentCache{entries: map[string][]workflowAgentCacheEntry{}}
}

func (c *workflowAgentCache) add(entry workflowAgentCacheEntry) {
	if c == nil || entry.Key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string][]workflowAgentCacheEntry{}
	}
	c.entries[entry.Key] = append(c.entries[entry.Key], entry)
}

func (c *workflowAgentCache) get(key string, idx int) (workflowAgentCacheEntry, bool) {
	if c == nil || key == "" || idx < 0 {
		return workflowAgentCacheEntry{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entries := c.entries[key]
	if idx >= len(entries) {
		return workflowAgentCacheEntry{}, false
	}
	return entries[idx], true
}

func workflowAgentCacheKey(prompt, phase, label string, opts agentOptions) string {
	payload := struct {
		Prompt          string         `json:"prompt"`
		Phase           string         `json:"phase,omitempty"`
		Label           string         `json:"label,omitempty"`
		Model           string         `json:"model,omitempty"`
		Cwd             string         `json:"cwd,omitempty"`
		Isolation       string         `json:"isolation,omitempty"`
		Tools           []string       `json:"tools,omitempty"`
		DisallowedTools []string       `json:"disallowedTools,omitempty"`
		PermissionMode  string         `json:"permissionMode,omitempty"`
		MaxTurns        int            `json:"maxTurns,omitempty"`
		Thinking        string         `json:"thinking,omitempty"`
		Skills          []string       `json:"skills,omitempty"`
		MemoryScope     string         `json:"memoryScope,omitempty"`
		Schema          map[string]any `json:"schema,omitempty"`
	}{
		Prompt:          prompt,
		Phase:           phase,
		Label:           label,
		Model:           opts.Model,
		Cwd:             opts.Cwd,
		Isolation:       opts.Isolation,
		Tools:           opts.Tools,
		DisallowedTools: opts.DisallowedTools,
		PermissionMode:  opts.PermissionMode,
		MaxTurns:        opts.MaxTurns,
		Thinking:        opts.Thinking,
		Skills:          opts.Skills,
		MemoryScope:     opts.MemoryScope,
		Schema:          opts.Schema,
	}
	data, _ := json.Marshal(payload)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
