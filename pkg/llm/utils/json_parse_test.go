package utils

import "testing"

func TestParseStreamingJSON(t *testing.T) {
	value := ParseStreamingJSON(`{"a":1}`)
	m, ok := value.(map[string]any)
	if !ok || m["a"] != float64(1) {
		t.Fatalf("unexpected value: %#v", value)
	}
	value = ParseStreamingJSON("{")
	m, ok = value.(map[string]any)
	if !ok || len(m) != 0 {
		t.Fatalf("expected empty object, got %#v", value)
	}
}
