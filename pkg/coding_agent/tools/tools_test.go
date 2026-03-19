package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openmodu/modu/pkg/types"
)

func TestTruncateHead(t *testing.T) {
	content := strings.Repeat("line\n", 3000)
	result := TruncateHead(content, TruncateOptions{MaxLines: 100})
	if !result.WasTruncated {
		t.Fatal("expected truncation")
	}
	if result.KeptLines != 100 {
		t.Fatalf("expected 100 kept lines, got %d", result.KeptLines)
	}
}

func TestTruncateTail(t *testing.T) {
	content := strings.Repeat("line\n", 3000)
	result := TruncateTail(content, TruncateOptions{MaxLines: 100})
	if !result.WasTruncated {
		t.Fatal("expected truncation")
	}
	if result.KeptLines != 100 {
		t.Fatalf("expected 100 kept lines, got %d", result.KeptLines)
	}
}

func TestTruncateLine(t *testing.T) {
	short := "hello"
	if TruncateLine(short, 10) != short {
		t.Fatal("short line should not be truncated")
	}

	long := strings.Repeat("x", 600)
	result := TruncateLine(long, 500)
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
		got := FormatSize(tt.bytes)
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
		got := ExpandPath(tt.input)
		if got != tt.expected {
			t.Errorf("ExpandPath(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestResolveToCwd(t *testing.T) {
	got := ResolveToCwd("foo/bar.go", "/tmp/project")
	if got != "/tmp/project/foo/bar.go" {
		t.Errorf("ResolveToCwd = %q, want /tmp/project/foo/bar.go", got)
	}

	got = ResolveToCwd("/absolute/path.go", "/tmp/project")
	if got != "/absolute/path.go" {
		t.Errorf("ResolveToCwd = %q, want /absolute/path.go", got)
	}
}

func TestReadTool(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	os.WriteFile(filePath, []byte("line1\nline2\nline3\n"), 0o644)

	tool := NewReadTool(dir)

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
}

func TestReadToolWithOffset(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	var content strings.Builder
	for i := 1; i <= 10; i++ {
		content.WriteString("line" + strings.Repeat("x", i) + "\n")
	}
	os.WriteFile(filePath, []byte(content.String()), 0o644)

	tool := NewReadTool(dir)
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

func TestWriteTool(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteTool(dir)

	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"path":    "newfile.txt",
		"content": "hello world",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, "Successfully") {
		t.Fatal("expected success message")
	}

	data, _ := os.ReadFile(filepath.Join(dir, "newfile.txt"))
	if string(data) != "hello world" {
		t.Fatalf("file content mismatch: %s", string(data))
	}
}

func TestWriteToolSubdirectory(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteTool(dir)

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

func TestEditTool(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "edit_test.go")
	os.WriteFile(filePath, []byte("func hello() {\n\tfmt.Println(\"hello\")\n}\n"), 0o644)

	tool := NewEditTool(dir)

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

	tool := NewEditTool(dir)
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

func TestBashTool(t *testing.T) {
	tool := NewBashTool(t.TempDir())

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
	tool := NewBashTool(t.TempDir())

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

func TestLsTool(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(dir, "b.go"), []byte(""), 0o644)
	os.MkdirAll(filepath.Join(dir, "subdir"), 0o755)

	tool := NewLsTool(dir)
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

func TestGrepTool(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.go"), []byte("func main() {\n\tfmt.Println(\"hello\")\n}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "other.txt"), []byte("nothing here\n"), 0o644)

	tool := NewGrepTool(dir)
	result, err := tool.Execute(context.Background(), "test-id", map[string]any{
		"pattern": "Println",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := extractText(result.Content)
	if !strings.Contains(text, "Println") {
		t.Fatalf("expected Println in results, got: %s", text)
	}
}

func TestFindTool(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(dir, "readme.md"), []byte(""), 0o644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "lib.go"), []byte(""), 0o644)

	tool := NewFindTool(dir)
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
	if len(ct) != 4 {
		t.Fatalf("expected 4 coding tools, got %d", len(ct))
	}
}

func TestReadOnlyTools(t *testing.T) {
	ro := ReadOnlyTools("/tmp")
	if len(ro) != 4 {
		t.Fatalf("expected 4 read-only tools, got %d", len(ro))
	}
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
