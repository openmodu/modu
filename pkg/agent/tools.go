package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/openmodu/modu/pkg/types"
)

const DefaultMaxParallelToolConcurrency = 4
const turnBudgetTruncatedNotice = "\n[tool output truncated by turn budget]"
const turnBudgetOmittedNotice = "[tool output omitted: turn budget exhausted]"

type DefaultTools struct {
	MaxConcurrency         int
	MaxTurnToolResultBytes int
}

func (d DefaultTools) maxConcurrency() int {
	if d.MaxConcurrency <= 0 {
		return DefaultMaxParallelToolConcurrency
	}
	return d.MaxConcurrency
}

func (d DefaultTools) Execute(ctx context.Context, input types.ToolInput) (types.ToolOutput, error) {
	if input.Events == nil {
		input.Events = discardEvents{}
	}
	results := make([]types.ToolResultMessage, len(input.Calls))
	messages := make([]types.AgentMessage, 0, len(input.Calls))
	var steering []types.AgentMessage
	turnBudgetUsed := 0

	for i := 0; i < len(input.Calls); {
		batchEnd := i + 1
		if isParallelCapable(input.Tools, input.Calls[i]) {
			for batchEnd < len(input.Calls) && isParallelCapable(input.Tools, input.Calls[batchEnd]) {
				batchEnd++
			}
		}

		batchResults := runToolBatch(ctx, input, input.Calls[i:batchEnd], d.maxConcurrency())
		for j, result := range batchResults {
			if d.MaxTurnToolResultBytes > 0 {
				result = applyTurnToolResultBudget(result, d.MaxTurnToolResultBytes, &turnBudgetUsed)
			}
			results[i+j] = result
			messages = append(messages, result)
		}

		steering = getMessages(input.GetSteeringMessages)
		if len(steering) > 0 {
			for k := batchEnd; k < len(input.Calls); k++ {
				result := skipToolCall(input.Calls[k], input.Events)
				if d.MaxTurnToolResultBytes > 0 {
					result = applyTurnToolResultBudget(result, d.MaxTurnToolResultBytes, &turnBudgetUsed)
				}
				results[k] = result
				messages = append(messages, result)
			}
			break
		}
		i = batchEnd
	}

	return types.ToolOutput{Messages: messages, Results: results, Steering: steering}, nil
}

func runToolBatch(ctx context.Context, input types.ToolInput, calls []types.ToolCallContent, maxConcurrency int) []types.ToolResultMessage {
	parallel := len(calls) > 1
	batchSize := len(calls)
	out := make([]types.ToolResultMessage, len(calls))
	prepared := make([]preparedCall, len(calls))
	for i, call := range calls {
		prepared[i] = prepareToolCall(input, call)
		input.Events.Emit(types.Event{Type: types.EventTypeToolExecutionStart, ToolCallID: call.ID, ToolName: call.Name, Args: call.Arguments, Parallel: parallel, BatchSize: batchSize})
	}

	runOne := func(i int) {
		out[i] = executePreparedCall(ctx, input, calls[i], prepared[i], parallel, batchSize)
	}
	if parallel {
		var wg sync.WaitGroup
		if maxConcurrency <= 0 {
			maxConcurrency = DefaultMaxParallelToolConcurrency
		}
		sem := make(chan struct{}, maxConcurrency)
		for i := range calls {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
				case <-ctx.Done():
					out[i] = executePreparedCall(ctx, input, calls[i], preparedCall{denyMsg: ctx.Err().Error()}, parallel, batchSize)
					return
				}
				defer func() { <-sem }()
				if err := ctx.Err(); err != nil {
					out[i] = executePreparedCall(ctx, input, calls[i], preparedCall{denyMsg: err.Error()}, parallel, batchSize)
					return
				}
				runOne(i)
			}(i)
		}
		wg.Wait()
	} else {
		runOne(0)
	}

	for _, result := range out {
		input.Events.Emit(types.Event{Type: types.EventTypeMessageStart, Message: result})
		input.Events.Emit(types.Event{Type: types.EventTypeMessageEnd, Message: result})
	}
	return out
}

type preparedCall struct {
	tool    types.Tool
	args    map[string]any
	denyMsg string
}

