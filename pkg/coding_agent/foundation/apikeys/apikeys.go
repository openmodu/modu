package apikeys

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Store manages per-provider API key storage under the agent dir.
type Store struct {
	path string
	keys map[string]string
}

// New creates a new API key store.
func New(agentDir string) *Store {
	return &Store{
		path: filepath.Join(agentDir, "auth.json"),
		keys: make(map[string]string),
	}
}

// Load reads API keys from disk.
func (s *Store) Load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return json.Unmarshal(data, &s.keys)
}

// Get returns an API key for the given provider.
func (s *Store) Get(provider string) (string, bool) {
	key, ok := s.keys[provider]
	return key, ok
}

// Set stores an API key for the given provider.
func (s *Store) Set(provider, key string) error {
	s.keys[provider] = key
	return s.save()
}

func (s *Store) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.keys, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600) // restrictive perms for secrets
}
