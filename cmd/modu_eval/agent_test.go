package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAgentTask(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "task.md")
	content := `---
id: sample_task
name: Sample Task
category: coding
timeout_seconds: 12
workspace_files:
  - path: "input.txt"
    content: "hello"
checks:
  - name: answer mentions ok
    type: output_contains
    value: ok
---

## Prompt

Say ok.

## Expected Behavior

The assistant responds.
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write task: %v", err)
	}

	task, err := parseAgentTask(path)
	if err != nil {
		t.Fatalf("parseAgentTask() error = %v", err)
	}
	if task.ID != "sample_task" || task.Name != "Sample Task" {
		t.Fatalf("unexpected task identity: %#v", task)
	}
	if task.Prompt != "Say ok." {
		t.Fatalf("prompt = %q", task.Prompt)
	}
	if len(task.WorkspaceFiles) != 1 || task.WorkspaceFiles[0].Path != "input.txt" {
		t.Fatalf("workspace files = %#v", task.WorkspaceFiles)
	}
	if len(task.Checks) != 1 || task.Checks[0].Type != "output_contains" {
		t.Fatalf("checks = %#v", task.Checks)
	}
}

func TestParseAgentTaskExtractsAutomatedChecks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "task.md")
	content := `---
id: python_grade
---

## Prompt

Say ok.

## Automated Checks

` + "```python" + `
def grade(transcript, workspace_path):
    return {"ok": 1.0}
` + "```" + `
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write task: %v", err)
	}
	task, err := parseAgentTask(path)
	if err != nil {
		t.Fatalf("parseAgentTask() error = %v", err)
	}
	if !strings.Contains(task.GradeCode, "def grade") {
		t.Fatalf("grade code not extracted: %q", task.GradeCode)
	}
}

func TestAgentWorkspaceRejectsUnsafePaths(t *testing.T) {
	dir := t.TempDir()
	if _, err := safeWorkspacePath(dir, "../escape.txt"); err == nil {
		t.Fatal("safeWorkspacePath accepted path escape")
	}
	if _, err := safeWorkspacePath(dir, "/tmp/escape.txt"); err == nil {
		t.Fatal("safeWorkspacePath accepted absolute path")
	}
}

func TestExtractAgentFileArtifacts(t *testing.T) {
	dir := t.TempDir()
	stdout := "done\n```file path=\"nested/out.txt\"\nhello artifact\n```\n"
	artifacts, err := extractAgentFileArtifacts(dir, stdout)
	if err != nil {
		t.Fatalf("extractAgentFileArtifacts() error = %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].Path != "nested/out.txt" {
		t.Fatalf("artifacts = %#v", artifacts)
	}
	data, err := os.ReadFile(filepath.Join(dir, "nested", "out.txt"))
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if string(data) != "hello artifact" {
		t.Fatalf("artifact content = %q", data)
	}
}

func TestRunAgentChecks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "answer.txt"), []byte("modu_code ready\n"), 0644); err != nil {
		t.Fatalf("write answer: %v", err)
	}
	checks := []agentCheck{
		{Name: "responded", Type: "assistant_responded"},
		{Name: "file contains", Type: "file_contains", Path: "answer.txt", Value: "ready"},
		{Name: "tool called", Type: "tool_called", Value: "write"},
	}
	results := runAgentChecks(checks, dir, "", "ok", []string{"read", "write"})
	for _, result := range results {
		if !result.Pass || result.Score != 1.0 {
			t.Fatalf("check failed: %#v", result)
		}
	}
}

func TestRunAgentTaskWithFakeAgent(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-agent.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\n' '{\"type\":\"message_update\",\"message\":\"done\"}'\necho updated > result.txt\n"), 0755); err != nil {
		t.Fatalf("write fake agent: %v", err)
	}
	task := agentTask{
		ID:             "fake",
		Name:           "Fake",
		Prompt:         "write result",
		TimeoutSeconds: 5,
		Checks: []agentCheck{
			{Name: "assistant", Type: "assistant_responded"},
			{Name: "file", Type: "file_contains", Path: "result.txt", Value: "updated"},
		},
	}
	result := runAgentTask(task, agentEvalOptions{
		Agent:          script,
		PromptArg:      "",
		JSONOutput:     true,
		TimeoutSeconds: 10,
	})
	if result.Status != "success" {
		t.Fatalf("status = %s error=%s stdout=%s stderr=%s", result.Status, result.Error, result.Stdout, result.Stderr)
	}
	if strings.TrimSpace(result.AssistantText) != "done" {
		t.Fatalf("assistant text = %q", result.AssistantText)
	}
	if len(result.CheckResults) != 2 {
		t.Fatalf("check results = %#v", result.CheckResults)
	}
}

