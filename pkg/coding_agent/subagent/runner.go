package subagent

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

// Run executes a subagent in an independent agent.Agent instance within the
// calling goroutine. It:
//   - Filters allTools to the names listed in def.Tools (unknown names are warned and skipped).
//   - Uses def.Model as model ID if set (inheriting ProviderID from parent model).
//   - Runs task as the user message.
//   - Returns the text of the last AssistantMessage as the result.
func Run(
	ctx context.Context,
	def *SubagentDefinition,
	task string,
	allTools []agent.AgentTool,
	model *types.Model,
	getAPIKey func(string) (string, error),
) (string, error) {
	activeTools := filterTools(def.Tools, allTools)

	// Override model ID if the definition specifies one.
	activeModel := model
	if def.Model != "" && model != nil {
		activeModel = &types.Model{
			ID:         def.Model,
			Name:       def.Model,
			ProviderID: model.ProviderID,
		}
	}

	systemPrompt := def.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = fmt.Sprintf("You are a specialized assistant named %q. %s", def.Name, def.Description)
	}

	ag := agent.NewAgent(agent.AgentConfig{
		GetAPIKey: getAPIKey,
		InitialState: &agent.AgentState{
			SystemPrompt: systemPrompt,
			Model:        activeModel,
			Tools:        activeTools,
		},
	})

	if err := ag.Prompt(ctx, task); err != nil {
		return "", fmt.Errorf("subagent %q: %w", def.Name, err)
	}

	return extractLastAssistantText(ag.GetState().Messages), nil
}

// filterTools returns the subset of allTools whose Name() matches wantNames.
// Unrecognised names are logged and skipped.
func filterTools(wantNames []string, allTools []agent.AgentTool) []agent.AgentTool {
	if len(wantNames) == 0 {
		return nil
	}
	toolMap := make(map[string]agent.AgentTool, len(allTools))
	for _, t := range allTools {
		toolMap[t.Name()] = t
	}
	var result []agent.AgentTool
	for _, name := range wantNames {
		name = strings.TrimSpace(name)
		if t, ok := toolMap[name]; ok {
			result = append(result, t)
		} else {
			log.Printf("subagent: unknown tool %q, skipping", name)
		}
	}
	return result
}

// extractLastAssistantText returns the concatenated text blocks of the last
// AssistantMessage in the message list.
func extractLastAssistantText(messages []agent.AgentMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		var msg types.AssistantMessage
		switch m := messages[i].(type) {
		case types.AssistantMessage:
			msg = m
		case *types.AssistantMessage:
			msg = *m
		default:
			continue
		}
		var parts []string
		for _, block := range msg.Content {
			if tc, ok := block.(*types.TextContent); ok && tc != nil && tc.Text != "" {
				parts = append(parts, tc.Text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}
	return ""
}
