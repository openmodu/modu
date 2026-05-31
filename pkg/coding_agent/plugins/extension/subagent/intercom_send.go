package subagent

import (
	"context"
	"fmt"

	"github.com/openmodu/modu/pkg/types"
)

// intercomSendTool is the agent-facing writer side of the intercom inbox.
// Both parent and child agents can call it; the message lands in the
// addressed task's JSONL file and the recipient reads via
// `subagent action=intercom id=<taskID>`.
type intercomSendTool struct {
	ext *Extension
}

func newIntercomSendTool(ext *Extension) *intercomSendTool {
	return &intercomSendTool{ext: ext}
}

func (t *intercomSendTool) Name() string   { return "subagent_intercom_send" }
func (t *intercomSendTool) Label() string  { return "Subagent Intercom Send" }
func (t *intercomSendTool) Parallel() bool { return true }

func (t *intercomSendTool) Description() string {
	return `Send a structured message to a subagent task's intercom inbox.

Use this when you (parent or child) want to drop a note that the other
side can read via ` + "`subagent action=intercom id=<taskID>`" + `. The
message is appended to a per-task JSONL file under
tool-results/<project>/subagents/intercom/<taskID>.jsonl.

The recipient is identified by taskId — typically the synthetic
subagent-batch-N id the orchestrator returns for an async dispatch, or a
host-managed task-N id.`
}

func (t *intercomSendTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"taskId":  map[string]any{"type": "string", "description": "Target task id."},
			"message": map[string]any{"type": "string", "description": "Message body."},
			"from":    map[string]any{"type": "string", "description": "Optional sender label (e.g. agent name)."},
		},
		"required": []string{"taskId", "message"},
	}
}

func (t *intercomSendTool) Execute(_ context.Context, _ string, args map[string]any, _ types.ToolUpdateCallback) (types.ToolResult, error) {
	taskID, _ := args["taskId"].(string)
	message, _ := args["message"].(string)
	from, _ := args["from"].(string)
	if taskID == "" {
		return errResult(`subagent_intercom_send: "taskId" is required`), nil
	}
	if message == "" {
		return errResult(`subagent_intercom_send: "message" is required`), nil
	}
	if err := appendIntercomMessage(t.ext, taskID, from, message); err != nil {
		return errResult(fmt.Sprintf("subagent_intercom_send: %v", err)), nil
	}
	return types.ToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: fmt.Sprintf("Message sent to intercom for task %s.", taskID)}},
	}, nil
}