func TestUsesDefaultGoRunModuCode(t *testing.T) {
	if !usesDefaultGoRunModuCode(agentEvalOptions{
		Agent:     "go",
		AgentArgs: []string{"run", "./cmd/modu_code", "--no-approve"},
	}) {
		t.Fatal("expected default go run modu_code command to be detected")
	}
	if usesDefaultGoRunModuCode(agentEvalOptions{
		Agent:     "go",
		AgentArgs: []string{"run", "./cmd/modu_code", "--no-approve", "--extra"},
	}) {
		t.Fatal("custom go args should not be treated as default")
	}
	if usesDefaultGoRunModuCode(agentEvalOptions{
		Agent:     "modu_code",
		AgentArgs: []string{"--no-approve"},
	}) {
		t.Fatal("installed binary should not be treated as default")
	}
}

func TestRunPythonGrade(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "answer.txt"), []byte("ready\n"), 0644); err != nil {
		t.Fatalf("write answer: %v", err)
	}
	code := `
def grade(transcript, workspace_path):
    from pathlib import Path
    text = Path(workspace_path, "answer.txt").read_text()
    return {
        "file_ready": 1.0 if "ready" in text else 0.0,
        "has_transcript": 1.0 if transcript else 0.0,
    }
`
	results := runPythonGrade(code, []map[string]any{pinchbenchTextMessage("assistant", "ready")}, dir, "")
	if len(results) != 2 {
		t.Fatalf("results = %#v", results)
	}
	for _, result := range results {
		if !result.Pass || result.Score != 1.0 {
			t.Fatalf("python grade failed: %#v", result)
		}
	}
}

