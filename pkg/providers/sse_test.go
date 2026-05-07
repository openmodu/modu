package providers

import (
	"strings"
	"testing"
)

func TestScanSSEData(t *testing.T) {
	var got []string
	err := ScanSSEData(strings.NewReader("event: message\ndata: one\n\ndata: two\ndata: [DONE]\ndata: three\n"), func(data string) bool {
		got = append(got, data)
		return true
	})
	if err != nil {
		t.Fatalf("ScanSSEData returned error: %v", err)
	}
	if strings.Join(got, ",") != "one,two" {
		t.Fatalf("got %v", got)
	}
}

func TestScanSSEDataStopsWhenHandlerReturnsFalse(t *testing.T) {
	var got []string
	err := ScanSSEData(strings.NewReader("data: one\ndata: two\n"), func(data string) bool {
		got = append(got, data)
		return false
	})
	if err != nil {
		t.Fatalf("ScanSSEData returned error: %v", err)
	}
	if strings.Join(got, ",") != "one" {
		t.Fatalf("got %v", got)
	}
}
