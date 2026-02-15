package coding_agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/crosszan/modu/pkg/llm"
)

func TestNewCodingSession(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")

	model := &llm.Model{
		ID:            "test-model",
		Name:          "Test Model",
		Api:           "ollama",
		Provider:      "ollama",
		ContextWindow: 8192,
		MaxTokens:     2048,
	}

	session, err := NewCodingSession(CodingSessionOptions{
		Cwd:      dir,
		AgentDir: agentDir,
		Model:    model,
		GetAPIKey: func(provider string) (string, error) {
			return "", nil
		},
	})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	// Check tools are initialized
	toolNames := session.GetActiveToolNames()
	if len(toolNames) == 0 {
		t.Fatal("expected tools to be initialized")
	}

	// Check config
	cfg := session.GetConfig()
	if cfg == nil {
		t.Fatal("config should not be nil")
	}
}

func TestCodingSessionRequiresCwd(t *testing.T) {
	_, err := NewCodingSession(CodingSessionOptions{
		Model: &llm.Model{ID: "test"},
	})
	if err == nil {
		t.Fatal("expected error when Cwd is empty")
	}
}

func TestCodingSessionRequiresModel(t *testing.T) {
	_, err := NewCodingSession(CodingSessionOptions{
		Cwd: "/tmp",
	})
	if err == nil {
		t.Fatal("expected error when Model is nil")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.ThinkingLevel == "" {
		t.Fatal("thinking level should have default")
	}
	if !cfg.AutoCompaction {
		t.Fatal("auto compaction should be on by default")
	}
}

func TestLoadConfigMissing(t *testing.T) {
	cfg, err := LoadConfig("/nonexistent", "/nonexistent")
	if err != nil {
		t.Fatalf("loading missing config should not error: %v", err)
	}
	if cfg.ThinkingLevel == "" {
		t.Fatal("should have defaults when files are missing")
	}
}

func TestLoadConfigFromFile(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agent")
	os.MkdirAll(agentDir, 0o755)

	configContent := `{"thinkingLevel":"high","autoCompaction":false}`
	os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(configContent), 0o644)

	cfg, err := LoadConfig(agentDir, dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ThinkingLevel != "high" {
		t.Fatalf("expected thinking level 'high', got %s", cfg.ThinkingLevel)
	}
	if cfg.AutoCompaction {
		t.Fatal("auto compaction should be false from config")
	}
}

func TestAPIKeyStore(t *testing.T) {
	dir := t.TempDir()
	store := NewAPIKeyStore(dir)

	// Set and get
	store.Set("test-provider", "test-key-123")

	key, ok := store.Get("test-provider")
	if !ok || key != "test-key-123" {
		t.Fatal("failed to get stored key")
	}

	// Missing key
	_, ok = store.Get("nonexistent")
	if ok {
		t.Fatal("should not find nonexistent key")
	}
}
