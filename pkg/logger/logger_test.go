package logger

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestSetGetLevel(t *testing.T) {
	orig := GetLevel()
	defer SetLevel(orig)

	for _, l := range []Level{LevelDebug, LevelInfo, LevelWarn, LevelError, LevelFatal} {
		SetLevel(l)
		if got := GetLevel(); got != l {
			t.Errorf("SetLevel(%v): GetLevel() = %v", l, got)
		}
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input string
		want  Level
	}{
		{"debug", LevelDebug},
		{"DEBUG", LevelDebug},
		{"info", LevelInfo},
		{"warn", LevelWarn},
		{"WARNING", LevelWarn},
		{"error", LevelError},
		{"fatal", LevelFatal},
		{"unknown", LevelInfo}, // default
		{"", LevelInfo},
	}
	for _, tt := range tests {
		if got := ParseLevel(tt.input); got != tt.want {
			t.Errorf("ParseLevel(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestLevelFiltering(t *testing.T) {
	orig := GetLevel()
	defer SetLevel(orig)

	SetLevel(LevelWarn)

	// These should not panic.
	Debug("should be filtered")
	Info("should be filtered")
	Warn("should appear")
	Error("should appear")
}

func TestWithLogger(t *testing.T) {
	orig := GetLevel()
	defer SetLevel(orig)
	SetLevel(LevelDebug)

	log := With("component", "test")
	log.Info("hello", "key", "value")

	child := log.With("sub", "child")
	child.Debug("nested", "n", 42)
}

func TestJSONFileLogging(t *testing.T) {
	orig := GetLevel()
	defer SetLevel(orig)
	SetLevel(LevelDebug)

	tmpFile := t.TempDir() + "/test.log"
	if err := EnableFileLogging(tmpFile); err != nil {
		t.Fatalf("EnableFileLogging: %v", err)
	}
	defer DisableFileLogging()

	Info("test message", "key", "val", "num", 123)
	With("component", "moms").Warn("alert", "code", 500)

	DisableFileLogging()

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 log lines, got %d: %s", len(lines), string(data))
	}

	// Verify first line is valid JSON with expected fields.
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("JSON parse error: %v (line=%q)", err, lines[0])
	}
	if entry["level"] != "INFO" {
		t.Errorf("expected level INFO, got %v", entry["level"])
	}
	if entry["msg"] != "test message" {
		t.Errorf("expected msg 'test message', got %v", entry["msg"])
	}
	if entry["key"] != "val" {
		t.Errorf("expected key=val, got %v", entry["key"])
	}
	if entry["num"] != float64(123) {
		t.Errorf("expected num=123, got %v", entry["num"])
	}

	// Verify second line has component.
	var entry2 map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &entry2); err != nil {
		t.Fatalf("JSON parse error: %v", err)
	}
	if entry2["component"] != "moms" {
		t.Errorf("expected component=moms, got %v", entry2["component"])
	}
	if entry2["level"] != "WARN" {
		t.Errorf("expected level WARN, got %v", entry2["level"])
	}
}

func TestLevelString(t *testing.T) {
	tests := []struct {
		level Level
		want  string
	}{
		{LevelDebug, "DEBUG"},
		{LevelInfo, "INFO"},
		{LevelWarn, "WARN"},
		{LevelError, "ERROR"},
		{LevelFatal, "FATAL"},
		{Level(99), "LEVEL(99)"},
	}
	for _, tt := range tests {
		if got := tt.level.String(); got != tt.want {
			t.Errorf("Level(%d).String() = %q, want %q", tt.level, got, tt.want)
		}
	}
}

// captureStderr is a test helper (unused currently but available for
// future tests that want to verify stderr output).
func captureStderr(fn func()) string {
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	fn()

	w.Close()
	os.Stderr = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	return buf.String()
}
