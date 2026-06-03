package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/openmodu/modu/pkg/evals"
)

func TestLoadResultsFiltersFailures(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "evals.jsonl")
	data := `{"name":"pass","provider":"lmstudio","pass":true}
{"name":"fail","provider":"lmstudio","pass":false,"score":0.2}
`
	if err := os.WriteFile(file, []byte(data), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	results, err := loadResults(file, true)
	if err != nil {
		t.Fatalf("loadResults() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 failed result, got %d", len(results))
	}
	if results[0].Name != "fail" {
		t.Fatalf("unexpected result: %#v", results[0])
	}
}

func TestProviderLabelIncludesModel(t *testing.T) {
	result := evals.EvalLogLine{Provider: "lmstudio", Model: "qwen-test"}
	if got := providerLabel(result); got != "lmstudio/qwen-test" {
		t.Fatalf("providerLabel() = %q", got)
	}
}
