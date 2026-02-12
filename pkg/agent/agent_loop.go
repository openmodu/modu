package agent

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// RunLoop implements the ReAct loop logic from agent-loop.ts
func (a *Agent) RunLoop(ctx context.Context, initialMessages []Message) error {
	a.SetStreaming(true)
	defer a.SetStreaming(false)

	// Add initial messages to state
	for _, msg := range initialMessages {
		a.AppendMessage(msg)
	}

	a.Emit(AgentEvent{Type: EventTypeAgentStart})

	// Add new messages events
	for _, msg := range initialMessages {
		a.Emit(AgentEvent{Type: EventTypeMessageStart, Message: &msg})
		a.Emit(AgentEvent{Type: EventTypeMessageEnd, Message: &msg})
	}

	firstTurn := true

	// Outer loop handles follow-ups
	for {
		pendingSteering := a.GetSteeringMessages()

		// Inner loop handles tool calls and steering
		// Corresponds to `while (hasMoreToolCalls || pendingMessages.length > 0)` in TS
		for hasMoreToolCalls := true; hasMoreToolCalls || len(pendingSteering) > 0; {

			if !firstTurn {
				a.Emit(AgentEvent{Type: EventTypeTurnStart})
			} else {
				firstTurn = false
			}

			// Process pending steering messages
			if len(pendingSteering) > 0 {
				for _, msg := range pendingSteering {
					a.AppendMessage(msg)
					a.Emit(AgentEvent{Type: EventTypeMessageStart, Message: &msg})
					a.Emit(AgentEvent{Type: EventTypeMessageEnd, Message: &msg})
				}
				pendingSteering = []Message{}
			}

			// Stream Assistant Response
			state := a.GetState()
			stream, err := state.Model.Stream(ctx, state.Messages, state.Tools)
			if err != nil {
				a.Emit(AgentEvent{Type: EventTypeAgentEnd, IsError: true})
				a.SetError(err)
				return err
			}

			var currentAssistantMsg Message
			currentAssistantMsg.Role = RoleAssistant
			currentAssistantMsg.Timestamp = time.Now().UnixMilli()

			// Accumulators
			var textBuilder strings.Builder
			var toolCalls []ToolCall

			// Emit MessageStart once
			a.Emit(AgentEvent{Type: EventTypeMessageStart, Message: &currentAssistantMsg})

			for event := range stream {
				switch event.Type {
				case "text_delta":
					chunk := event.Payload.(string)
					textBuilder.WriteString(chunk)
					// Update current message
					currentAssistantMsg.Content = []ContentBlock{{Type: ContentTypeText, Text: textBuilder.String()}}
					// Emit update
					a.Emit(AgentEvent{Type: EventTypeMessageUpdate, Message: &currentAssistantMsg, Partial: chunk})

				case "tool_call":
					tc := event.Payload.(ToolCall)
					toolCalls = append(toolCalls, tc)
					// In a real stream, we might receive partial tool calls, here we assume full for simplicity

				case "error":
					err := event.Payload.(error)
					a.SetError(err)
					return err
				}
			}

			// Finalize assistant message content
			currentAssistantMsg.Content = []ContentBlock{}
			if textBuilder.Len() > 0 {
				currentAssistantMsg.Content = append(currentAssistantMsg.Content, ContentBlock{Type: ContentTypeText, Text: textBuilder.String()})
			}
			for _, tc := range toolCalls {
				t := tc // Copy
				currentAssistantMsg.Content = append(currentAssistantMsg.Content, ContentBlock{Type: ContentTypeToolCall, ToolCall: &t})
			}

			a.AppendMessage(currentAssistantMsg)
			a.Emit(AgentEvent{Type: EventTypeMessageEnd, Message: &currentAssistantMsg})

			// Execute Tools
			hasMoreToolCalls = len(toolCalls) > 0
			var toolResults []Message

			if hasMoreToolCalls {
				// Check for Steering BEFORE execution? In TS it's checked inside executeToolCalls loop
				// We'll mimic the chunked execution

				newSteering := a.GetSteeringMessages()
				if len(newSteering) > 0 {
					// Logic to skip tools if steering present
					pendingSteering = newSteering
					// Skip execution of remaining tools
					// For now, let's just break and handle steering in next iteration
					// But we should mark tools as skipped ideally
				} else {
					for _, tc := range toolCalls {
						a.Emit(AgentEvent{Type: EventTypeToolExecutionStart, ToolCallID: tc.ID, ToolName: tc.Name, Result: tc.Args})

						var tool Tool
						for _, t := range state.Tools {
							if t.Name() == tc.Name {
								tool = t
								break
							}
						}

						var resultStr string
						var execErr error

						if tool != nil {
							resultStr, execErr = tool.Execute(ctx, tc.ID, tc.Args, func(partial interface{}) {
								a.Emit(AgentEvent{Type: EventTypeToolExecutionUpdate, ToolCallID: tc.ID, ToolName: tc.Name, Partial: partial})
							})
						} else {
							execErr = fmt.Errorf("tool not found")
						}

						a.Emit(AgentEvent{Type: EventTypeToolExecutionEnd, ToolCallID: tc.ID, ToolName: tc.Name, Result: resultStr, IsError: execErr != nil})

						// Create Tool Result Message
						content := resultStr
						if execErr != nil {
							content = fmt.Sprintf("Error: %v", execErr)
						}

						trMsg := Message{
							Role:      RoleTool,
							Content:   []ContentBlock{{Type: ContentTypeText, Text: content}},
							Timestamp: time.Now().UnixMilli(),
							// In real impl, link to ToolCallID
						}
						toolResults = append(toolResults, trMsg)
						a.AppendMessage(trMsg)

						a.Emit(AgentEvent{Type: EventTypeMessageStart, Message: &trMsg})
						a.Emit(AgentEvent{Type: EventTypeMessageEnd, Message: &trMsg})

						// Check steering mid-execution
						midSteering := a.GetSteeringMessages()
						if len(midSteering) > 0 {
							pendingSteering = midSteering
							break // Stop executing tools
						}
					}
				}
			}

			a.Emit(AgentEvent{Type: EventTypeTurnEnd, Message: &currentAssistantMsg, ToolResults: toolResults})
		}

		// Check Follow Ups
		followUps := a.GetFollowUpMessages()
		if len(followUps) > 0 {
			pendingSteering = followUps // Treat as pending messages for next loop
			continue
		}

		break
	}

	a.Emit(AgentEvent{Type: EventTypeAgentEnd, Messages: a.GetState().Messages})
	return nil
}
