package moms

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type CompactionSettings struct {
	Enabled          bool `json:"enabled"`
	ReserveTokens    int  `json:"reserveTokens"`
	KeepRecentTokens int  `json:"keepRecentTokens"`

	// Soft-summarization settings (mirrors PicoClaw).
	SummarizeEnabled          bool `json:"summarizeEnabled"`
	ContextWindow             int  `json:"contextWindow"`             // token capacity of the model; default 32768
	SummarizeTokenPercent     int  `json:"summarizeTokenPercent"`     // trigger at this % of ContextWindow; default 75
	SummarizeMessageThreshold int  `json:"summarizeMessageThreshold"` // also trigger above this many messages; default 20
}

type RetrySettings struct {
	Enabled     bool `json:"enabled"`
	MaxRetries  int  `json:"maxRetries"`
	BaseDelayMs int  `json:"baseDelayMs"`
}

type Settings struct {
	DefaultProvider      string              `json:"defaultProvider,omitempty"`
	DefaultModel         string              `json:"defaultModel,omitempty"`
	DefaultThinkingLevel string              `json:"defaultThinkingLevel,omitempty"`
	Compaction           *CompactionSettings `json:"compaction,omitempty"`
	Retry                *RetrySettings      `json:"retry,omitempty"`

	// Internal
	path string     `json:"-"`
	mu   sync.Mutex `json:"-"`
}

var DefaultCompaction = CompactionSettings{
	Enabled:                   true,
	ReserveTokens:             16384,
	KeepRecentTokens:          20000,
	SummarizeEnabled:          true,
	ContextWindow:             32768,
	SummarizeTokenPercent:     75,
	SummarizeMessageThreshold: 20,
}

var DefaultRetry = RetrySettings{
	Enabled:     true,
	MaxRetries:  3,
	BaseDelayMs: 2000,
}

func NewSettingsManager(workingDir string) *Settings {
	s := &Settings{
		path: filepath.Join(workingDir, "settings.json"),
	}
	s.Load()
	return s
}

func (s *Settings) Load() {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err == nil {
		_ = json.Unmarshal(data, s)
	}
}

func (s *Settings) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}

func (s *Settings) GetModelID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.DefaultModel != "" {
		return s.DefaultModel
	}
	return ""
}

func (s *Settings) GetProvider() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.DefaultProvider != "" {
		return s.DefaultProvider
	}
	return ""
}

func (s *Settings) GetThinkingLevel() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.DefaultThinkingLevel != "" {
		return s.DefaultThinkingLevel
	}
	return "off"
}

// GetCompaction returns the effective compaction settings, applying defaults
// to any zero-valued fields.
func (s *Settings) GetCompaction() CompactionSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := DefaultCompaction
	if s.Compaction != nil {
		if s.Compaction.Enabled != c.Enabled {
			c.Enabled = s.Compaction.Enabled
		}
		if s.Compaction.ReserveTokens > 0 {
			c.ReserveTokens = s.Compaction.ReserveTokens
		}
		if s.Compaction.KeepRecentTokens > 0 {
			c.KeepRecentTokens = s.Compaction.KeepRecentTokens
		}
		c.SummarizeEnabled = s.Compaction.SummarizeEnabled
		if s.Compaction.ContextWindow > 0 {
			c.ContextWindow = s.Compaction.ContextWindow
		}
		if s.Compaction.SummarizeTokenPercent > 0 {
			c.SummarizeTokenPercent = s.Compaction.SummarizeTokenPercent
		}
		if s.Compaction.SummarizeMessageThreshold > 0 {
			c.SummarizeMessageThreshold = s.Compaction.SummarizeMessageThreshold
		}
	}
	return c
}

func (s *Settings) SetModel(provider, model string) error {
	s.mu.Lock()
	s.DefaultProvider = provider
	s.DefaultModel = model
	s.mu.Unlock() // Unlock before Save
	return s.Save()
}
