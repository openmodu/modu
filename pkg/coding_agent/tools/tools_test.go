package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/coding_agent/tools/bash"
	"github.com/openmodu/modu/pkg/coding_agent/tools/common"
	"github.com/openmodu/modu/pkg/coding_agent/tools/edit"
	"github.com/openmodu/modu/pkg/coding_agent/tools/find"
	"github.com/openmodu/modu/pkg/coding_agent/tools/grep"
	"github.com/openmodu/modu/pkg/coding_agent/tools/ls"
	"github.com/openmodu/modu/pkg/coding_agent/tools/planning"
	"github.com/openmodu/modu/pkg/coding_agent/tools/read"
	"github.com/openmodu/modu/pkg/coding_agent/tools/write"
	"github.com/openmodu/modu/pkg/types"
)

func TestTruncateHead(t *testing.T) {
	content := strings.Repeat("line\n", 3000)
	result := common.TruncateHead(content, common.TruncateOptions{MaxLines: 100})
	if !result.WasTruncated {
		t.Fatal("expected truncation")
	}
	if result.KeptLines != 100 {
		t.Fatalf("expected 100 kept lines, got %d", result.KeptLines)
	}
}

func TestTruncateTail(t *testing.T) {
	content := strings.Repeat("line\n", 3000)
	result := common.TruncateTail(content, common.TruncateOptions{MaxLines: 100})
	if !result.WasTruncated {
		t.Fatal("expected truncation")
	}
	if result.KeptLines != 100 {
		t.Fatalf("expected 100 kept lines, got %d", result.KeptLines)
	}
}

func TestTruncateLine(t *testing.T) {
	short := "hello"
	if common.TruncateLine(short, 10) != short {
		t.Fatal("short line should not be truncated")
	}

	long := strings.Repeat("x", 600)
	result := common.TruncateLine(long, 500)
	if len(result) <= 500 {
		// result should be 500 chars + "..."
	}
	if !strings.HasSuffix(result, "...") {
		t.Fatal("truncated line should end with ...")
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{100, "100B"},
		{1024, "1.0KB"},
		{1536, "1.5KB"},
		{1048576, "1.0MB"},
		{1073741824, "1.0GB"},
	}
	for _, tt := range tests {
		got := common.FormatSize(tt.bytes)
		if got != tt.expected {
			t.Errorf("FormatSize(%d) = %s, want %s", tt.bytes, got, tt.expected)
		}
	}
}

func TestExpandPath(t *testing.T) {
	home, _ := os.UserHomeDir()

	tests := []struct {
		input    string
		expected string
	}{
		{"~/test", filepath.Join(home, "test")},
		{"  'quoted'  ", "quoted"},
		{`"double"`, "double"},
	}
	for _, tt := range tests {
		got := common.ExpandPath(tt.input)
		if got != tt.expected {
			t.Errorf("ExpandPath(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestResolveToCwd(t *testing.T) {
	got := common.ResolveToCwd("foo/bar.go", "/tmp/project")
	if got != "/tmp/project/foo/bar.go" {
		t.Errorf("ResolveToCwd = %q, want /tmp/project/foo/bar.go", got)
	}

	got = common.ResolveToCwd("/absolute/path.go", "/tmp/project")
	if got != "/absolute/path.go" {
		t.Errorf("ResolveToCwd = %q, want /absolute/path.go", got)
	}
}

func TestReadTool(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	os.WriteFile(filePath, []byte("line1\nline2\nline3\n"), 0o644)

	tool := read.NewTool(dir)

	if tool.Name() != "read" {
		t.Fatalf("expected name 'read', got %s", tool.Name())
	}

	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path": "test.txt",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, "line1") {
		t.Fatal("expected content to contain 'line1'")
	}
	if !strings.Contains(text, "line2") {
		t.Fatal("expected content to contain 'line2'")
	}
	if strings.Contains(text, "4\t") {
		t.Fatalf("file ending in newline should not render an extra numbered blank line, got: %s", text)
	}
	if !strings.Contains(text, "consider whether it would be considered malware") {
		t.Fatalf("expected cyber risk mitigation reminder, got: %s", text)
	}
}

func TestReadToolAcceptsFilePathAlias(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	os.WriteFile(filePath, []byte("line1\nline2\n"), 0o644)

	tool := read.NewTool(dir)
	args, err := agent.ValidateToolArguments(types.ToolDefinition{
		Name:       tool.Name(),
		Parameters: tool.Parameters(),
	}, types.ToolCallContent{
		Name: "read",
		Arguments: map[string]any{
			"file_path": "test.txt",
		},
	})
	if err != nil {
		t.Fatalf("expected file_path alias to validate: %v", err)
	}

	result, err := tool.Execute(context.Background(), "test-id", args, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); !strings.Contains(text, "line1") {
		t.Fatalf("expected content to contain line1, got: %s", text)
	}
}

func TestReadToolResolvesAlternateMacOSScreenshotSpace(t *testing.T) {
	dir := t.TempDir()
	requested := "Screenshot 2026-06-28 at 10.30.00 AM.png"
	actual := "Screenshot 2026-06-28 at 10.30.00\u202fAM.png"
	if err := os.WriteFile(filepath.Join(dir, actual), []byte("fake png"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := read.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path": requested,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Content) != 1 {
		t.Fatalf("expected one image block, got %#v", result.Content)
	}
	image, ok := result.Content[0].(*types.ImageContent)
	if !ok {
		t.Fatalf("expected alternate screenshot path to read as image, got %#v", result.Content[0])
	}
	if image.MimeType != "image/png" || image.Data == "" {
		t.Fatalf("expected png image content, got mime=%q data=%q", image.MimeType, image.Data)
	}
}

func TestReadToolReadsSVGAsText(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "icon.svg"), []byte("<svg><text>label</text></svg>\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := read.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path": "icon.svg",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, block := range result.Content {
		if _, ok := block.(*types.ImageContent); ok {
			t.Fatalf("expected svg to be read as text, got image block %#v", block)
		}
	}
	text := extractText(result.Content)
	if !strings.Contains(text, "1\t<svg><text>label</text></svg>") {
		t.Fatalf("expected numbered svg text content, got: %s", text)
	}
	if !strings.Contains(text, "consider whether it would be considered malware") {
		t.Fatalf("expected text read mitigation reminder, got: %s", text)
	}
}

func TestReadToolReadsJupyterNotebookCellsAndOutputs(t *testing.T) {
	dir := t.TempDir()
	notebook := map[string]any{
		"metadata": map[string]any{
			"language_info": map[string]any{"name": "python"},
		},
		"cells": []map[string]any{
			{
				"id":        "intro",
				"cell_type": "markdown",
				"source":    []string{"# Title\n", "Some notes"},
			},
			{
				"id":              "calc",
				"cell_type":       "code",
				"execution_count": 1,
				"source":          []string{"print('hi')\n"},
				"outputs": []map[string]any{
					{
						"output_type": "stream",
						"text":        []string{"hi\n"},
					},
					{
						"output_type": "display_data",
						"data": map[string]any{
							"text/plain": "plot",
							"image/png":  "aW1h\nZ2U=",
						},
					},
				},
			},
		},
	}
	data, err := json.Marshal(notebook)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "analysis.ipynb"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	tool := read.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path": "analysis.ipynb",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, `<cell id="intro"><cell_type>markdown</cell_type># Title`) {
		t.Fatalf("expected markdown cell in notebook output, got: %s", text)
	}
	if !strings.Contains(text, `<cell id="calc">print('hi')`) {
		t.Fatalf("expected code cell in notebook output, got: %s", text)
	}
	if !strings.Contains(text, "hi") || !strings.Contains(text, "plot") {
		t.Fatalf("expected notebook text outputs, got: %s", text)
	}

	var image *types.ImageContent
	for _, block := range result.Content {
		if img, ok := block.(*types.ImageContent); ok {
			image = img
			break
		}
	}
	if image == nil {
		t.Fatalf("expected notebook image output block, got %#v", result.Content)
	}
	if image.MimeType != "image/png" || image.Data != "aW1hZ2U=" {
		t.Fatalf("expected normalized PNG image output, got %#v", image)
	}

	details, ok := result.Details.(map[string]any)
	if !ok {
		t.Fatalf("expected details map, got %#v", result.Details)
	}
	if got, ok := details["type"].(string); !ok || got != "notebook" {
		t.Fatalf("expected notebook detail type, got %#v", result.Details)
	}
	if got, ok := details["cells"].(int); !ok || got != 2 {
		t.Fatalf("expected two notebook cells, got %#v", result.Details)
	}
}

func TestReadToolSummarizesLargeJupyterNotebookOutputs(t *testing.T) {
	dir := t.TempDir()
	largeOutput := strings.Repeat("x", 11000)
	notebook := map[string]any{
		"metadata": map[string]any{
			"language_info": map[string]any{"name": "python"},
		},
		"cells": []map[string]any{
			{
				"id":        "large",
				"cell_type": "code",
				"source":    "print('large')\n",
				"outputs": []map[string]any{
					{
						"output_type": "stream",
						"text":        largeOutput,
					},
				},
			},
		},
	}
	data, err := json.Marshal(notebook)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "large-output.ipynb"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	tool := read.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path": "large-output.ipynb",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, `<cell id="large">print('large')`) {
		t.Fatalf("expected notebook cell source to remain visible, got: %s", text)
	}
	if !strings.Contains(text, "Outputs are too large to include") {
		t.Fatalf("expected large-output summary, got: %s", text)
	}
	if !strings.Contains(text, "jq '.cells[0].outputs'") {
		t.Fatalf("expected jq cell output guidance, got: %s", text)
	}
	if strings.Contains(text, strings.Repeat("x", 1000)) {
		t.Fatalf("large notebook output should not be included, got: %s", text)
	}
}

func TestReadToolEmptyFileReturnsReminder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "empty.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	tool := read.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path": "empty.txt",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, "file exists but the contents are empty") {
		t.Fatalf("expected empty-file reminder, got: %s", text)
	}
	if strings.Contains(text, "1\t") {
		t.Fatalf("empty file should not be rendered as a numbered blank line, got: %s", text)
	}
	if strings.Contains(text, "consider whether it would be considered malware") {
		t.Fatalf("empty-file warning should not include content mitigation reminder, got: %s", text)
	}
}

func TestReadToolWithOffset(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	var content strings.Builder
	for i := 1; i <= 10; i++ {
		content.WriteString("line" + strings.Repeat("x", i) + "\n")
	}
	os.WriteFile(filePath, []byte(content.String()), 0o644)

	tool := read.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path":   "test.txt",
		"offset": float64(3),
		"limit":  float64(2),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, "3\t") {
		t.Fatalf("expected line 3 in output, got: %s", text)
	}
}

