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