func prepareToolCall(input types.ToolInput, call types.ToolCallContent) preparedCall {
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
			input.Events.Emit(types.Event{
				Type: types.EventTypeInterrupt,
				Interrupt: &types.InterruptEvent{
					Reason:     types.InterruptReasonToolApproval,
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

func executePreparedCall(ctx context.Context, input types.ToolInput, call types.ToolCallContent, prepared preparedCall, parallel bool, batchSize int) types.ToolResultMessage {
	result := types.ToolResult{}
	isError := false
	if prepared.denyMsg != "" {
		result = errorToolResult(prepared.denyMsg)
		isError = true
	} else {
		r, err := prepared.tool.Execute(ctx, call.ID, prepared.args, func(partial types.ToolResult) {
			input.Events.Emit(types.Event{Type: types.EventTypeToolExecutionUpdate, ToolCallID: call.ID, ToolName: call.Name, Args: call.Arguments, Partial: partial, Parallel: parallel, BatchSize: batchSize})
		})
		if err != nil {
			r = errorToolResult(err.Error())
			isError = true
		} else {
			isError = r.IsError
		}
		result = r
	}

	input.Events.Emit(types.Event{
		Type:       types.EventTypeToolExecutionEnd,
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Args:       call.Arguments,
		Result:     result,
		IsError:    isError,
		Parallel:   parallel,
		BatchSize:  batchSize,
	})
	return types.ToolResultMessage{
		Role:       types.RoleToolResult,
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Content:    result.Content,
		Details:    result.Details,
		IsError:    isError,
		Timestamp:  time.Now().UnixMilli(),
	}
}

func errorToolResult(message string) types.ToolResult {
	return types.ToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: message}},
		Details: map[string]any{},
		IsError: true,
	}
}

func skipToolCall(call types.ToolCallContent, events types.EventSink) types.ToolResultMessage {
	result := errorToolResult("Skipped due to queued user message.")
	events.Emit(types.Event{Type: types.EventTypeToolExecutionStart, ToolCallID: call.ID, ToolName: call.Name, Args: call.Arguments})
	events.Emit(types.Event{Type: types.EventTypeToolExecutionEnd, ToolCallID: call.ID, ToolName: call.Name, Args: call.Arguments, Result: result, IsError: true})
	message := types.ToolResultMessage{
		Role:       types.RoleToolResult,
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Content:    result.Content,
		Details:    result.Details,
		IsError:    true,
		Timestamp:  time.Now().UnixMilli(),
	}
	events.Emit(types.Event{Type: types.EventTypeMessageStart, Message: message})
	events.Emit(types.Event{Type: types.EventTypeMessageEnd, Message: message})
	return message
}

func applyTurnToolResultBudget(result types.ToolResultMessage, maxBytes int, used *int) types.ToolResultMessage {
	if maxBytes <= 0 || used == nil {
		return result
	}
	remaining := maxBytes - *used
	if remaining <= 0 {
		result.Content = []types.ContentBlock{&types.TextContent{Type: "text", Text: turnBudgetOmittedNotice}}
		result.Details = mergeToolResultDetails(result.Details, map[string]any{
			"turnBudgetTruncated": true,
			"turnBudgetBytes":     maxBytes,
		})
		return result
	}
	contentBudget := remaining
	if contentBudget > len([]byte(turnBudgetTruncatedNotice)) {
		contentBudget -= len([]byte(turnBudgetTruncatedNotice))
	}
	content, keptBytes, truncated := limitContentTextBytes(result.Content, contentBudget)
	*used += keptBytes
	if !truncated {
		return result
	}
	content = appendTurnBudgetNotice(content, remaining-keptBytes)
	result.Content = content
	result.Details = mergeToolResultDetails(result.Details, map[string]any{
		"turnBudgetTruncated": true,
		"turnBudgetBytes":     maxBytes,
	})
	return result
}

func appendTurnBudgetNotice(blocks []types.ContentBlock, remaining int) []types.ContentBlock {
	notice := turnBudgetTruncatedNotice
	if remaining > 0 && len([]byte(notice)) > remaining {
		notice = trimUTF8Bytes(notice, remaining)
	}
	if notice == "" {
		return blocks
	}
	for i := len(blocks) - 1; i >= 0; i-- {
		text, ok := blocks[i].(*types.TextContent)
		if !ok || text == nil {
			continue
		}
		copyText := *text
		copyText.Text += notice
		out := append([]types.ContentBlock{}, blocks...)
		out[i] = &copyText
		return out
	}
	return append(blocks, &types.TextContent{Type: "text", Text: strings.TrimPrefix(notice, "\n")})
}

func limitContentTextBytes(blocks []types.ContentBlock, maxBytes int) ([]types.ContentBlock, int, bool) {
	if maxBytes <= 0 {
		return []types.ContentBlock{&types.TextContent{Type: "text", Text: ""}}, 0, len(blocks) > 0
	}
	out := make([]types.ContentBlock, 0, len(blocks))
	used := 0
	truncated := false
	for _, block := range blocks {
		text, ok := block.(*types.TextContent)
		if !ok || text == nil {
			out = append(out, block)
			continue
		}
		remaining := maxBytes - used
		if remaining <= 0 {
			truncated = true
			continue
		}
		if len([]byte(text.Text)) <= remaining {
			out = append(out, text)
			used += len([]byte(text.Text))
			continue
		}
		copyText := *text
		copyText.Text = trimUTF8Bytes(text.Text, remaining)
		used += len([]byte(copyText.Text))
		out = append(out, &copyText)
		truncated = true
	}
	if len(out) == 0 && len(blocks) > 0 {
		out = append(out, &types.TextContent{Type: "text", Text: ""})
	}
	return out, used, truncated
}

func trimUTF8Bytes(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len([]byte(s)) <= maxBytes {
		return s
	}
	cut := 0
	for i := range s {
		if i > maxBytes {
			break
		}
		cut = i
	}
	if cut == 0 {
		for _, r := range s {
			if len(string(r)) <= maxBytes {
				return string(r)
			}
			return ""
		}
	}
	return s[:cut]
}

func mergeToolResultDetails(details any, extra map[string]any) any {
	out := map[string]any{}
	if existing, ok := details.(map[string]any); ok {
		for k, v := range existing {
			out[k] = v
		}
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func findTool(tools []types.Tool, name string) types.Tool {
	for _, tool := range tools {
		if tool.Name() == name {
			return tool
		}
	}
	return nil
}

func isParallelCapable(tools []types.Tool, call types.ToolCallContent) bool {
	tool := findTool(tools, call.Name)
	if tool == nil {
		return false
	}
	parallel, ok := tool.(types.ParallelTool)
	return ok && parallel.Parallel()
}

func toolDefinitions(tools []types.Tool) []types.ToolDefinition {
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
