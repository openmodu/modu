package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openmodu/modu/pkg/types"
)

func TestModuTUIToolPresenterMapsStartAndEndToSameCall(t *testing.T) {
	presenter := moduTUIToolPresenter{}
	start, ok := presenter.EventNode(types.Event{
		Type:       types.EventTypeToolExecutionStart,
		ToolCallID: "call-1",
		ToolName:   "bash",
		Args:       map[string]any{"command": "go test ./..."},
		BatchSize:  2,
		BatchID:    "batch-1",
	}, "")
	if !ok {
		t.Fatal("tool start was not presented")
	}
	if start.Call.ID != "call-1" || start.Call.Input != "go test ./..." || start.Call.Summary != "Running shell command" {
		t.Fatalf("start node = %#v", start)
	}
	if start.Call.BatchSize != 2 || start.Call.BatchID != "batch-1" || start.Call.Done {
		t.Fatalf("start lifecycle = %#v", start.Call)
	}

	end, ok := presenter.EventNode(types.Event{
		Type:       types.EventTypeToolExecutionEnd,
		ToolCallID: "call-1",
		ToolName:   "bash",
		Result: types.ToolResult{
			Content: []types.ContentBlock{
				&types.TextContent{Type: "text", Text: "ok"},
			},
		},
	}, "")
	if !ok {
		t.Fatal("tool end was not presented")
	}
	if end.Call.ID != start.Call.ID || !end.Call.Done || end.Call.Output != "ok" || end.Call.Summary != "Ran 1 shell command" {
		t.Fatalf("end node = %#v", end)
	}
}

func TestModuTUIToolPresenterCarriesHistoricalArtifact(t *testing.T) {
	node := (moduTUIToolPresenter{}).ResultNode(types.ToolResultMessage{
		Role:       types.RoleToolResult,
		ToolCallID: "call-1",
		ToolName:   "bash",
		Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: "preview"},
		},
		Details: map[string]any{
			"output": map[string]any{
				"artifactId":   "call-1",
				"artifactPath": "/tmp/call-1.output",
				"truncated":    true,
			},
		},
	}, "")

	if !node.Call.Done || node.Call.ArtifactID != "call-1" || node.Call.ArtifactPath != "/tmp/call-1.output" || !node.Call.Truncated {
		t.Fatalf("historical result node = %#v", node)
	}
}

func TestModuTUIToolPresenterBuildsLocalEditDiff(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "main.go")
	if err := os.WriteFile(path, []byte("before\nold\nafter\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	node := (moduTUIToolPresenter{}).CallNode(&types.ToolCallContent{
		Type: "toolCall",
		ID:   "call-1",
		Name: "edit",
		Arguments: map[string]any{
			"path":     "main.go",
			"old_text": "old\n",
			"new_text": "new\n",
		},
	}, cwd)

	if node.Call.Name != "update" || node.Call.Language != "diff" || !node.Call.NoCollapse || !node.Expanded {
		t.Fatalf("edit node = %#v", node)
	}
	for _, want := range []string{"@@ -2,1 +2,1 @@", "- 2  old", "+ 2  new"} {
		if !strings.Contains(node.Call.Code, want) {
			t.Fatalf("edit diff missing %q: %s", want, node.Call.Code)
		}
	}
}
