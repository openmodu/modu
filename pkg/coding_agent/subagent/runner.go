package subagent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

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
	streamFn agent.StreamFn,
) (string, error) {
	result, err := RunWithMessages(ctx, def, nil, task, allTools, model, getAPIKey, streamFn)
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

// RunResult contains a subagent's final text and full message transcript.
type RunResult struct {
	Text     string
	Messages []agent.AgentMessage
}

// RunWithMessages executes a subagent using initialMessages as prior
// conversation context, then appends task as the next user message.
func RunWithMessages(
	ctx context.Context,
	def *SubagentDefinition,
	initialMessages []agent.AgentMessage,
	task string,
	allTools []agent.AgentTool,
	model *types.Model,
	getAPIKey func(string) (string, error),
	streamFn agent.StreamFn,
) (RunResult, error) {
	activeTools := filterTools(def.Tools, def.DisallowedTools, allTools)
	activeTools = applyPermissionMode(activeTools, def.PermissionMode)

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

	wrappedStreamFn := streamFn
	if def.MaxTurns > 0 && streamFn != nil {
		wrappedStreamFn = limitTurns(streamFn, def.MaxTurns)
	}

	ag := agent.NewAgent(agent.AgentConfig{
		GetAPIKey: getAPIKey,
		StreamFn:  wrappedStreamFn,
		InitialState: &agent.AgentState{
			SystemPrompt:  systemPrompt,
			Model:         activeModel,
			ThinkingLevel: resolveThinkingLevel(def),
			Tools:         activeTools,
			Messages:      append([]agent.AgentMessage(nil), initialMessages...),
		},
	})

	if err := ag.Prompt(ctx, task); err != nil {
		messages := ag.GetState().Messages
		return RunResult{Messages: append([]agent.AgentMessage(nil), messages...)}, fmt.Errorf("subagent %q: %w", def.Name, err)
	}

	messages := ag.GetState().Messages
	return RunResult{
		Text:     extractLastAssistantText(messages),
		Messages: append([]agent.AgentMessage(nil), messages...),
	}, nil
}

// WithWorkingDirectory returns a copy of def whose prompt explicitly names the
// directory used by cwd-bound tools.
func WithWorkingDirectory(def *SubagentDefinition, cwd string) *SubagentDefinition {
	if def == nil || strings.TrimSpace(cwd) == "" {
		return def
	}

	clone := *def
	base := strings.TrimSpace(clone.SystemPrompt)
	if base == "" {
		base = fmt.Sprintf("You are a specialized assistant named %q. %s", clone.Name, clone.Description)
	}

	env := fmt.Sprintf("# Environment\n- Working directory: %s\n- All file and shell tools are already bound to this working directory.", cwd)
	if strings.Contains(base, "Working directory: "+cwd) {
		clone.SystemPrompt = base
		return &clone
	}
	clone.SystemPrompt = base + "\n\n---\n\n" + env
	return &clone
}

// filterTools returns the subset of allTools whose Name() matches wantNames.
// Unrecognised names are logged and skipped.
func filterTools(wantNames, disallowedNames []string, allTools []agent.AgentTool) []agent.AgentTool {
	toolMap := make(map[string]agent.AgentTool, len(allTools))
	for _, t := range allTools {
		toolMap[t.Name()] = t
	}

	disallowed := make(map[string]struct{}, len(disallowedNames))
	for _, name := range disallowedNames {
		name = strings.TrimSpace(name)
		if name != "" {
			disallowed[name] = struct{}{}
		}
	}

	if len(wantNames) == 0 {
		var result []agent.AgentTool
		for _, t := range allTools {
			if _, blocked := disallowed[t.Name()]; blocked {
				continue
			}
			result = append(result, t)
		}
		return result
	}

	var result []agent.AgentTool
	for _, name := range wantNames {
		name = strings.TrimSpace(name)
		if t, ok := toolMap[name]; ok {
			if _, blocked := disallowed[name]; blocked {
				continue
			}
			result = append(result, t)
		} else {
			log.Printf("subagent: unknown tool %q, skipping", name)
		}
	}
	return result
}

func applyPermissionMode(tools []agent.AgentTool, mode string) []agent.AgentTool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "default", "normal":
		return tools
	case "read-only", "readonly", "read_only":
		var result []agent.AgentTool
		for _, t := range tools {
			if isReadOnlyToolName(t.Name()) {
				result = append(result, t)
			}
		}
		return result
	default:
		return tools
	}
}

func isReadOnlyToolName(name string) bool {
	switch name {
	case "read", "grep", "find", "ls":
		return true
	default:
		return false
	}
}

func limitTurns(next agent.StreamFn, maxTurns int) agent.StreamFn {
	var mu sync.Mutex
	turns := 0
	return func(ctx context.Context, model *types.Model, llmCtx *types.LLMContext, opts *types.SimpleStreamOptions) (types.EventStream, error) {
		mu.Lock()
		defer mu.Unlock()
		turns++
		if turns > maxTurns {
			return nil, fmt.Errorf("subagent exceeded max_turns=%d", maxTurns)
		}
		return next(ctx, model, llmCtx, opts)
	}
}

func resolveThinkingLevel(def *SubagentDefinition) agent.ThinkingLevel {
	if def == nil {
		return agent.ThinkingLevelOff
	}
	if def.ThinkingLevel != "" {
		return def.ThinkingLevel
	}
	switch strings.ToLower(strings.TrimSpace(def.Effort)) {
	case "minimal":
		return agent.ThinkingLevelMinimal
	case "low":
		return agent.ThinkingLevelLow
	case "medium":
		return agent.ThinkingLevelMedium
	case "high":
		return agent.ThinkingLevelHigh
	case "xhigh":
		return agent.ThinkingLevelXHigh
	default:
		return agent.ThinkingLevelOff
	}
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
