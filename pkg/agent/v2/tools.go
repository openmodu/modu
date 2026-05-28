package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/openmodu/modu/pkg/types"
)

type ToolResult struct {
	Content []types.ContentBlock `json:"content"`
	Details any                  `json:"details"`
	IsError bool                 `json:"isError,omitempty"`
}

type ToolUpdateCallback func(partial ToolResult)

type Tool interface {
	Name() string
	Label() string
	Description() string
	Parameters() any
	Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate ToolUpdateCallback) (ToolResult, error)
}

type ParallelTool interface {
	Parallel() bool
}

type ToolApprovalDecision string

const (
	ToolApprovalAllow       ToolApprovalDecision = "allow"
	ToolApprovalAllowAlways ToolApprovalDecision = "allow_always"
	ToolApprovalDeny        ToolApprovalDecision = "deny"
	ToolApprovalDenyAlways  ToolApprovalDecision = "deny_always"
)

func (d ToolApprovalDecision) IsAllow() bool {
	return d == ToolApprovalAllow || d == ToolApprovalAllowAlways
}

type DefaultTools struct{}

func (DefaultTools) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	results := make([]types.ToolResultMessage, len(input.Calls))
	messages := make([]AgentMessage, 0, len(input.Calls))
	var steering []AgentMessage

	for i := 0; i < len(input.Calls); {
		batchEnd := i + 1
		if isParallelCapable(input.Tools, input.Calls[i]) {
			for batchEnd < len(input.Calls) && isParallelCapable(input.Tools, input.Calls[batchEnd]) {
				batchEnd++
			}
		}

		batchResults := runToolBatch(ctx, input, input.Calls[i:batchEnd])
		for j, result := range batchResults {
			results[i+j] = result
			messages = append(messages, result)
		}

		steering = getMessages(input.GetSteeringMessages)
		if len(steering) > 0 {
			for k := batchEnd; k < len(input.Calls); k++ {
				result := skipToolCall(input.Calls[k], input.Events)
				results[k] = result
				messages = append(messages, result)
			}
			break
		}
		i = batchEnd
	}

	return ToolOutput{Messages: messages, Results: results, Steering: steering}, nil
}

func runToolBatch(ctx context.Context, input ToolInput, calls []types.ToolCallContent) []types.ToolResultMessage {
	parallel := len(calls) > 1
	out := make([]types.ToolResultMessage, len(calls))
	prepared := make([]preparedCall, len(calls))
	for i, call := range calls {
		prepared[i] = prepareToolCall(input, call)
		input.Events.Push(Event{Type: EventTypeToolExecutionStart, ToolCallID: call.ID, ToolName: call.Name, Args: call.Arguments, Parallel: parallel})
	}

	runOne := func(i int) {
		out[i] = executePreparedCall(ctx, input, calls[i], prepared[i], parallel)
	}
	if parallel {
		var wg sync.WaitGroup
		for i := range calls {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				runOne(i)
			}(i)
		}
		wg.Wait()
	} else {
		runOne(0)
	}

	for _, result := range out {
		emitMessage(input.Events, result)
	}
	return out
}

type preparedCall struct {
	tool    Tool
	args    map[string]any
	denyMsg string
}

func prepareToolCall(input ToolInput, call types.ToolCallContent) preparedCall {
	tool := findTool(input.Tools, call.Name)
	if tool == nil {
		return preparedCall{denyMsg: "Tool not found"}
	}
	toolDef := types.ToolDefinition{Name: tool.Name(), Description: tool.Description(), Parameters: tool.Parameters()}
	args, err := ValidateToolArguments(toolDef, call)
	if err != nil {
		return preparedCall{denyMsg: err.Error()}
	}
	if input.ApproveTool != nil {
		if input.EnableInterrupts {
			input.Events.Push(Event{
				Type: EventTypeInterrupt,
				Interrupt: &InterruptEvent{
					Reason:     InterruptReasonToolApproval,
					ToolCallID: call.ID,
					ToolName:   call.Name,
					ToolArgs:   args,
				},
			})
		}
		decision, err := input.ApproveTool(call.Name, call.ID, args)
		if err != nil {
			return preparedCall{denyMsg: fmt.Sprintf("Tool approval error: %v", err)}
		}
		if !decision.IsAllow() {
			return preparedCall{denyMsg: "Tool execution denied by user."}
		}
	}
	return preparedCall{tool: tool, args: args}
}

func executePreparedCall(ctx context.Context, input ToolInput, call types.ToolCallContent, prepared preparedCall, parallel bool) types.ToolResultMessage {
	result := ToolResult{}
	isError := false
	if prepared.denyMsg != "" {
		result = errorToolResult(prepared.denyMsg)
		isError = true
	} else {
		r, err := prepared.tool.Execute(ctx, call.ID, prepared.args, func(partial ToolResult) {
			input.Events.Push(Event{Type: EventTypeToolExecutionUpdate, ToolCallID: call.ID, ToolName: call.Name, Args: call.Arguments, Partial: partial, Parallel: parallel})
		})
		if err != nil {
			r = errorToolResult(err.Error())
			isError = true
		} else {
			isError = r.IsError
		}
		result = r
	}

	input.Events.Push(Event{
		Type:       EventTypeToolExecutionEnd,
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Args:       call.Arguments,
		Result:     result,
		IsError:    isError,
		Parallel:   parallel,
	})
	return types.ToolResultMessage{
		Role:       RoleToolResult,
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Content:    result.Content,
		Details:    result.Details,
		IsError:    isError,
		Timestamp:  time.Now().UnixMilli(),
	}
}

func errorToolResult(message string) ToolResult {
	return ToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: message}},
		Details: map[string]any{},
		IsError: true,
	}
}

func skipToolCall(call types.ToolCallContent, events *EventStream) types.ToolResultMessage {
	result := errorToolResult("Skipped due to queued user message.")
	events.Push(Event{Type: EventTypeToolExecutionStart, ToolCallID: call.ID, ToolName: call.Name, Args: call.Arguments})
	events.Push(Event{Type: EventTypeToolExecutionEnd, ToolCallID: call.ID, ToolName: call.Name, Args: call.Arguments, Result: result, IsError: true})
	message := types.ToolResultMessage{
		Role:       RoleToolResult,
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Content:    result.Content,
		Details:    result.Details,
		IsError:    true,
		Timestamp:  time.Now().UnixMilli(),
	}
	emitMessage(events, message)
	return message
}

func findTool(tools []Tool, name string) Tool {
	for _, tool := range tools {
		if tool.Name() == name {
			return tool
		}
	}
	return nil
}

func isParallelCapable(tools []Tool, call types.ToolCallContent) bool {
	tool := findTool(tools, call.Name)
	if tool == nil {
		return false
	}
	parallel, ok := tool.(ParallelTool)
	return ok && parallel.Parallel()
}

func toolDefinitions(tools []Tool) []types.ToolDefinition {
	defs := make([]types.ToolDefinition, 0, len(tools))
	for _, tool := range tools {
		defs = append(defs, types.ToolDefinition{
			Name:        tool.Name(),
			Description: tool.Description(),
			Parameters:  tool.Parameters(),
		})
	}
	return defs
}