func TestReadToolAcceptsSemanticNumberStrings(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "range.txt")
	if err := os.WriteFile(filePath, []byte("line1\nline2\nline3\nline4\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := read.NewTool(dir)
	args, err := agent.ValidateToolArguments(types.ToolDefinition{
		Name:       tool.Name(),
		Parameters: tool.Parameters(),
	}, types.ToolCallContent{
		Name: "read",
		Arguments: map[string]any{
			"file_path": "range.txt",
			"offset":    "2",
			"limit":     "2",
		},
	})
	if err != nil {
		t.Fatalf("expected offset/limit string numbers to validate: %v", err)
	}

	result, err := tool.Execute(context.Background(), "test-id", args, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(result.Content)
	if !strings.Contains(text, "2\tline2") || !strings.Contains(text, "3\tline3") {
		t.Fatalf("expected offset/limit string numbers to read lines 2-3, got: %s", text)
	}
	if strings.Contains(text, "1\tline1") || strings.Contains(text, "4\tline4") {
		t.Fatalf("expected offset/limit string numbers to exclude lines outside range, got: %s", text)
	}
}

func TestReadToolOffsetBeyondEOFReturnsReminder(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(filePath, []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := read.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path":   "test.txt",
		"offset": 10,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, "file exists but is shorter than the provided offset (10)") {
		t.Fatalf("expected offset reminder, got: %s", text)
	}
	if !strings.Contains(text, "The file has 2 lines") {
		t.Fatalf("expected total line count, got: %s", text)
	}
}

func TestReadToolPreservesRealBlankLines(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "blank-lines.txt")
	if err := os.WriteFile(filePath, []byte("line1\n\nline3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := read.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path": "blank-lines.txt",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, "2\t\n") {
		t.Fatalf("expected real blank line to be numbered, got: %s", text)
	}
	if strings.Contains(text, "4\t") {
		t.Fatalf("terminal newline should not add a fake blank line, got: %s", text)
	}
}

func TestReadToolRejectsInvalidRangeArguments(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(filePath, []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := read.NewTool(dir)
	def := types.ToolDefinition{
		Name:       tool.Name(),
		Parameters: tool.Parameters(),
	}

	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{
			name: "negative offset",
			args: map[string]any{
				"path":   "test.txt",
				"offset": -1,
			},
			want: "offset must be greater than or equal to 0",
		},
		{
			name: "negative offset string",
			args: map[string]any{
				"path":   "test.txt",
				"offset": "-1",
			},
			want: "offset must be greater than or equal to 0",
		},
		{
			name: "zero limit",
			args: map[string]any{
				"path":  "test.txt",
				"limit": 0,
			},
			want: "limit must be greater than 0",
		},
		{
			name: "zero limit string",
			args: map[string]any{
				"path":  "test.txt",
				"limit": "0",
			},
			want: "limit must be greater than 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := agent.ValidateToolArguments(def, types.ToolCallContent{
				Name:      "read",
				Arguments: tt.args,
			}); err == nil {
				t.Fatalf("expected schema validation to reject args: %#v", tt.args)
			}

			result, err := tool.Execute(context.Background(), "test-id", tt.args, nil)
			if err != nil {
				t.Fatal(err)
			}
			if text := extractText(result.Content); !strings.Contains(text, tt.want) {
				t.Fatalf("expected runtime error %q, got: %s", tt.want, text)
			}
		})
	}
}

func TestReadToolRejectsLargeTextWithoutExplicitLimit(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "large.txt")
	content := strings.Repeat("0123456789\n", common.ReadMaxBytes/10+1)
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := read.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path": "large.txt",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, "file is too large to read at once") {
		t.Fatalf("expected large file error, got: %s", text)
	}
	if !strings.Contains(text, "Use offset and limit") {
		t.Fatalf("expected offset/limit guidance, got: %s", text)
	}
}

func TestReadToolAllowsLargeTextWithExplicitLimit(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "large.txt")
	var content strings.Builder
	for i := 0; i < common.ReadMaxBytes/16; i++ {
		content.WriteString("small line value\n")
	}
	if err := os.WriteFile(filePath, []byte(content.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := read.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path":  "large.txt",
		"limit": 1,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if strings.Contains(text, "file is too large") {
		t.Fatalf("did not expect large file error with explicit limit, got: %s", text)
	}
	if !strings.Contains(text, "1\tsmall line value") {
		t.Fatalf("expected first line, got: %s", text)
	}
}

func TestReadToolRejectsBlockedDevicePath(t *testing.T) {
	if _, err := os.Stat("/dev/zero"); err != nil {
		t.Skip("/dev/zero is not available on this platform")
	}

	tool := read.NewTool(t.TempDir())
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path": "/dev/zero",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, "would block or produce infinite output") {
		t.Fatalf("expected blocked device warning, got: %s", text)
	}
}

func TestReadToolRejectsBinaryContent(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "payload.dat")
	if err := os.WriteFile(filePath, []byte{'m', 'o', 'd', 'u', 0, 1, 2, 3}, 0o644); err != nil {
		t.Fatal(err)
	}

	tool := read.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path": "payload.dat",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if text := extractText(result.Content); !strings.Contains(text, "cannot read binary files") {
		t.Fatalf("expected binary file error, got: %s", text)
	}
}

func TestReadToolRejectsBinaryExtension(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "archive.zip")
	if err := os.WriteFile(filePath, []byte("not actually zipped"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := read.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path": "archive.zip",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, "binary .zip file") {
		t.Fatalf("expected binary extension error, got: %s", text)
	}
}

func TestWriteTool(t *testing.T) {
	dir := t.TempDir()
	tool := write.NewTool(dir)

	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path":    "newfile.txt",
		"content": "hello world",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, "File created successfully at: newfile.txt") {
		t.Fatalf("expected create success message, got: %s", text)
	}
	details, ok := result.Details.(map[string]any)
	if !ok {
		t.Fatalf("expected details map, got %#v", result.Details)
	}
	if got := details["type"]; got != "create" {
		t.Fatalf("expected create details type, got: %#v", got)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "newfile.txt"))
	if string(data) != "hello world" {
		t.Fatalf("file content mismatch: %s", string(data))
	}
}

func TestWriteToolRequiresContentButAllowsExplicitEmptyString(t *testing.T) {
	dir := t.TempDir()
	tool := write.NewTool(dir)

	missingResult, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path": "missing-content.txt",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	missingText := extractText(missingResult.Content)
	if !strings.Contains(missingText, "content is required") {
		t.Fatalf("expected missing content error, got: %s", missingText)
	}
	if _, err := os.Stat(filepath.Join(dir, "missing-content.txt")); !os.IsNotExist(err) {
		t.Fatalf("missing content should not create file, stat err: %v", err)
	}

	emptyResult, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path":    "empty.txt",
		"content": "",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(emptyResult.Content); !strings.Contains(text, "File created successfully at: empty.txt") {
		t.Fatalf("expected explicit empty content write to succeed, got: %s", text)
	}
	data, err := os.ReadFile(filepath.Join(dir, "empty.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Fatalf("expected empty file content, got: %q", string(data))
	}
}

func TestWriteToolReportsUpdateForExistingFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := write.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path":    "existing.txt",
		"content": "new",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if text := extractText(result.Content); !strings.Contains(text, "The file existing.txt has been updated successfully.") {
		t.Fatalf("expected update success message, got: %s", text)
	}
	details, ok := result.Details.(map[string]any)
	if !ok {
		t.Fatalf("expected details map, got %#v", result.Details)
	}
	if got := details["type"]; got != "update" {
		t.Fatalf("expected update details type, got: %#v", got)
	}
	data, err := os.ReadFile(filepath.Join(dir, "existing.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Fatalf("file content mismatch: %s", string(data))
	}
}

func TestWriteToolSubdirectory(t *testing.T) {
	dir := t.TempDir()
	tool := write.NewTool(dir)

	_, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path":    "sub/dir/file.txt",
		"content": "nested content",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "sub", "dir", "file.txt"))
	if string(data) != "nested content" {
		t.Fatalf("file content mismatch: %s", string(data))
	}
}

func TestWriteToolRejectsDirectoryTarget(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "target"), 0o755); err != nil {
		t.Fatal(err)
	}

	tool := write.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path":    "target",
		"content": "not a file",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, "target is a directory, not a file") {
		t.Fatalf("expected directory target error, got: %s", text)
	}
	if _, err := os.ReadDir(filepath.Join(dir, "target")); err != nil {
		t.Fatalf("target directory should remain readable: %v", err)
	}
}

