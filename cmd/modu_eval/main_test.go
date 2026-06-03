package main

import (
	"os"
	"path/filepath"
	"strings"
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

func TestExtractMinPassRate(t *testing.T) {
	rate, rest := extractMinPassRate([]string{"-v", "--min-pass-rate", "0.8", "./pkg/agent"})
	if rate != 0.8 {
		t.Fatalf("rate = %v, want 0.8", rate)
	}
	if len(rest) != 2 || rest[0] != "-v" || rest[1] != "./pkg/agent" {
		t.Fatalf("rest = %v, want [-v ./pkg/agent]", rest)
	}

	rate2, rest2 := extractMinPassRate([]string{"--min-pass-rate=0.5", "./..."})
	if rate2 != 0.5 || len(rest2) != 1 || rest2[0] != "./..." {
		t.Fatalf("equals form: rate=%v rest=%v", rate2, rest2)
	}

	rateDefault, _ := extractMinPassRate([]string{"-run", "Eval"})
	if rateDefault != 1.0 {
		t.Fatalf("default rate = %v, want 1.0", rateDefault)
	}
}

func TestTestsBelowPassRate(t *testing.T) {
	results := []evals.EvalLogLine{
		{Name: "A", Pass: true}, {Name: "A", Pass: true}, {Name: "A", Pass: false}, // 2/3 = 67%
		{Name: "B", Pass: true}, {Name: "B", Pass: true}, // 2/2 = 100%
	}

	below := testsBelowPassRate(results, 0.8)
	if len(below) != 1 || !strings.HasPrefix(below[0], "A ") {
		t.Fatalf("min 0.8 below = %v, want only A", below)
	}
	if below := testsBelowPassRate(results, 0.5); len(below) != 0 {
		t.Fatalf("min 0.5 below = %v, want none", below)
	}
	if below := testsBelowPassRate(results, 1.0); len(below) != 1 {
		t.Fatalf("min 1.0 below = %v, want A (preserves all-or-nothing)", below)
	}
}
