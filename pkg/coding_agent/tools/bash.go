package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/crosszan/modu/pkg/agent"
	"github.com/crosszan/modu/pkg/providers"
)

const (
	defaultBashTimeout = 120 // seconds
	maxBashTimeout     = 600 // seconds
)

// BashTool implements the bash command execution tool.
type BashTool struct {
	cwd string
}

func NewBashTool(cwd string) *BashTool {
	return &BashTool{cwd: cwd}
}

func (t *BashTool) Name() string  { return "bash" }
func (t *BashTool) Label() string { return "Bash Command" }
func (t *BashTool) Description() string {
	return `Execute a bash command and return its output. The command runs in the working directory. Use timeout parameter to set execution timeout in seconds (default 120, max 600).`
}

func (t *BashTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The bash command to execute",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Timeout in seconds (default 120, max 600)",
			},
		},
		"required": []string{"command"},
	}
}

func (t *BashTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	command, _ := args["command"].(string)
	if command == "" {
		return errorResult("command is required"), nil
	}

	timeout := defaultBashTimeout
	if v, ok := args["timeout"]; ok {
		timeout = toInt(v)
		if timeout <= 0 {
			timeout = defaultBashTimeout
		}
		if timeout > maxBashTimeout {
			timeout = maxBashTimeout
		}
	}

	// Create context with timeout
	cmdCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "bash", "-c", command)
	cmd.Dir = t.cwd

	// Inherit environment
	cmd.Env = os.Environ()

	// Set process group so we can kill all child processes
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	var output strings.Builder
	if stdout.Len() > 0 {
		output.WriteString(stdout.String())
	}
	if stderr.Len() > 0 {
		if output.Len() > 0 {
			output.WriteString("\n")
		}
		output.WriteString(stderr.String())
	}

	result := output.String()

	// Truncate output
	truncated := TruncateTail(result, TruncateOptions{
		MaxLines: BashMaxLines,
		MaxBytes: DefaultMaxBytes,
	})

	exitCode := 0
	timedOut := false

	if err != nil {
		if cmdCtx.Err() == context.DeadlineExceeded {
			timedOut = true
			// Kill process group
			if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		}

		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if !timedOut {
			return errorResult(fmt.Sprintf("failed to execute command: %v", err)), nil
		}
	}

	var text string
	if timedOut {
		text = fmt.Sprintf("Command timed out after %d seconds.\n", timeout)
		if truncated.Content != "" {
			text += "Partial output:\n" + truncated.Content
		}
	} else {
		text = truncated.Content
		if truncated.WasTruncated {
			text = truncated.Message + text
		}
		if exitCode != 0 {
			text += fmt.Sprintf("\n\nExit code: %d", exitCode)
		}
	}

	if text == "" {
		text = "(no output)"
	}

	return agent.AgentToolResult{
		Content: []providers.ContentBlock{
			providers.TextContent{
				Type: "text",
				Text: text,
			},
		},
		Details: map[string]any{
			"exitCode": exitCode,
			"timedOut": timedOut,
		},
	}, nil
}