func TestWriteToolAcceptsFilePathAlias(t *testing.T) {
	dir := t.TempDir()
	tool := write.NewTool(dir)
	args, err := agent.ValidateToolArguments(types.ToolDefinition{
		Name:       tool.Name(),
		Parameters: tool.Parameters(),
	}, types.ToolCallContent{
		Name: "write",
		Arguments: map[string]any{
			"file_path": "alias.txt",
			"content":   "alias content",
		},
	})
	if err != nil {
		t.Fatalf("expected file_path alias to validate: %v", err)
	}

	result, err := tool.Execute(context.Background(), "test-id", args, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); !strings.Contains(text, "File created successfully at: alias.txt") {
		t.Fatalf("expected success, got: %s", text)
	}

	data, err := os.ReadFile(filepath.Join(dir, "alias.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "alias content" {
		t.Fatalf("file content mismatch: %s", string(data))
	}
}

func TestEditTool(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "edit_test.go")
	os.WriteFile(filePath, []byte("func hello() {\n\tfmt.Println(\"hello\")\n}\n"), 0o644)

	tool := edit.NewTool(dir)

	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path":     "edit_test.go",
		"old_text": "hello",
		"new_text": "world",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	// Should fail because "hello" appears multiple times
	if !strings.Contains(text, "appears") {
		t.Fatalf("expected ambiguity error, got: %s", text)
	}

	// Try with unique text
	result, err = tool.Execute(context.Background(), "test-id", map[string]any{
		"path":     "edit_test.go",
		"old_text": "fmt.Println(\"hello\")",
		"new_text": "fmt.Println(\"world\")",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text = extractText(result.Content)
	if !strings.Contains(text, "Successfully") {
		t.Fatalf("expected success, got: %s", text)
	}

	data, _ := os.ReadFile(filePath)
	if !strings.Contains(string(data), "world") {
		t.Fatal("edit not applied")
	}
}

func TestEditToolReplaceAll(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	os.WriteFile(filePath, []byte("foo bar foo baz foo"), 0o644)

	tool := edit.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path":        "test.txt",
		"old_text":    "foo",
		"new_text":    "qux",
		"replace_all": true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, "3 replacement") {
		t.Fatalf("expected 3 replacements, got: %s", text)
	}

	data, _ := os.ReadFile(filePath)
	if strings.Contains(string(data), "foo") {
		t.Fatal("still contains 'foo'")
	}
}

func TestEditToolAcceptsReplaceAllStringBoolean(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "replace_all_string.txt")
	if err := os.WriteFile(filePath, []byte("alpha beta alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := edit.NewTool(dir)
	args, err := agent.ValidateToolArguments(types.ToolDefinition{
		Name:       tool.Name(),
		Parameters: tool.Parameters(),
	}, types.ToolCallContent{
		Name: "edit",
		Arguments: map[string]any{
			"file_path":   "replace_all_string.txt",
			"old_string":  "alpha",
			"new_string":  "gamma",
			"replace_all": "true",
		},
	})
	if err != nil {
		t.Fatalf("expected replace_all string boolean to validate: %v", err)
	}

	result, err := tool.Execute(context.Background(), "test-id", args, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); !strings.Contains(text, "2 replacement") {
		t.Fatalf("expected two replacements, got: %s", text)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "gamma beta gamma\n" {
		t.Fatalf("expected replace_all string boolean to replace all matches, got: %q", got)
	}
}

func TestEditToolAcceptsClaudeAliases(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "alias.txt")
	if err := os.WriteFile(filePath, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := edit.NewTool(dir)
	args, err := agent.ValidateToolArguments(types.ToolDefinition{
		Name:       tool.Name(),
		Parameters: tool.Parameters(),
	}, types.ToolCallContent{
		Name: "edit",
		Arguments: map[string]any{
			"file_path":  "alias.txt",
			"old_string": "before",
			"new_string": "after",
		},
	})
	if err != nil {
		t.Fatalf("expected Claude edit aliases to validate: %v", err)
	}

	result, err := tool.Execute(context.Background(), "test-id", args, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); !strings.Contains(text, "Successfully") {
		t.Fatalf("expected success, got: %s", text)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "after\n" {
		t.Fatalf("file content mismatch: %q", string(data))
	}
}

func TestEditToolRejectsDirectoryTarget(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "target"), 0o755); err != nil {
		t.Fatal(err)
	}

	tool := edit.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path":     "target",
		"old_text": "before",
		"new_text": "after",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, "target is a directory, not a file") {
		t.Fatalf("expected directory target error, got: %s", text)
	}
}

func TestEditToolRejectsJupyterNotebookTarget(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "notebook.ipynb")
	if err := os.WriteFile(filePath, []byte(`{"cells":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := edit.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path":     "notebook.ipynb",
		"old_text": "cells",
		"new_text": "items",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, "Jupyter Notebook") {
		t.Fatalf("expected notebook target rejection, got: %s", text)
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != `{"cells":[]}` {
		t.Fatalf("expected notebook to remain unchanged, got: %s", got)
	}
}

func TestEditToolEmptyOldStringCreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "nested", "created.txt")

	tool := edit.NewTool(dir)
	args, err := agent.ValidateToolArguments(types.ToolDefinition{
		Name:       tool.Name(),
		Parameters: tool.Parameters(),
	}, types.ToolCallContent{
		Name: "edit",
		Arguments: map[string]any{
			"file_path":  "nested/created.txt",
			"old_string": "",
			"new_string": "created\n",
		},
	})
	if err != nil {
		t.Fatalf("expected empty old_string to validate: %v", err)
	}

	result, err := tool.Execute(context.Background(), "test-id", args, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); !strings.Contains(text, "Successfully") {
		t.Fatalf("expected success, got: %s", text)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "created\n" {
		t.Fatalf("expected created file content, got: %q", got)
	}
}

func TestEditToolEmptyOldStringWritesEmptyFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(filePath, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	tool := edit.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path":     "empty.txt",
		"old_text": "",
		"new_text": "filled\n",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); !strings.Contains(text, "Successfully") {
		t.Fatalf("expected success, got: %s", text)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "filled\n" {
		t.Fatalf("expected empty file to be filled, got: %q", got)
	}
}

func TestEditToolEmptyOldStringRejectsExistingContent(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "existing.txt")
	original := "already here\n"
	if err := os.WriteFile(filePath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := edit.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path":     "existing.txt",
		"old_text": "",
		"new_text": "replacement\n",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); !strings.Contains(text, "Cannot create new file - file already exists") {
		t.Fatalf("expected existing file rejection, got: %s", text)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != original {
		t.Fatalf("expected existing content to remain unchanged, got: %q", got)
	}
}

func TestEditToolRejectsNoOpEdit(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	original := "hello world\n"
	if err := os.WriteFile(filePath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := edit.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path":     "test.txt",
		"old_text": "hello",
		"new_text": "hello",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, "No changes to make") {
		t.Fatalf("expected no-op edit error, got: %s", text)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Fatalf("file changed after no-op edit: %q", string(data))
	}
}

func TestEditToolRejectsOversizedFileBeforeReading(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "huge.txt")
	if err := os.WriteFile(filePath, []byte("small prefix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(filePath, int64(1024*1024*1024+1)); err != nil {
		t.Fatal(err)
	}

	tool := edit.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path":     "huge.txt",
		"old_text": "small prefix",
		"new_text": "replacement",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, "File is too large to edit") {
		t.Fatalf("expected oversized file rejection, got: %s", text)
	}
}

func TestEditToolEmptyReplacementRemovesTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "delete_line.txt")
	if err := os.WriteFile(filePath, []byte("first\nremove me\nlast\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := edit.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path":     "delete_line.txt",
		"old_text": "remove me",
		"new_text": "",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); !strings.Contains(text, "Successfully") {
		t.Fatalf("expected success, got: %s", text)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "first\nlast\n" {
		t.Fatalf("expected line deletion without blank line, got: %q", got)
	}
}

func TestEditToolReplaceAllEmptyReplacementRemovesTrailingNewlines(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "delete_lines.txt")
	if err := os.WriteFile(filePath, []byte("drop\nkeep\ndrop\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := edit.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path":        "delete_lines.txt",
		"old_text":    "drop",
		"new_text":    "",
		"replace_all": true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); !strings.Contains(text, "2 replacement") {
		t.Fatalf("expected two replacements, got: %s", text)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "keep\n" {
		t.Fatalf("expected replace_all line deletion without blank lines, got: %q", got)
	}
}

func TestEditToolReplaceAllEmptyReplacementKeepsReportedCountHonest(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "mixed_delete.txt")
	if err := os.WriteFile(filePath, []byte("drop\nkeep drop"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := edit.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path":        "mixed_delete.txt",
		"old_text":    "drop",
		"new_text":    "",
		"replace_all": true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); !strings.Contains(text, "2 replacement") {
		t.Fatalf("expected two replacements, got: %s", text)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "keep " {
		t.Fatalf("expected all matches deleted with only whole-line newline stripped, got: %q", got)
	}
}

func TestEditToolFuzzyMatchPreservesUnmatchedOriginalContent(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "quotes.go")
	original := "package main\n\nvar title = \u201ckeep smart quotes\u201d\nvar message = \u201chello\u201d  \n"
	if err := os.WriteFile(filePath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := edit.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path":     "quotes.go",
		"old_text": "var message = \"hello\"",
		"new_text": "var message = \"world\"",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, "using fuzzy match") {
		t.Fatalf("expected fuzzy match success, got: %s", text)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "var title = \u201ckeep smart quotes\u201d") {
		t.Fatalf("unmatched smart quotes were modified: %q", got)
	}
	if !strings.Contains(got, "var message = \u201cworld\u201d") {
		t.Fatalf("expected message replacement to preserve smart quotes, got: %q", got)
	}
}

func TestEditToolFuzzyMatchPreservesSingleQuoteStyle(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "single_quotes.txt")
	original := "msg = \u2018don\u2019t stop\u2019\n"
	if err := os.WriteFile(filePath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := edit.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path":     "single_quotes.txt",
		"old_text": "msg = 'don't stop'",
		"new_text": "msg = 'don't wait'",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); !strings.Contains(text, "using fuzzy match") {
		t.Fatalf("expected fuzzy match success, got: %s", text)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "msg = \u2018don\u2019t wait\u2019\n" {
		t.Fatalf("expected single quote style preservation, got: %q", got)
	}
}

func TestBashTool(t *testing.T) {
	tool := bash.NewTool(t.TempDir())

	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"command": "echo hello && echo world",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, "hello") || !strings.Contains(text, "world") {
		t.Fatalf("expected hello world, got: %s", text)
	}
}

func TestBashToolExitCode(t *testing.T) {
	tool := bash.NewTool(t.TempDir())

	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"command": "exit 42",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, "42") {
		t.Fatalf("expected exit code 42, got: %s", text)
	}
}

func TestBashToolTreatsClaudeTimeoutAsMilliseconds(t *testing.T) {
	tool := bash.NewTool(t.TempDir())
	start := time.Now()

	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"command": "tail -f /dev/null",
		"timeout": 1000,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)

	text := extractText(result.Content)
	if !strings.Contains(text, "timed out after 1 seconds") {
		t.Fatalf("expected 1000 timeout to mean one second, got: %s", text)
	}
	if elapsed > 1800*time.Millisecond {
		t.Fatalf("expected command to time out around one second, elapsed: %s", elapsed)
	}
}

func TestBashToolAcceptsTimeoutMSAlias(t *testing.T) {
	tool := bash.NewTool(t.TempDir())
	args, err := agent.ValidateToolArguments(types.ToolDefinition{
		Name:       tool.Name(),
		Parameters: tool.Parameters(),
	}, types.ToolCallContent{
		Name: "bash",
		Arguments: map[string]any{
			"command":    "echo done",
			"timeout_ms": 1000,
		},
	})
	if err != nil {
		t.Fatalf("expected timeout_ms alias to validate: %v", err)
	}

	result, err := tool.Execute(context.Background(), "test-id", args, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); !strings.Contains(text, "done") {
		t.Fatalf("expected command output, got: %s", text)
	}
}

func TestBashToolAcceptsSemanticNumberTimeoutStrings(t *testing.T) {
	tool := bash.NewTool(t.TempDir())
	args, err := agent.ValidateToolArguments(types.ToolDefinition{
		Name:       tool.Name(),
		Parameters: tool.Parameters(),
	}, types.ToolCallContent{
		Name: "bash",
		Arguments: map[string]any{
			"command":    "tail -f /dev/null",
			"timeout_ms": "1000",
		},
	})
	if err != nil {
		t.Fatalf("expected timeout_ms string number to validate: %v", err)
	}

	start := time.Now()
	result, err := tool.Execute(context.Background(), "test-id", args, nil)
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)

	if text := extractText(result.Content); !strings.Contains(text, "timed out after 1 seconds") {
		t.Fatalf("expected timeout_ms string number to mean one second, got: %s", text)
	}
	if elapsed > 1800*time.Millisecond {
		t.Fatalf("expected command to time out around one second, elapsed: %s", elapsed)
	}
}

func TestBashToolBlocksForegroundSleep(t *testing.T) {
	tool := bash.NewTool(t.TempDir())
	start := time.Now()

	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"command": "sleep 2",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)

	text := extractText(result.Content)
	if !strings.Contains(text, "Blocked: standalone sleep 2") {
		t.Fatalf("expected foreground sleep block, got: %s", text)
	}
	if !strings.Contains(text, "run_in_background=true") {
		t.Fatalf("expected background guidance, got: %s", text)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("expected sleep to be blocked before execution, elapsed: %s", elapsed)
	}
}

func TestBashToolBlocksDecimalAndSuffixedForegroundSleep(t *testing.T) {
	tool := bash.NewTool(t.TempDir())

	tests := []struct {
		name        string
		command     string
		wantMessage string
	}{
		{
			name:        "decimal",
			command:     "sleep 2.5",
			wantMessage: "Blocked: standalone sleep 2.5",
		},
		{
			name:        "seconds suffix",
			command:     "sleep 2s && echo done",
			wantMessage: "Blocked: sleep 2s followed by: echo done",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()

			result, err := tool.Execute(context.Background(), "test-id", map[string]any{
				"command": tt.command,
			}, nil)
			if err != nil {
				t.Fatal(err)
			}

			text := extractText(result.Content)
			if !strings.Contains(text, tt.wantMessage) {
				t.Fatalf("expected foreground sleep block %q, got: %s", tt.wantMessage, text)
			}
			if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
				t.Fatalf("expected sleep to be blocked before execution, elapsed: %s", elapsed)
			}
		})
	}
}

func TestBashToolAcceptsRunInBackgroundAlias(t *testing.T) {
	tool := bash.NewTool(t.TempDir())
	args, err := agent.ValidateToolArguments(types.ToolDefinition{
		Name:       tool.Name(),
		Parameters: tool.Parameters(),
	}, types.ToolCallContent{
		Name: "bash",
		Arguments: map[string]any{
			"command":           "true",
			"run_in_background": true,
		},
	})
	if err != nil {
		t.Fatalf("expected run_in_background alias to validate: %v", err)
	}

	result, err := tool.Execute(context.Background(), "test-id", args, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(result.Content)
	if !strings.Contains(text, "Process started in background") {
		t.Fatalf("expected background process message, got: %s", text)
	}
	details, ok := result.Details.(map[string]any)
	if !ok {
		t.Fatalf("expected details map, got %#v", result.Details)
	}
	if got, ok := details["background"].(bool); !ok || !got {
		t.Fatalf("expected background detail true, got %#v", result.Details)
	}
}

func TestBashToolAcceptsSemanticBooleanBackgroundStrings(t *testing.T) {
	tool := bash.NewTool(t.TempDir())
	args, err := agent.ValidateToolArguments(types.ToolDefinition{
		Name:       tool.Name(),
		Parameters: tool.Parameters(),
	}, types.ToolCallContent{
		Name: "bash",
		Arguments: map[string]any{
			"command":           "true",
			"run_in_background": "true",
		},
	})
	if err != nil {
		t.Fatalf("expected run_in_background string boolean to validate: %v", err)
	}

	result, err := tool.Execute(context.Background(), "test-id", args, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); !strings.Contains(text, "Process started in background") {
		t.Fatalf("expected background process message, got: %s", text)
	}
}

func TestBashToolAcceptsDangerouslyDisableSandboxCompatibilityFlag(t *testing.T) {
	tool := bash.NewTool(t.TempDir())
	for _, value := range []any{true, "false"} {
		args, err := agent.ValidateToolArguments(types.ToolDefinition{
			Name:       tool.Name(),
			Parameters: tool.Parameters(),
		}, types.ToolCallContent{
			Name: "bash",
			Arguments: map[string]any{
				"command":                   "echo ok",
				"dangerouslyDisableSandbox": value,
			},
		})
		if err != nil {
			t.Fatalf("expected dangerouslyDisableSandbox=%#v to validate: %v", value, err)
		}

		result, err := tool.Execute(context.Background(), "test-id", args, nil)
		if err != nil {
			t.Fatal(err)
		}
		if text := extractText(result.Content); !strings.Contains(text, "ok") {
			t.Fatalf("expected command output with dangerouslyDisableSandbox=%#v, got: %s", value, text)
		}
	}
}

func TestBashToolAllowsBackgroundSleep(t *testing.T) {
	tool := bash.NewTool(t.TempDir())

	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"command":           "sleep 2",
		"run_in_background": true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); !strings.Contains(text, "Process started in background") {
		t.Fatalf("expected background sleep to start, got: %s", text)
	}
}

func TestBashToolBlocksSedInPlaceEdits(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "sed.txt")
	if err := os.WriteFile(filePath, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := bash.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"command": "sed -i '' 's/before/after/' sed.txt",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, "in-place sed edits must use the edit tool") {
		t.Fatalf("expected sed edit block, got: %s", text)
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "before\n" {
		t.Fatalf("file changed despite sed edit block: %q", string(data))
	}
}

func TestBashToolAllowsReadOnlySedTransform(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sed.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := bash.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"command": "sed 's/before/after/' sed.txt",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); !strings.Contains(text, "after") {
		t.Fatalf("expected read-only sed output, got: %s", text)
	}
}

func TestBashToolBlocksSimpleFileReads(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secret file.txt"), []byte("secret spaced\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := bash.NewTool(dir)
	for _, command := range []string{
		"cat secret.txt",
		`cat "secret file.txt"`,
		"cat -n secret.txt",
		"head -n 1 secret.txt",
		`head -n 1 "secret file.txt"`,
		"head -1 secret.txt",
		"tail --lines=1 secret.txt",
		`tail --lines=1 "secret file.txt"`,
		"tac secret.txt",
		`tac "secret file.txt"`,
		"nl secret.txt",
		`nl "secret file.txt"`,
		"less secret.txt",
		`less "secret file.txt"`,
		"more secret.txt",
		`more "secret file.txt"`,
	} {
		t.Run(command, func(t *testing.T) {
			result, err := tool.Execute(context.Background(), "test-id", map[string]any{
				"command": command,
			}, nil)
			if err != nil {
				t.Fatal(err)
			}

			text := extractText(result.Content)
			if !strings.Contains(text, "use the read tool for normal file reads") {
				t.Fatalf("expected simple file read block, got: %s", text)
			}
			if strings.Contains(text, "secret\n") {
				t.Fatalf("blocked read should not expose file content, got: %s", text)
			}
			if strings.Contains(text, "secret spaced\n") {
				t.Fatalf("blocked read should not expose spaced-path file content, got: %s", text)
			}
		})
	}
}

func TestBashToolAllowsComplexCatPipelines(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "data.txt"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := bash.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"command": "cat data.txt | wc -l",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if text := extractText(result.Content); !strings.Contains(text, "2") {
		t.Fatalf("expected complex pipeline to run, got: %s", text)
	}
}

func TestBashToolBlocksSimpleContentSearches(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "data.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data file.txt"), []byte("needle spaced\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := bash.NewTool(dir)
	for _, command := range []string{
		"grep needle data.txt",
		`grep needle "data file.txt"`,
		"grep -i needle data.txt",
		"grep -R -n needle .",
		"grep -e needle data.txt",
		`grep 'need.*' data.txt`,
		"grep --context=2 needle data.txt",
		"rg needle data.txt",
		`rg needle "data file.txt"`,
		"rg needle",
		"rg -n needle data.txt",
		"rg -C 2 needle data.txt",
		"rg --type go needle",
		"rg -g '*.go' needle",
		"rg --glob=*.go needle",
		"sed -n '/needle/p' data.txt",
		`sed -n '/needle/p' "data file.txt"`,
		"sed -En '/need.*/p' data.txt",
		"awk '/needle/' data.txt",
		`awk '/needle/' "data file.txt"`,
		"awk '/need.*/ {print}' data.txt",
	} {
		t.Run(command, func(t *testing.T) {
			result, err := tool.Execute(context.Background(), "test-id", map[string]any{
				"command": command,
			}, nil)
			if err != nil {
				t.Fatal(err)
			}

			text := extractText(result.Content)
			if !strings.Contains(text, "use the grep tool for normal content search") {
				t.Fatalf("expected simple content search block, got: %s", text)
			}
			if strings.Contains(text, "needle\n") {
				t.Fatalf("blocked search should not expose file content, got: %s", text)
			}
			if strings.Contains(text, "needle spaced\n") {
				t.Fatalf("blocked search should not expose spaced-path file content, got: %s", text)
			}
		})
	}
}

func TestBashToolAllowsComplexGrepPipelines(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "data.txt"), []byte("needle\nother\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := bash.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"command": "grep needle data.txt | wc -l",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if text := extractText(result.Content); !strings.Contains(text, "1") {
		t.Fatalf("expected complex grep pipeline to run, got: %s", text)
	}
}

func TestBashToolBlocksSimpleFilePatternSearches(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "space dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "space dir", "nested.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := bash.NewTool(dir)
	for _, command := range []string{
		"find . -name '*.go'",
		`find "space dir" -name '*.go'`,
		"find . -type f -name '*.go'",
		"find . -maxdepth 2 -iname '*_test.go' -print",
		"fd '*.go'",
		`fd '*.go' "space dir"`,
		"fd --hidden '*.go' .",
	} {
		t.Run(command, func(t *testing.T) {
			result, err := tool.Execute(context.Background(), "test-id", map[string]any{
				"command": command,
			}, nil)
			if err != nil {
				t.Fatal(err)
			}

			text := extractText(result.Content)
			if !strings.Contains(text, "use the find tool for normal file-name searches") {
				t.Fatalf("expected simple file-pattern search block, got: %s", text)
			}
			if strings.Contains(text, "main.go") {
				t.Fatalf("blocked search should not expose file names, got: %s", text)
			}
			if strings.Contains(text, "nested.go") {
				t.Fatalf("blocked search should not expose spaced-path file names, got: %s", text)
			}
		})
	}
}

func TestBashToolAllowsComplexFindCommands(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := bash.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"command": "find . -name '*.go' -exec basename {} \\;",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if text := extractText(result.Content); !strings.Contains(text, "main.go") {
		t.Fatalf("expected complex find command to run, got: %s", text)
	}
}

func TestBashToolBlocksSimpleDirectoryListings(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "visible.txt"), []byte("content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "space dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "space dir", "inside.txt"), []byte("content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := bash.NewTool(dir)
	for _, command := range []string{
		"ls",
		"ls .",
		"ls -la",
		"ls -A sub",
		`ls "space dir"`,
	} {
		t.Run(command, func(t *testing.T) {
			result, err := tool.Execute(context.Background(), "test-id", map[string]any{
				"command": command,
			}, nil)
			if err != nil {
				t.Fatal(err)
			}

			text := extractText(result.Content)
			if !strings.Contains(text, "use the ls tool for normal directory listings") {
				t.Fatalf("expected simple ls block, got: %s", text)
			}
			if strings.Contains(text, "visible.txt") {
				t.Fatalf("blocked ls should not expose directory content, got: %s", text)
			}
			if strings.Contains(text, "inside.txt") {
				t.Fatalf("blocked ls should not expose spaced-path directory content, got: %s", text)
			}
		})
	}
}

func TestBashToolAllowsComplexLsPipelines(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "visible.txt"), []byte("content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := bash.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"command": "ls . | wc -l",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if text := extractText(result.Content); !strings.Contains(text, "1") {
		t.Fatalf("expected complex ls pipeline to run, got: %s", text)
	}
}

func TestLsTool(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(dir, "b.go"), []byte(""), 0o644)
	os.MkdirAll(filepath.Join(dir, "subdir"), 0o755)

	tool := ls.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, "a.txt") {
		t.Fatal("expected a.txt in listing")
	}
	if !strings.Contains(text, "subdir/") {
		t.Fatal("expected subdir/ in listing")
	}
}

func TestLsToolIgnorePatterns(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"app.go", "debug.log", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"build", "vendor", "src"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	tool := ls.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"ignore": []any{"*.log", "build/", "vendor/**"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	for _, unwanted := range []string{"debug.log", "build/", "vendor/"} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("expected %s to be ignored, got: %s", unwanted, text)
		}
	}
	for _, want := range []string{"app.go", "notes.txt", "src/"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %s to remain listed, got: %s", want, text)
		}
	}

	stringIgnoreArgs := map[string]any{"ignore": "*.log"}
	if _, err := agent.ValidateToolArguments(types.ToolDefinition{
		Name:       tool.Name(),
		Parameters: tool.Parameters(),
	}, types.ToolCallContent{
		Name:      "ls",
		Arguments: stringIgnoreArgs,
	}); err != nil {
		t.Fatalf("expected string ignore pattern to validate: %v", err)
	}
	stringIgnoreResult, err := tool.Execute(context.Background(), "test-id", stringIgnoreArgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	stringIgnoreText := extractText(stringIgnoreResult.Content)
	if strings.Contains(stringIgnoreText, "debug.log") {
		t.Fatalf("expected string ignore pattern to hide debug.log, got: %s", stringIgnoreText)
	}
	if !strings.Contains(stringIgnoreText, "app.go") {
		t.Fatalf("expected string ignore pattern to keep app.go, got: %s", stringIgnoreText)
	}

	limitedResult, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"ignore": []any{"*.log", "build/", "vendor/**"},
		"limit":  2,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	limitedText := extractText(limitedResult.Content)
	if !strings.Contains(limitedText, "3 entries total") {
		t.Fatalf("expected truncation count after ignore filtering, got: %s", limitedText)
	}

	stringLimitArgs := map[string]any{"limit": "2"}
	if _, err := agent.ValidateToolArguments(types.ToolDefinition{
		Name:       tool.Name(),
		Parameters: tool.Parameters(),
	}, types.ToolCallContent{
		Name:      "ls",
		Arguments: stringLimitArgs,
	}); err != nil {
		t.Fatalf("expected string limit to validate: %v", err)
	}
	stringLimitResult, err := tool.Execute(context.Background(), "test-id", stringLimitArgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	stringLimitText := extractText(stringLimitResult.Content)
	if !strings.Contains(stringLimitText, "6 entries total, showing first 2") {
		t.Fatalf("expected string limit to cap listing, got: %s", stringLimitText)
	}
}

func TestLsToolRejectsNonDirectoryPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := ls.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path": "a.txt",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); !strings.Contains(text, "path is not a directory") {
		t.Fatalf("expected non-directory error, got: %s", text)
	}
}

func TestLsToolRejectsMissingDirectory(t *testing.T) {
	dir := t.TempDir()
	tool := ls.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path": "missing",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); !strings.Contains(text, "directory not found") {
		t.Fatalf("expected missing directory error, got: %s", text)
	}
}

func TestGrepTool(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.go"), []byte("func main() {\n\tfmt.Println(\"hello\")\n}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "other.txt"), []byte("nothing here\n"), 0o644)

	tool := grep.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"pattern": "Println",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, "test.go") {
		t.Fatalf("expected default files_with_matches output to include test.go, got: %s", text)
	}
	if strings.Contains(text, "Println") {
		t.Fatalf("default grep output should not include matching content, got: %s", text)
	}
}

func TestGrepToolOutputModes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one.txt"), []byte("needle\nneedle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "two.txt"), []byte("needle\nhaystack\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "none.txt"), []byte("haystack\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := grep.NewTool(dir)

	filesResult, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"pattern":     "needle",
		"output_mode": "files_with_matches",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	filesText := extractText(filesResult.Content)
	if !strings.Contains(filesText, "Found 2 file(s)") {
		t.Fatalf("expected files summary, got: %s", filesText)
	}
	if !strings.Contains(filesText, "one.txt") || !strings.Contains(filesText, "two.txt") {
		t.Fatalf("expected matching filenames, got: %s", filesText)
	}
	if strings.Contains(filesText, "none.txt") || strings.Contains(filesText, "haystack") {
		t.Fatalf("files_with_matches should not include nonmatches or content, got: %s", filesText)
	}

	countResult, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"pattern":     "needle",
		"output_mode": "count",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	countText := extractText(countResult.Content)
	if !strings.Contains(countText, "one.txt:2") || !strings.Contains(countText, "two.txt:1") {
		t.Fatalf("expected per-file counts, got: %s", countText)
	}
	if !strings.Contains(countText, "Found 3 total occurrence(s) across 2 file(s).") {
		t.Fatalf("expected total count summary, got: %s", countText)
	}
}

func TestGrepToolFilesWithMatchesSortsByNewestModificationTime(t *testing.T) {
	dir := t.TempDir()
	baseTime := time.Unix(1000, 0)
	files := []struct {
		name   string
		offset time.Duration
	}{
		{name: "a_old.txt", offset: time.Second},
		{name: "b_mid.txt", offset: 2 * time.Second},
		{name: "c_new.txt", offset: 3 * time.Second},
	}
	for _, file := range files {
		path := filepath.Join(dir, file.name)
		if err := os.WriteFile(path, []byte("needle\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		mtime := baseTime.Add(file.offset)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}

	tool := grep.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"pattern": "needle",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	got := grepFileResultLines(text)
	want := []string{"c_new.txt", "b_mid.txt", "a_old.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("expected newest mtime order %v, got %v from: %s", want, got, text)
	}
}

func TestGrepToolCountModeSummaryRespectsHeadLimit(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"one.txt":   "needle\nneedle\n",
		"two.txt":   "needle\n",
		"three.txt": "needle\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tool := grep.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"pattern":     "needle",
		"output_mode": "count",
		"head_limit":  1,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, "across 1 file(s)") {
		t.Fatalf("expected count summary to respect head_limit, got: %s", text)
	}
	if strings.Contains(text, "Found 4 total occurrence(s) across 3 file(s).") {
		t.Fatalf("count summary should not use unpaged totals, got: %s", text)
	}
	if !strings.Contains(text, "results limited to 1 item(s)") {
		t.Fatalf("expected pagination guidance, got: %s", text)
	}
}

func TestGrepToolAcceptsClaudeAliases(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "alias.txt"), []byte("before\nNeedle\nafter\nNeedle\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := grep.NewTool(dir)
	args, err := agent.ValidateToolArguments(types.ToolDefinition{
		Name:       tool.Name(),
		Parameters: tool.Parameters(),
	}, types.ToolCallContent{
		Name: "grep",
		Arguments: map[string]any{
			"pattern":     "needle",
			"output_mode": "content",
			"-i":          true,
			"-C":          1,
		},
	})
	if err != nil {
		t.Fatalf("expected Claude grep aliases to validate: %v", err)
	}

	result, err := tool.Execute(context.Background(), "test-id", args, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(result.Content)
	if !strings.Contains(text, "Needle") {
		t.Fatalf("expected case-insensitive match, got: %s", text)
	}
	if !strings.Contains(text, "before") {
		t.Fatalf("expected context line from -C alias, got: %s", text)
	}
}

func TestGrepToolAcceptsSemanticBooleanStrings(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "case.txt"), []byte("first\nNeedle\nlast\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "multi.txt"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := grep.NewTool(dir)
	args, err := agent.ValidateToolArguments(types.ToolDefinition{
		Name:       tool.Name(),
		Parameters: tool.Parameters(),
	}, types.ToolCallContent{
		Name: "grep",
		Arguments: map[string]any{
			"pattern":     "needle",
			"output_mode": "content",
			"-i":          "true",
			"-n":          "false",
		},
	})
	if err != nil {
		t.Fatalf("expected semantic boolean strings to validate: %v", err)
	}

	result, err := tool.Execute(context.Background(), "test-id", args, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(result.Content)
	if !strings.Contains(text, "case.txt:Needle") {
		t.Fatalf("expected -i string true to match and -n string false to hide line numbers, got: %s", text)
	}
	if strings.Contains(text, "case.txt:2:Needle") {
		t.Fatalf("expected -n string false to hide line numbers, got: %s", text)
	}

	multilineArgs, err := agent.ValidateToolArguments(types.ToolDefinition{
		Name:       tool.Name(),
		Parameters: tool.Parameters(),
	}, types.ToolCallContent{
		Name: "grep",
		Arguments: map[string]any{
			"pattern":     "alpha\\nbeta",
			"output_mode": "content",
			"multiline":   "true",
		},
	})
	if err != nil {
		t.Fatalf("expected multiline semantic boolean string to validate: %v", err)
	}
	multilineResult, err := tool.Execute(context.Background(), "test-id", multilineArgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if multilineText := extractText(multilineResult.Content); !strings.Contains(multilineText, "multi.txt") {
		t.Fatalf("expected multiline string true to match across lines, got: %s", multilineText)
	}
}

func TestGrepToolAcceptsSemanticNumberStrings(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "context.txt"), []byte("before\nneedle\nafter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one.txt", "two.txt", "three.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("alpha\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tool := grep.NewTool(dir)
	contextArgs, err := agent.ValidateToolArguments(types.ToolDefinition{
		Name:       tool.Name(),
		Parameters: tool.Parameters(),
	}, types.ToolCallContent{
		Name: "grep",
		Arguments: map[string]any{
			"pattern":     "needle",
			"output_mode": "content",
			"-B":          "1",
			"-A":          "1",
		},
	})
	if err != nil {
		t.Fatalf("expected context semantic number strings to validate: %v", err)
	}
	contextResult, err := tool.Execute(context.Background(), "test-id", contextArgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	contextText := extractText(contextResult.Content)
	if !strings.Contains(contextText, "before") || !strings.Contains(contextText, "after") {
		t.Fatalf("expected -A/-B string numbers to include context lines, got: %s", contextText)
	}

	pagedArgs, err := agent.ValidateToolArguments(types.ToolDefinition{
		Name:       tool.Name(),
		Parameters: tool.Parameters(),
	}, types.ToolCallContent{
		Name: "grep",
		Arguments: map[string]any{
			"pattern":    "alpha",
			"head_limit": "1",
			"offset":     "1",
		},
	})
	if err != nil {
		t.Fatalf("expected paging semantic number strings to validate: %v", err)
	}
	pagedResult, err := tool.Execute(context.Background(), "test-id", pagedArgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	pagedText := extractText(pagedResult.Content)
	if got := countGrepFileResultLines(pagedText); got != 1 {
		t.Fatalf("expected head_limit/offset string numbers to return one result, got %d: %s", got, pagedText)
	}
	if strings.Contains(pagedText, "one.txt") {
		t.Fatalf("expected offset string number to skip the first sorted result, got: %s", pagedText)
	}
}

func TestGrepToolContentModeCanHideLineNumbers(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lines.txt"), []byte("first\nNeedle\nlast\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := grep.NewTool(dir)
	args, err := agent.ValidateToolArguments(types.ToolDefinition{
		Name:       tool.Name(),
		Parameters: tool.Parameters(),
	}, types.ToolCallContent{
		Name: "grep",
		Arguments: map[string]any{
			"pattern":     "Needle",
			"output_mode": "content",
			"-n":          false,
		},
	})
	if err != nil {
		t.Fatalf("expected -n alias to validate: %v", err)
	}

	result, err := tool.Execute(context.Background(), "test-id", args, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(result.Content)
	if !strings.Contains(text, "lines.txt:Needle") {
		t.Fatalf("expected content output without line number, got: %s", text)
	}
	if strings.Contains(text, "lines.txt:2:Needle") {
		t.Fatalf("expected -n=false to hide line numbers, got: %s", text)
	}
}

func TestGrepToolCapsLongMatchingLinesWithRipgrep(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("rg is not available")
	}
	dir := t.TempDir()
	longLine := "needle" + strings.Repeat("x", common.GrepMaxLineLen+200) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "long.txt"), []byte(longLine), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := grep.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"pattern":     "needle",
		"output_mode": "content",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, "[Omitted long matching line]") {
		t.Fatalf("expected ripgrep max-columns omission, got: %s", text)
	}
	if strings.Contains(text, strings.Repeat("x", common.GrepMaxLineLen)) {
		t.Fatalf("expected long matching line to be omitted, got: %s", text)
	}
}

func TestGrepToolSupportsClaudePagingTypeAndMultiline(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one.go"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "two.go"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "three.txt"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := grep.NewTool(dir)
	pagedResult, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"pattern":    "alpha",
		"type":       "go",
		"head_limit": 1,
		"offset":     1,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pagedText := extractText(pagedResult.Content)
	if !strings.Contains(pagedText, ".go") {
		t.Fatalf("expected a Go file in paged results, got: %s", pagedText)
	}
	if strings.Contains(pagedText, "three.txt") {
		t.Fatalf("type filter should exclude txt file, got: %s", pagedText)
	}
	resultLines := strings.Split(strings.TrimSpace(pagedText), "\n")
	matchingLines := 0
	for _, line := range resultLines {
		if strings.HasSuffix(line, ".go") {
			matchingLines++
		}
	}
	if matchingLines != 1 {
		t.Fatalf("expected head_limit and offset to return one Go file, got: %s", pagedText)
	}

	multilineResult, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"pattern":   "alpha\\nbeta",
		"multiline": true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	multilineText := extractText(multilineResult.Content)
	if !strings.Contains(multilineText, "one.go") || !strings.Contains(multilineText, "two.go") {
		t.Fatalf("expected multiline default file matches, got: %s", multilineText)
	}
}

func TestGrepToolUsesClaudeStyleDefaultHeadLimitAndUnlimitedZero(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 251; i++ {
		name := fmt.Sprintf("file-%03d.txt", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("needle\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tool := grep.NewTool(dir)
	defaultResult, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"pattern": "needle",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defaultText := extractText(defaultResult.Content)
	if !strings.Contains(defaultText, "results limited to 250 item(s)") {
		t.Fatalf("expected default head limit of 250, got: %s", defaultText)
	}
	if got := countGrepFileResultLines(defaultText); got != 250 {
		t.Fatalf("default head limit should return 250 file lines, got %d: %s", got, defaultText)
	}

	unlimitedResult, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"pattern":    "needle",
		"head_limit": 0,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	unlimitedText := extractText(unlimitedResult.Content)
	if strings.Contains(unlimitedText, "results limited") {
		t.Fatalf("head_limit=0 should be unlimited, got: %s", unlimitedText)
	}
	if got := countGrepFileResultLines(unlimitedText); got != 251 {
		t.Fatalf("head_limit=0 should include all 251 file lines, got %d: %s", got, unlimitedText)
	}
}

func countGrepFileResultLines(text string) int {
	count := 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, ".txt") {
			count++
		}
	}
	return count
}

func grepFileResultLines(text string) []string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, ".txt") {
			lines = append(lines, line)
		}
	}
	return lines
}

func TestGrepToolRejectsInvalidOutputMode(t *testing.T) {
	dir := t.TempDir()
	tool := grep.NewTool(dir)
	args := map[string]any{
		"pattern":     "needle",
		"output_mode": "filenames",
	}

	if _, err := agent.ValidateToolArguments(types.ToolDefinition{
		Name:       tool.Name(),
		Parameters: tool.Parameters(),
	}, types.ToolCallContent{
		Name:      "grep",
		Arguments: args,
	}); err == nil {
		t.Fatal("expected schema validation to reject invalid output_mode")
	}

	result, err := tool.Execute(context.Background(), "test-id", args, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); !strings.Contains(text, "invalid output_mode") {
		t.Fatalf("expected runtime invalid output_mode error, got: %s", text)
	}
}

func TestGrepToolRejectsMissingPath(t *testing.T) {
	dir := t.TempDir()
	tool := grep.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"pattern": "needle",
		"path":    "missing",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); !strings.Contains(text, "path does not exist") {
		t.Fatalf("expected missing path error, got: %s", text)
	}
}

func TestGrepToolAllowsFilePath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "needle.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := grep.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"pattern": "needle",
		"path":    "needle.txt",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(result.Content)
	if !strings.Contains(text, "needle.txt") {
		t.Fatalf("expected search in explicit file path, got: %s", text)
	}
	if strings.Contains(text, "other.txt") {
		t.Fatalf("file path search should not include sibling file, got: %s", text)
	}
}

func TestGrepToolRipgrepFilePathOutputIsRelativeToCwd(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("rg is not available")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(dir, "sub", "needle.txt")
	if err := os.WriteFile(filePath, []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := grep.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"pattern": "needle",
		"path":    "sub/needle.txt",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, filepath.Join("sub", "needle.txt")) {
		t.Fatalf("expected relative file path, got: %s", text)
	}
	if strings.Contains(text, filePath) {
		t.Fatalf("did not expect absolute file path in output, got: %s", text)
	}
}

func TestGrepToolPathResultsAreRelativeToCwd(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "needle.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := grep.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"pattern": "needle",
		"path":    "sub",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, filepath.Join("sub", "needle.txt")) {
		t.Fatalf("expected result to be relative to cwd, got: %s", text)
	}
	if strings.TrimSpace(text) == "needle.txt" {
		t.Fatalf("expected directory context to be preserved, got: %s", text)
	}
}

func TestGrepToolSupportsMultipleGlobPatterns(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"one.go":    "needle\n",
		"two.md":    "needle\n",
		"three.txt": "needle\n",
		"four.tsx":  "needle\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tool := grep.NewTool(dir)
	commaResult, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"pattern": "needle",
		"glob":    "*.go,*.md",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	commaText := extractText(commaResult.Content)
	if !strings.Contains(commaText, "one.go") || !strings.Contains(commaText, "two.md") {
		t.Fatalf("expected comma-separated glob matches, got: %s", commaText)
	}
	if strings.Contains(commaText, "three.txt") || strings.Contains(commaText, "four.tsx") {
		t.Fatalf("comma-separated glob should exclude nonmatching extensions, got: %s", commaText)
	}

	braceResult, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"pattern": "needle",
		"glob":    "*.{go,tsx}",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	braceText := extractText(braceResult.Content)
	if !strings.Contains(braceText, "one.go") || !strings.Contains(braceText, "four.tsx") {
		t.Fatalf("expected brace glob matches, got: %s", braceText)
	}
	if strings.Contains(braceText, "two.md") || strings.Contains(braceText, "three.txt") {
		t.Fatalf("brace glob should exclude nonmatching extensions, got: %s", braceText)
	}
}

func TestGrepToolHandlesPatternStartingWithDash(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "dash.txt"), []byte("-needle\nplain\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := grep.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"pattern": "-needle",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(result.Content)
	if !strings.Contains(text, "dash.txt") {
		t.Fatalf("expected dash-leading pattern to match file, got: %s", text)
	}
	if strings.Contains(text, "ripgrep error") {
		t.Fatalf("dash-leading pattern should not be interpreted as a ripgrep flag, got: %s", text)
	}
}

func TestGrepToolSearchesHiddenFilesAndSkipsVCSDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".hidden.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := grep.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"pattern": "needle",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(result.Content)
	if !strings.Contains(text, ".hidden.txt") {
		t.Fatalf("expected hidden file match, got: %s", text)
	}
	if strings.Contains(text, ".git") {
		t.Fatalf("expected VCS directory to be excluded, got: %s", text)
	}
}

func TestGrepToolBuiltinFallbackSearchesOrdinaryIgnoredDirectoryNames(t *testing.T) {
	t.Setenv("PATH", "")

	dir := t.TempDir()
	files := []string{
		filepath.Join("node_modules", "pkg.txt"),
		filepath.Join("vendor", "lib.txt"),
		filepath.Join(".git", "config"),
	}
	for _, name := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("needle\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tool := grep.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"pattern": "needle",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	for _, want := range []string{filepath.Join("node_modules", "pkg.txt"), filepath.Join("vendor", "lib.txt")} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected fallback grep to search %s, got: %s", want, text)
		}
	}
	if strings.Contains(text, ".git") {
		t.Fatalf("expected fallback grep to keep excluding VCS metadata, got: %s", text)
	}
}

func TestFindTool(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(dir, "readme.md"), []byte(""), 0o644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "lib.go"), []byte(""), 0o644)

	tool := find.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"pattern": "*.go",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, "main.go") {
		t.Fatalf("expected main.go in results, got: %s", text)
	}
}

func TestFindToolPathResultsAreRelativeToCwd(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "lib.go"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := find.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"pattern": "*.go",
		"path":    "sub",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, filepath.Join("sub", "lib.go")) {
		t.Fatalf("expected result to be relative to cwd, got: %s", text)
	}
	if strings.TrimSpace(text) == "lib.go" {
		t.Fatalf("expected directory context to be preserved, got: %s", text)
	}
}

func TestFindToolSupportsAbsoluteGlobPattern(t *testing.T) {
	cwd := t.TempDir()
	searchDir := t.TempDir()
	matchPath := filepath.Join(searchDir, "match.go")
	if err := os.WriteFile(matchPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(searchDir, "other.txt"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := find.NewTool(cwd)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"pattern": filepath.Join(searchDir, "*.go"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, matchPath) {
		t.Fatalf("expected absolute pattern to find %s, got: %s", matchPath, text)
	}
	if strings.Contains(text, "other.txt") {
		t.Fatalf("absolute pattern should exclude nonmatching files, got: %s", text)
	}
}

func TestFindToolSearchesHiddenAndGitignoredFilesButSkipsVCS(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		".hidden.txt",
		"ignored.txt",
		filepath.Join("node_modules", "pkg.txt"),
		filepath.Join(".git", "secret.txt"),
	}
	for _, name := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored.txt\nnode_modules/\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := find.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"pattern": "*.txt",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	for _, want := range []string{".hidden.txt", "ignored.txt", filepath.Join("node_modules", "pkg.txt")} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %s in results, got: %s", want, text)
		}
	}
	if strings.Contains(text, ".git") {
		t.Fatalf("expected VCS metadata to be excluded, got: %s", text)
	}
}

func TestFindToolSortsByModificationTime(t *testing.T) {
	dir := t.TempDir()
	baseTime := time.Unix(1000, 0)
	files := []struct {
		name   string
		offset time.Duration
	}{
		{name: "new.go", offset: 3 * time.Second},
		{name: "old.go", offset: 1 * time.Second},
		{name: "mid.go", offset: 2 * time.Second},
	}
	for _, file := range files {
		path := filepath.Join(dir, file.name)
		if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
		mtime := baseTime.Add(file.offset)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}

	tool := find.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"pattern": "*.go",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(extractText(result.Content)), "\n")
	if got, want := strings.Join(lines[:3], ","), "new.go,mid.go,old.go"; got != want {
		t.Fatalf("expected newest-first mtime order %s, got %s", want, got)
	}
}

func TestFindToolUsesClaudeStyleDefaultLimitAndTruncation(t *testing.T) {
	dir := t.TempDir()
	baseTime := time.Unix(1000, 0)
	for i := 0; i < 101; i++ {
		name := fmt.Sprintf("file-%03d.txt", i)
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
		mtime := baseTime.Add(time.Duration(i) * time.Second)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}

	tool := find.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"pattern": "*.txt",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, "Results are truncated") {
		t.Fatalf("expected truncation guidance, got: %s", text)
	}
	if strings.Contains(text, "file-000.txt") {
		t.Fatalf("default limit should keep the newest 100 mtime-sorted files and drop the oldest, got: %s", text)
	}
	lines := strings.Split(strings.Split(text, "\n\n")[0], "\n")
	if len(lines) != 100 {
		t.Fatalf("expected default limit of 100 result lines, got %d: %s", len(lines), text)
	}
}

func TestFindToolDoesNotReportTruncationAtExactLimit(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file-%d.txt", i)), []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tool := find.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"pattern": "*.txt",
		"limit":   2,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); strings.Contains(text, "Results are truncated") {
		t.Fatalf("did not expect truncation at exact limit, got: %s", text)
	}
}

func TestFindToolAcceptsSemanticNumberLimitString(t *testing.T) {
	dir := t.TempDir()
	baseTime := time.Unix(1000, 0)
	for i := 0; i < 3; i++ {
		path := filepath.Join(dir, fmt.Sprintf("file-%d.txt", i))
		if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
		mtime := baseTime.Add(time.Duration(i) * time.Second)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}

	tool := find.NewTool(dir)
	args, err := agent.ValidateToolArguments(types.ToolDefinition{
		Name:       tool.Name(),
		Parameters: tool.Parameters(),
	}, types.ToolCallContent{
		Name: "find",
		Arguments: map[string]any{
			"pattern": "*.txt",
			"limit":   "2",
		},
	})
	if err != nil {
		t.Fatalf("expected string limit to validate: %v", err)
	}

	result, err := tool.Execute(context.Background(), "test-id", args, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(result.Content)
	lines := strings.Split(strings.Split(text, "\n\n")[0], "\n")
	if len(lines) != 2 {
		t.Fatalf("expected string limit to return two result lines, got %d: %s", len(lines), text)
	}
	if !strings.Contains(text, "Results are truncated") {
		t.Fatalf("expected truncation guidance with string limit, got: %s", text)
	}
}

func TestFindToolRejectsNonDirectoryPath(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(filePath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := find.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"pattern": "*.go",
		"path":    "main.go",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); !strings.Contains(text, "path is not a directory") {
		t.Fatalf("expected non-directory error, got: %s", text)
	}
}

func TestFindToolRejectsMissingDirectory(t *testing.T) {
	dir := t.TempDir()
	tool := find.NewTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"pattern": "*.go",
		"path":    "missing",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); !strings.Contains(text, "directory not found") {
		t.Fatalf("expected missing directory error, got: %s", text)
	}
}

type testTodoStore struct {
	todos []planning.TodoItem
}

func (s *testTodoStore) GetTodos() []planning.TodoItem {
	out := make([]planning.TodoItem, len(s.todos))
	copy(out, s.todos)
	return out
}

func (s *testTodoStore) SetTodos(items []planning.TodoItem) {
	s.todos = make([]planning.TodoItem, len(items))
	copy(s.todos, items)
}

func TestTodoWriteTool(t *testing.T) {
	store := &testTodoStore{}
	tool := planning.NewTodoWriteTool(store)

	result, err := tool.Execute(context.Background(), "todo-1", map[string]any{
		"todos": []any{
			map[string]any{"content": "read files", "status": "completed"},
			map[string]any{"content": "implement fix", "status": "in_progress"},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, "updated todo list") {
		t.Fatalf("unexpected result: %s", text)
	}
	if len(store.todos) != 2 {
		t.Fatalf("expected 2 todos, got %d", len(store.todos))
	}
}

func TestTodoWriteToolRejectsMultipleInProgress(t *testing.T) {
	store := &testTodoStore{}
	tool := planning.NewTodoWriteTool(store)

	result, err := tool.Execute(context.Background(), "todo-2", map[string]any{
		"todos": []any{
			map[string]any{"content": "one", "status": "in_progress"},
			map[string]any{"content": "two", "status": "in_progress"},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, "at most one todo may be in_progress") {
		t.Fatalf("unexpected result: %s", text)
	}
}

func TestAllToolsCreation(t *testing.T) {
	allTools := AllTools("/tmp")
	if len(allTools) != 7 {
		t.Fatalf("expected 7 tools, got %d", len(allTools))
	}

	names := make(map[string]bool)
	for _, tool := range allTools {
		names[tool.Name()] = true
	}

	expected := []string{"read", "write", "edit", "bash", "grep", "find", "ls"}
	for _, name := range expected {
		if !names[name] {
			t.Fatalf("missing tool: %s", name)
		}
	}
}

func TestCodingTools(t *testing.T) {
	ct := CodingTools("/tmp")
	names := toolNames(ct)
	// The default coding set includes the read-only navigation tools
	// (grep/find/ls) so Claude-Code-trained models don't hit "Tool not found"
	// when they reach for ls instead of shelling out via bash.
	for _, name := range []string{"read", "bash", "edit", "write", "grep", "find", "ls"} {
		if !containsName(names, name) {
			t.Fatalf("expected %s in coding tools, got %v", name, names)
		}
	}
	if len(ct) != 7 {
		t.Fatalf("expected 7 coding tools, got %d (%v)", len(ct), names)
	}
}

func TestReadOnlyTools(t *testing.T) {
	ro := ReadOnlyTools("/tmp")
	if len(ro) != 4 {
		t.Fatalf("expected 4 read-only tools, got %d", len(ro))
	}
}

func TestResearchToolsAreOptInNetworkTools(t *testing.T) {
	rt := ResearchTools()
	names := toolNames(rt)
	for _, name := range []string{"web_fetch", "web_search"} {
		if !containsName(names, name) {
			t.Fatalf("expected %s in research tools, got %v", name, names)
		}
	}
}

func TestDefaultProviderBuildsAndRebindsTools(t *testing.T) {
	provider := NewProvider(ToolSetReadOnly)
	tools := provider.Tools(types.ToolContext{
		Cwd: "/tmp/a",
		Features: map[string]bool{
			FeatureMemory: true,
		},
		Values: map[string]any{
			ValueMemoryStore: fakeMemoryStore{},
		},
	})
	names := toolNames(tools)
	for _, name := range []string{"read", "grep", "find", "ls", "memo"} {
		if !containsName(names, name) {
			t.Fatalf("expected %s in provider tools, got %v", name, names)
		}
	}
	if containsName(names, "write") || containsName(names, "bash") {
		t.Fatalf("read-only provider should not include write/bash, got %v", names)
	}

	rebound, ok := provider.Rebind(read.NewTool("/tmp/a"), types.ToolContext{Cwd: "/tmp/b"})
	if !ok || rebound.Name() != "read" {
		t.Fatalf("expected read tool to rebind, got %T %v", rebound, ok)
	}
	_, ok = provider.Rebind(testUnknownTool{}, types.ToolContext{Cwd: "/tmp/b"})
	if ok {
		t.Fatal("expected unknown tool not to rebind")
	}
}

func TestProviderWriteRequiresFreshFullReadForExistingFiles(t *testing.T) {
	dir := t.TempDir()
	provider := NewProvider(ToolSetCoding)
	builtTools := provider.Tools(types.ToolContext{Cwd: dir})
	readTool := findToolByName(t, builtTools, "read")
	writeTool := findToolByName(t, builtTools, "write")

	if err := os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := writeTool.Execute(context.Background(), "write-1", map[string]any{
		"path":    "existing.txt",
		"content": "new\n",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); !strings.Contains(text, "File has not been read yet") {
		t.Fatalf("expected unread overwrite rejection, got: %s", text)
	}

	if _, err := readTool.Execute(context.Background(), "read-1", map[string]any{
		"path": "existing.txt",
	}, nil); err != nil {
		t.Fatal(err)
	}
	result, err = writeTool.Execute(context.Background(), "write-2", map[string]any{
		"path":    "existing.txt",
		"content": "new\n",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); !strings.Contains(text, "updated successfully") {
		t.Fatalf("expected write after full read to succeed, got: %s", text)
	}
}

func TestDefaultProviderZeroValueSharesReadStateWithinBuiltTools(t *testing.T) {
	dir := t.TempDir()
	provider := DefaultProvider{Set: ToolSetCoding}
	builtTools := provider.Tools(types.ToolContext{Cwd: dir})
	readTool := findToolByName(t, builtTools, "read")
	writeTool := findToolByName(t, builtTools, "write")

	if err := os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readTool.Execute(context.Background(), "read-1", map[string]any{
		"path": "existing.txt",
	}, nil); err != nil {
		t.Fatal(err)
	}
	result, err := writeTool.Execute(context.Background(), "write-1", map[string]any{
		"path":    "existing.txt",
		"content": "new\n",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); !strings.Contains(text, "updated successfully") {
		t.Fatalf("expected direct DefaultProvider construction to share read state, got: %s", text)
	}
}

func TestProviderWriteRejectsPartialOrStaleReadState(t *testing.T) {
	dir := t.TempDir()
	provider := NewProvider(ToolSetCoding)
	builtTools := provider.Tools(types.ToolContext{Cwd: dir})
	readTool := findToolByName(t, builtTools, "read")
	writeTool := findToolByName(t, builtTools, "write")

	if err := os.WriteFile(filepath.Join(dir, "partial.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readTool.Execute(context.Background(), "read-partial", map[string]any{
		"path":  "partial.txt",
		"limit": 1,
	}, nil); err != nil {
		t.Fatal(err)
	}
	result, err := writeTool.Execute(context.Background(), "write-partial", map[string]any{
		"path":    "partial.txt",
		"content": "changed\n",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); !strings.Contains(text, "File has not been read yet") {
		t.Fatalf("expected partial read rejection, got: %s", text)
	}

	if err := os.WriteFile(filepath.Join(dir, "stale.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readTool.Execute(context.Background(), "read-stale", map[string]any{
		"path": "stale.txt",
	}, nil); err != nil {
		t.Fatal(err)
	}
	stalePath := filepath.Join(dir, "stale.txt")
	if err := os.WriteFile(stalePath, []byte("external\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(stalePath, future, future); err != nil {
		t.Fatal(err)
	}
	result, err = writeTool.Execute(context.Background(), "write-stale", map[string]any{
		"path":    "stale.txt",
		"content": "changed\n",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); !strings.Contains(text, "File has been modified since read") {
		t.Fatalf("expected stale read rejection, got: %s", text)
	}
}

func TestProviderEditRequiresFreshFullReadForExistingFiles(t *testing.T) {
	dir := t.TempDir()
	provider := NewProvider(ToolSetCoding)
	builtTools := provider.Tools(types.ToolContext{Cwd: dir})
	readTool := findToolByName(t, builtTools, "read")
	editTool := findToolByName(t, builtTools, "edit")

	if err := os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := editTool.Execute(context.Background(), "edit-1", map[string]any{
		"path":     "existing.txt",
		"old_text": "old",
		"new_text": "new",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); !strings.Contains(text, "File has not been read yet") {
		t.Fatalf("expected unread edit rejection, got: %s", text)
	}

	if _, err := readTool.Execute(context.Background(), "read-1", map[string]any{
		"path": "existing.txt",
	}, nil); err != nil {
		t.Fatal(err)
	}
	result, err = editTool.Execute(context.Background(), "edit-2", map[string]any{
		"path":     "existing.txt",
		"old_text": "old",
		"new_text": "new",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); !strings.Contains(text, "Successfully edited") {
		t.Fatalf("expected edit after full read to succeed, got: %s", text)
	}
	data, err := os.ReadFile(filepath.Join(dir, "existing.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "new\n" {
		t.Fatalf("expected edited file content, got: %q", got)
	}
}

func TestProviderEditAllowsPartialButRejectsStaleReadState(t *testing.T) {
	dir := t.TempDir()
	provider := NewProvider(ToolSetCoding)
	builtTools := provider.Tools(types.ToolContext{Cwd: dir})
	readTool := findToolByName(t, builtTools, "read")
	editTool := findToolByName(t, builtTools, "edit")

	if err := os.WriteFile(filepath.Join(dir, "partial.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A partial (ranged) read still records the full file content, so a targeted
	// edit is allowed afterwards -- unlike write, which fully rewrites the file.
	if _, err := readTool.Execute(context.Background(), "read-partial", map[string]any{
		"path":  "partial.txt",
		"limit": 1,
	}, nil); err != nil {
		t.Fatal(err)
	}
	result, err := editTool.Execute(context.Background(), "edit-partial", map[string]any{
		"path":     "partial.txt",
		"old_text": "one",
		"new_text": "changed",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); !strings.Contains(text, "Successfully edited") {
		t.Fatalf("expected edit after partial read to succeed, got: %s", text)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "partial.txt")); err != nil {
		t.Fatal(err)
	} else if got := string(data); got != "changed\ntwo\n" {
		t.Fatalf("expected partial-read edit to apply, got: %q", got)
	}

	if err := os.WriteFile(filepath.Join(dir, "stale.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readTool.Execute(context.Background(), "read-stale", map[string]any{
		"path": "stale.txt",
	}, nil); err != nil {
		t.Fatal(err)
	}
	stalePath := filepath.Join(dir, "stale.txt")
	if err := os.WriteFile(stalePath, []byte("external\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(stalePath, future, future); err != nil {
		t.Fatal(err)
	}
	result, err = editTool.Execute(context.Background(), "edit-stale", map[string]any{
		"path":     "stale.txt",
		"old_text": "external",
		"new_text": "changed",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := extractText(result.Content); !strings.Contains(text, "File has been modified since read") {
		t.Fatalf("expected stale read rejection, got: %s", text)
	}
}

func TestCodingToolDescriptionsSteerDedicatedToolUse(t *testing.T) {
	cwd := t.TempDir()
	cases := []struct {
		tool types.Tool
		want []string
	}{
		{
			tool: read.NewTool(cwd),
			want: []string{
				"prefer it over bash commands such as cat",
				"Use offset and limit",
				"Numeric strings such as \"10\" are accepted for offset and limit",
				"Do not include the line-number prefix",
				"Empty files return a system-reminder warning",
				"offset is beyond the end of the file",
				"analyze potentially malicious code without improving it",
				"PNG, JPG, JPEG, GIF, and WEBP images",
				"macOS screenshot names",
				"Jupyter notebooks (.ipynb) are parsed",
				"Large notebook cell outputs are replaced",
			},
		},
		{
			tool: edit.NewTool(cwd),
			want: []string{
				"prefer it over write",
				"Read the file first",
				"must not include read line-number prefixes",
				"no-op edits are rejected",
				"Existing files must be fully read before editing",
				"The path must refer to a file",
				"Jupyter notebooks (.ipynb) are rejected",
				"Files larger than 1.0GB are rejected",
				"Boolean strings \"true\" and \"false\" are accepted",
				"file_path, old_string, and new_string aliases are accepted",
			},
		},
		{
			tool: write.NewTool(cwd),
			want: []string{
				"create new files or to completely rewrite",
				"Prefer edit for targeted changes",
				"Do not create documentation files",
				"Only use emojis if the user explicitly requests",
				"content argument is required",
				"Reports whether the file was created or updated",
				"file_path alias is accepted",
			},
		},
		{
			tool: bash.NewTool(cwd),
			want: []string{
				"Do not use bash for normal file reads",
				"background=true",
				"Numeric strings such as \"1000\" are accepted",
				"Boolean strings \"true\" and \"false\" are accepted",
				"run_in_background alias is accepted",
				"timeout_ms alias is accepted",
				"dangerouslyDisableSandbox parameter is accepted",
				"Foreground sleep commands of 2 seconds or longer are blocked",
				"In-place sed edits are blocked",
				"Simple cat/head/tail/tac/nl/less/more file reads, including common line/byte-count flags, are blocked",
				"Simple grep/rg/sed/awk content searches, including common search and glob flags, are blocked",
				"Simple find/fd file-name searches are blocked",
				"Simple ls directory listings are blocked",
				"never run destructive commands",
			},
		},
		{
			tool: grep.NewTool(cwd),
			want: []string{
				"prefer it over running grep",
				"Use literal=true",
				"Defaults to output_mode=\"files_with_matches\"",
				"files_with_matches results are sorted by modification time",
				"path must exist",
				"Searches hidden files while excluding VCS directories",
				"The built-in fallback skips only VCS metadata directories",
				"capped at 500 columns",
				"Glob accepts space- or comma-separated patterns",
				"relative to the working directory",
				"-i, -n, -A, -B, -C, head_limit, offset, type, and multiline parameters are accepted",
				"Boolean strings \"true\" and \"false\" are accepted for -i, -n, literal, and multiline",
				"numeric strings such as \"10\" are accepted for context, -A, -B, -C, head_limit, and offset",
			},
		},
		{
			tool: find.NewTool(cwd),
			want: []string{
				"prefer it over running shell find",
				"**/*.go",
				"Absolute patterns such as",
				"Searches hidden files and ordinary files ignored by .gitignore",
				"path must be a directory",
				"relative to the working directory",
				"Numeric strings such as \"10\" are accepted for limit",
			},
		},
		{
			tool: ls.NewTool(cwd),
			want: []string{
				"prefer it over running ls through bash",
				"Use find when you need glob pattern matching",
				"Use ignore to hide shallow entries",
				"pass either one pattern or an array of patterns",
				"Numeric strings such as \"10\" are accepted for limit",
				"path must be a directory",
			},
		},
	}

	for _, tc := range cases {
		desc := tc.tool.Description()
		for _, want := range tc.want {
			if !strings.Contains(desc, want) {
				t.Fatalf("%s description missing %q:\n%s", tc.tool.Name(), want, desc)
			}
		}
	}
}

type fakeMemoryStore struct{}

func (fakeMemoryStore) ReadLongTerm() string               { return "" }
func (fakeMemoryStore) WriteLongTerm(content string) error { return nil }
func (fakeMemoryStore) ReadGlobalLongTerm() string         { return "" }
func (fakeMemoryStore) WriteGlobalLongTerm(content string) error {
	return nil
}
func (fakeMemoryStore) AppendToday(content string) error { return nil }

type testUnknownTool struct{}

func (testUnknownTool) Name() string        { return "unknown" }
func (testUnknownTool) Label() string       { return "Unknown" }
func (testUnknownTool) Description() string { return "Unknown" }
func (testUnknownTool) Parameters() any     { return nil }
func (testUnknownTool) Execute(context.Context, string, map[string]any, types.ToolUpdateCallback) (types.ToolResult, error) {
	return types.ToolResult{}, nil
}

func toolNames(tools []types.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name())
	}
	return names
}

func containsName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

func findToolByName(t *testing.T, tools []types.Tool, name string) types.Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Name() == name {
			return tool
		}
	}
	t.Fatalf("tool %s not found in %v", name, toolNames(tools))
	return nil
}

func extractText(content []types.ContentBlock) string {
	var parts []string
	for _, block := range content {
		if tc, ok := block.(*types.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}
