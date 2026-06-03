package evals

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// EvalLogLine is one JSONL row in evals.jsonl.
type EvalLogLine struct {
	Name      string  `json:"name"`
	Timestamp string  `json:"timestamp"`
	RunNumber int     `json:"run_number"`
	Provider  string  `json:"provider"`
	Model     string  `json:"model"`
	Grader    string  `json:"grader"`
	Rubric    string  `json:"rubric"`
	Output    string  `json:"output"`
	Reasoning string  `json:"reasoning"`
	Score     float64 `json:"score"`
	Pass      bool    `json:"pass"`
}

// EvalResult is the caller-provided grading result.
type EvalResult struct {
	Rubric    string  `json:"rubric"`
	Output    string  `json:"output"`
	Reasoning string  `json:"reasoning"`
	Score     float64 `json:"score"`
	Pass      bool    `json:"pass"`
}

var recordMu sync.Mutex

// RecordScore appends one eval result to evals.jsonl at the current module root.
func RecordScore(e *EvalT, result *EvalResult) {
	e.Helper()

	root, err := FindModuleRoot("")
	if err != nil {
		e.Fatalf("find module root: %v", err)
	}

	line := EvalLogLine{
		Name:      e.Name(),
		Timestamp: time.Now().Format(time.RFC3339),
		RunNumber: e.runNumber,
		Provider:  e.ProviderSpec.ProviderID,
		Model:     e.ProviderSpec.ModelID,
		Grader:    e.GraderSpec.ProviderID + "/" + e.GraderSpec.ModelID,
		Rubric:    result.Rubric,
		Output:    result.Output,
		Reasoning: result.Reasoning,
		Score:     result.Score,
		Pass:      result.Pass,
	}

	// Marshal to a single buffer and emit it with one Write. go test runs
	// packages as separate processes that all append to this file, so the
	// in-process mutex alone cannot prevent interleaving — a single O_APPEND
	// write keeps each JSONL row intact (atomic for the common small-row case).
	data, err := json.Marshal(line)
	if err != nil {
		e.Fatalf("marshal eval result: %v", err)
	}
	data = append(data, '\n')

	recordMu.Lock()
	defer recordMu.Unlock()

	file, err := os.OpenFile(filepath.Join(root, "evals.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		e.Fatalf("open evals.jsonl: %v", err)
	}
	defer file.Close()

	if _, err := file.Write(data); err != nil {
		e.Fatalf("write evals.jsonl: %v", err)
	}
}

// FindModuleRoot finds the nearest parent directory containing go.mod.
func FindModuleRoot(start string) (string, error) {
	dir := start
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	dir = filepath.Clean(dir)

	for {
		if stat, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil && !stat.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("module root not found from %s", start)
		}
		dir = parent
	}
}
