package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// Happy path requires a live LLM, so it's exercised manually. These tests
// cover the deterministic error branches the user is most likely to hit.

func TestAddEmptyDescriptionErrors(t *testing.T) {
	err := Add(context.Background(), filepath.Join(t.TempDir(), "x.yaml"), "", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "description required") {
		t.Errorf("expected 'description required', got: %v", err)
	}
}

func TestAddWhitespaceOnlyDescriptionErrors(t *testing.T) {
	err := Add(context.Background(), filepath.Join(t.TempDir(), "x.yaml"), "   \t\n", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "description required") {
		t.Errorf("expected 'description required', got: %v", err)
	}
}

func TestAddNoProviderErrors(t *testing.T) {
	unsetAllProviders(t)
	err := Add(context.Background(), filepath.Join(t.TempDir(), "x.yaml"), "every morning at 8 run git log", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "no provider configured") {
		t.Errorf("expected 'no provider configured', got: %v", err)
	}
}
