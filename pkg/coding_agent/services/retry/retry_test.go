package retry

import (
	"testing"

	"github.com/openmodu/modu/pkg/coding_agent/foundation/config"
)

// --- Retry Manager Tests ---

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		msg      string
		expected bool
	}{
		{"server overloaded", true},
		{"rate limit exceeded", true},
		{"429 too many requests", true},
		{"HTTP 502 bad gateway", true},
		{"HTTP 503 service unavailable", true},
		{"normal error", false},
		{"invalid input", false},
		{"temporarily unavailable", true},
	}
	for _, tt := range tests {
		if got := IsRetryableError(tt.msg); got != tt.expected {
			t.Errorf("IsRetryableError(%q) = %v, want %v", tt.msg, got, tt.expected)
		}
	}
}

func TestRetryManagerReset(t *testing.T) {
	rm := New(config.RetryConfig{MaxRetries: 2, BaseDelayMs: 10, MaxDelayMs: 100}, true)
	rm.Reset()
	if !rm.IsEnabled() {
		t.Fatal("should be enabled")
	}
}

func TestRetryManagerDisabled(t *testing.T) {
	rm := New(config.RetryConfig{}, false)
	if rm.IsEnabled() {
		t.Fatal("should be disabled")
	}
	rm.SetEnabled(true)
	if !rm.IsEnabled() {
		t.Fatal("should be enabled after SetEnabled(true)")
	}
}

func TestRetryManagerAbort(t *testing.T) {
	rm := New(config.RetryConfig{MaxRetries: 3, BaseDelayMs: 10, MaxDelayMs: 100}, true)
	rm.AbortRetry()
}

// --- CycleModel Tests ---
