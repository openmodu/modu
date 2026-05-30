package coding_agent

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// APIKeyStore manages per-provider API key storage under the agent dir.
type APIKeyStore struct {
	path string
	keys map[string]string
}

// NewAPIKeyStore creates a new API key store.
func NewAPIKeyStore(agentDir string) *APIKeyStore {
	return &APIKeyStore{
		path: filepath.Join(agentDir, "auth.json"),
		keys: make(map[string]string),
	}
}

// Load reads API keys from disk.
func (s *APIKeyStore) Load() error {
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
func (s *APIKeyStore) Get(provider string) (string, bool) {
	key, ok := s.keys[provider]
	return key, ok
}

// Set stores an API key for the given provider.
func (s *APIKeyStore) Set(provider, key string) error {
	s.keys[provider] = key
	return s.save()
}

func (s *APIKeyStore) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.keys, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600) // restrictive perms for secrets
}