func TestRunPythonGradeUsesSourceTaskImportRoot(t *testing.T) {
	repo := t.TempDir()
	helperDir := filepath.Join(repo, "eval")
	if err := os.MkdirAll(helperDir, 0755); err != nil {
		t.Fatalf("mkdir helper: %v", err)
	}
	if err := os.WriteFile(filepath.Join(helperDir, "__init__.py"), []byte(""), 0644); err != nil {
		t.Fatalf("write init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(helperDir, "grader_helpers.py"), []byte("def ok():\n    return 1.0\n"), 0644); err != nil {
		t.Fatalf("write helper: %v", err)
	}
	taskDir := filepath.Join(repo, "eval", "tasks", "suite")
	if err := os.MkdirAll(taskDir, 0755); err != nil {
		t.Fatalf("mkdir task dir: %v", err)
	}
	sourceTask := filepath.Join(taskDir, "task.md")
	if err := os.WriteFile(sourceTask, []byte(""), 0644); err != nil {
		t.Fatalf("write source task: %v", err)
	}

	code := `
def grade(transcript, workspace_path):
    from eval.grader_helpers import ok
    return {"helper": ok()}
`
	results := runPythonGrade(code, []map[string]any{pinchbenchTextMessage("assistant", "ready")}, t.TempDir(), sourceTask)
	if len(results) != 1 || !results[0].Pass {
		t.Fatalf("python grade with source import root failed: %#v", results)
	}
}

func TestSummarizeAgentResults(t *testing.T) {
	results := []agentRunResult{
		{
			TaskID:               "pass",
			Category:             "productivity",
			GradingType:          "deterministic",
			Status:               "success",
			ExecutionTimeSeconds: 2,
			CheckResults: []agentCheckResult{
				{Name: "a", Pass: true, Score: 1},
				{Name: "b", Pass: true, Score: 1},
			},
		},
		{
			TaskID:               "fail",
			Category:             "productivity",
			GradingType:          "llm",
			Status:               "failed",
			ExecutionTimeSeconds: 4,
			CheckResults: []agentCheckResult{
				{Name: "a", Pass: true, Score: 1},
				{Name: "b", Pass: false, Score: 0},
			},
		},
	}

	metrics := summarizeAgentResults(results)
	if metrics.TotalTasks != 2 || metrics.PassedTasks != 1 {
		t.Fatalf("task metrics = %#v", metrics)
	}
	if metrics.TotalChecks != 4 || metrics.PassedChecks != 3 {
		t.Fatalf("check metrics = %#v", metrics)
	}
	if metrics.TotalSeconds != 6 {
		t.Fatalf("total seconds = %v, want 6", metrics.TotalSeconds)
	}
	if len(metrics.ByCategory) != 1 || metrics.ByCategory[0].Name != "productivity" || metrics.ByCategory[0].PassedTasks != 1 {
		t.Fatalf("category metrics = %#v", metrics.ByCategory)
	}
	if len(metrics.ByGradingType) != 2 {
		t.Fatalf("grading metrics = %#v", metrics.ByGradingType)
	}
}

func TestAgentTUIFailureFilter(t *testing.T) {
	model := initialAgentModel([]agentRunResult{
		{TaskID: "pass", Status: "success"},
		{TaskID: "fail", Status: "failed"},
	})
	model.showFailures = true
	filtered := model.filterResults()
	if len(filtered) != 1 || filtered[0].TaskID != "fail" {
		t.Fatalf("filtered = %#v, want only failed task", filtered)
	}
}

func TestAgentTUIRenderIncludesCoreMetrics(t *testing.T) {
	model := initialAgentModel([]agentRunResult{
		{
			TaskID:      "golden_001",
			Name:        "Golden One",
			Category:    "personal_assistant",
			GradingType: "deterministic",
			Status:      "success",
			CheckResults: []agentCheckResult{
				{Name: "assistant", Pass: true, Score: 1},
			},
		},
	})
	model.width = 120
	view := model.renderListView()
	for _, want := range []string{"Core Metrics", "Task pass rate", "Check pass rate", "By Category", "personal_assistant", "golden_001"} {
		if !strings.Contains(view, want) {
			t.Fatalf("rendered view missing %q:\n%s", want, view)
		}
	}
}

func TestAgentLiveTUIUpdatesFromRunMessages(t *testing.T) {
	tasks := []agentTask{
		{ID: "task_one", Name: "Task One", Category: "golden", GradingType: "deterministic"},
		{ID: "task_two", Name: "Task Two", Category: "golden", GradingType: "deterministic"},
	}
	model := initialAgentLiveModel(tasks, "/tmp/results")
	model.width = 120

	initial := model.renderListView()
	for _, want := range []string{"0/2 complete", "PENDING", "task_one", "task_two"} {
		if !strings.Contains(initial, want) {
			t.Fatalf("initial live view missing %q:\n%s", want, initial)
		}
	}

	updated, _ := model.Update(agentTaskStartedMsg{Task: tasks[0], Index: 0, Total: 2})
	model = updated.(agentTUIModel)
	running := model.renderListView()
	for _, want := range []string{"running task_one", "RUNNING"} {
		if !strings.Contains(running, want) {
			t.Fatalf("running live view missing %q:\n%s", want, running)
		}
	}

	updated, _ = model.Update(agentTaskResultMsg{
		Result: agentRunResult{
			TaskID:      "task_one",
			Name:        "Task One",
			Category:    "golden",
			GradingType: "deterministic",
			Status:      "success",
			CheckResults: []agentCheckResult{
				{Name: "assistant", Pass: true, Score: 1},
			},
		},
		Index: 0,
		Total: 2,
	})
	model = updated.(agentTUIModel)
	afterResult := model.renderListView()
	for _, want := range []string{"1/2 complete", "Task pass rate:  100.0% (1/1)", "task_two", "PENDING"} {
		if !strings.Contains(afterResult, want) {
			t.Fatalf("result live view missing %q:\n%s", want, afterResult)
		}
	}
}

func TestRenderAgentCheckDetailsIncludesFailureReason(t *testing.T) {
	result := agentRunResult{
		Status: "failed",
		CheckResults: []agentCheckResult{
			{Name: "file updated", Type: "file_contains", Pass: false, Score: 0, Reason: "missing expected text"},
		},
	}
	got := renderAgentCheckDetails(result)
	if !strings.Contains(got, "[FAIL]") || !strings.Contains(got, "missing expected text") {
		t.Fatalf("check details = %q", got)
	}
}
