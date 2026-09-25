package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProviderCLIFlow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("MODU_MODELROUTER_CONFIG", path)
	var out bytes.Buffer
	if err := run([]string{"provider", "add", "deepseek", "--url", "https://api.deepseek.com/v1", "--models", "deepseek-chat", "--key-env", "DEEPSEEK_API_KEY"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"models"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "deepseek/deepseek-chat") {
		t.Fatalf("models: %s", out.String())
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), `"api_key_env": "DEEPSEEK_API_KEY"`) {
		t.Fatalf("config: %s, %v", b, err)
	}
	if err := run([]string{"provider", "rm", "deepseek"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run([]string{"models"}, &out, &out); err != nil || out.Len() != 0 {
		t.Fatalf("models after removal: %q, %v", out.String(), err)
	}
}
