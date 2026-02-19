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
	Enabled:          true,
	ReserveTokens:    16384,
	KeepRecentTokens: 20000,
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

func (s *Settings) SetModel(provider, model string) error {
	s.mu.Lock()
	s.DefaultProvider = provider
	s.DefaultModel = model
	s.mu.Unlock() // Unlock before Save
	return s.Save()
}
