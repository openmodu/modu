package moms

import (
	"context"
	"fmt"
	"os"

	"github.com/crosszan/modu/pkg/agent"
	"github.com/crosszan/modu/pkg/llm"
)

// BashSandboxTool is the bash tool that executes commands in the sandbox.
type BashSandboxTool struct {
	sandbox *Sandbox
}

func NewBashSandboxTool(sb *Sandbox) *BashSandboxTool {
	return &BashSandboxTool{sandbox: sb}
}

func (t *BashSandboxTool) Name() string  { return "bash" }
func (t *BashSandboxTool) Label() string { return "Bash Command" }
func (t *BashSandboxTool) Description() string {
	return "Execute a bash command and return its output. Use timeout parameter to set execution timeout in seconds (default 120, max 600)."
}
func (t *BashSandboxTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The bash command to execute",
			},
			"label": map[string]any{
				"type":        "string",
				"description": "Short human-readable label for this command",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Timeout in seconds (default 120, max 600)",
			},
		},
		"required": []string{"command"},
	}
}

func (t *BashSandboxTool) Execute(ctx context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	command, _ := args["command"].(string)
	if command == "" {
		return errorToolResult("command is required"), nil
	}
	timeout := 120
	if v, ok := args["timeout"]; ok {
		timeout = toInt(v)
	}
	res := t.sandbox.Exec(ctx, command, timeout)
	output := buildOutput(res.Stdout, res.Stderr)
	if res.TimedOut {
		output = fmt.Sprintf("Command timed out after %d seconds.\n%s", timeout, output)
	} else if res.ExitCode != 0 {
		output += fmt.Sprintf("\n\nExit code: %d", res.ExitCode)
	}
	if output == "" {
		output = "(no output)"
	}
	return agent.AgentToolResult{
		Content: []llm.ContentBlock{llm.TextContent{Type: "text", Text: truncateStr(output, 200000)}},
		Details: map[string]any{"exitCode": res.ExitCode, "timedOut": res.TimedOut},
	}, nil
}

// -----------------------------------------------------------------------
// AttachTool sends a file back to Telegram.

type AttachTool struct {
	uploadFn func(filePath, title string) error
}

func NewAttachTool(uploadFn func(filePath, title string) error) *AttachTool {
	return &AttachTool{uploadFn: uploadFn}
}

func (t *AttachTool) Name() string  { return "attach" }
func (t *AttachTool) Label() string { return "Send File" }
func (t *AttachTool) Description() string {
	return "Send a file to the Telegram chat. Provide the absolute path and an optional title."
}
func (t *AttachTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Absolute path to the file to send",
			},
			"title": map[string]any{
				"type":        "string",
				"description": "Optional display title for the file",
			},
			"label": map[string]any{
				"type":        "string",
				"description": "Short human-readable label",
			},
		},
		"required": []string{"path"},
	}
}

func (t *AttachTool) Execute(ctx context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return errorToolResult("path is required"), nil
	}
	title, _ := args["title"].(string)
	if err := t.uploadFn(path, title); err != nil {
		return errorToolResult(fmt.Sprintf("failed to send file: %v", err)), nil
	}
	return agent.AgentToolResult{
		Content: []llm.ContentBlock{llm.TextContent{Type: "text", Text: fmt.Sprintf("File sent: %s", path)}},
	}, nil
}

// -----------------------------------------------------------------------
// Thin wrappers re-using coding_agent's read/write/edit tools but with
// the sandbox cwd mapped correctly.

// ReadTool wraps coding_agent's read tool.
type ReadTool struct {
	cwd string
}

func NewReadTool(cwd string) *ReadTool { return &ReadTool{cwd: cwd} }
func (t *ReadTool) Name() string       { return "read" }
func (t *ReadTool) Label() string      { return "Read File" }
func (t *ReadTool) Description() string {
	return "Read the contents of a file. Provide the absolute path."
}
func (t *ReadTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "Absolute path to file"},
			"label": map[string]any{"type": "string", "description": "Short label"},
		},
		"required": []string{"path"},
	}
}
func (t *ReadTool) Execute(_ context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return errorToolResult("path is required"), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return errorToolResult(fmt.Sprintf("read error: %v", err)), nil
	}
	text := truncateStr(string(data), 200000)
	return agent.AgentToolResult{
		Content: []llm.ContentBlock{llm.TextContent{Type: "text", Text: text}},
	}, nil
}

// WriteTool creates or overwrites a file.
type WriteTool struct{}

func NewWriteTool() *WriteTool { return &WriteTool{} }
func (t *WriteTool) Name() string  { return "write" }
func (t *WriteTool) Label() string { return "Write File" }
func (t *WriteTool) Description() string {
	return "Create or overwrite a file with the given content."
}
func (t *WriteTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":    map[string]any{"type": "string", "description": "Absolute file path"},
			"content": map[string]any{"type": "string", "description": "File content"},
			"label":   map[string]any{"type": "string", "description": "Short label"},
		},
		"required": []string{"path", "content"},
	}
}
func (t *WriteTool) Execute(_ context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	path, _ := args["path"].(string)
	content, _ := args["content"].(string)
	if path == "" {
		return errorToolResult("path is required"), nil
	}
	if err := os.MkdirAll(fmt.Sprintf("%s", getDirOf(path)), 0o755); err != nil {
		return errorToolResult(fmt.Sprintf("mkdir error: %v", err)), nil
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return errorToolResult(fmt.Sprintf("write error: %v", err)), nil
	}
	return agent.AgentToolResult{
		Content: []llm.ContentBlock{llm.TextContent{Type: "text", Text: fmt.Sprintf("Wrote %d bytes to %s", len(content), path)}},
	}, nil
}

// -----------------------------------------------------------------------
// helpers

func errorToolResult(msg string) agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: []llm.ContentBlock{llm.TextContent{Type: "text", Text: msg}},
		Details: map[string]any{"isError": true},
	}
}

func buildOutput(stdout, stderr string) string {
	if stdout != "" && stderr != "" {
		return stdout + "\n" + stderr
	}
	return stdout + stderr
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n...(truncated)"
}

func toInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case float32:
		return int(x)
	}
	return 0
}

func getDirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return "."
}
