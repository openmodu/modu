package coding_agent

import (
	"fmt"
	"sort"
	"strings"

	"github.com/openmodu/modu/pkg/types"
)

// SlashCommand represents a built-in slash command.
type SlashCommand struct {
	Name        string
	Description string
	Handler     func(session *CodingSession, args string) error
}

// HasSlashCommand reports whether the session has a registered slash command.
func (s *engine) HasSlashCommand(name string) bool {
	if s == nil {
		return false
	}
	name = strings.TrimPrefix(strings.TrimSpace(name), "/")
	if i := strings.IndexAny(name, " \t"); i >= 0 {
		name = name[:i]
	}
	if name == "" {
		return false
	}
	_, ok := s.slashCommands[name]
	return ok
}

// RegisteredSlashCommands returns all session-level slash commands, including
// commands contributed by extensions.
func (s *engine) RegisteredSlashCommands() []SlashCommand {
	if s == nil || len(s.slashCommands) == 0 {
		return nil
	}
	out := make([]SlashCommand, 0, len(s.slashCommands))
	for _, cmd := range s.slashCommands {
		out = append(out, cmd)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// BuiltinCommands returns all built-in slash commands.
func BuiltinCommands() []SlashCommand {
	return []SlashCommand{
		{
			Name:        "model",
			Description: "Switch the active model (e.g., /model ollama qwen3-coder-next)",
			Handler:     cmdModel,
		},
		{
			Name:        "compact",
			Description: "Manually trigger context compaction",
			Handler:     cmdCompact,
		},
		{
			Name:        "tree",
			Description: "Show conversation tree structure",
			Handler:     cmdTree,
		},
		{
			Name:        "fork",
			Description: "Fork the conversation from a specific entry (e.g., /fork <entryId>)",
			Handler:     cmdFork,
		},
		{
			Name:        "tools",
			Description: "List active tools",
			Handler:     cmdTools,
		},
		{
			Name:        "help",
			Description: "Show available commands",
			Handler:     cmdHelp,
		},
		{
			Name:        "thinking",
			Description: "Set thinking level (off, low, medium, high) or cycle if no argument",
			Handler:     cmdThinking,
		},
		{
			Name:        "retry",
			Description: "Manually trigger a retry of the last failed prompt",
			Handler:     cmdRetry,
		},
		{
			Name:        "session",
			Description: "Show current session information",
			Handler:     cmdSession,
		},
	}
}

func cmdModel(session *CodingSession, args string) error {
	parts := strings.Fields(args)
	if len(parts) < 2 {
		return fmt.Errorf("usage: /model <provider> <modelId>")
	}
	return session.SetModelByID(parts[0], parts[1])
}

func cmdCompact(session *CodingSession, _ string) error {
	return session.Compact(nil)
}

func cmdTree(session *CodingSession, _ string) error {
	if session.sessionTree == nil {
		return fmt.Errorf("session tree not available")
	}
	branches := session.sessionTree.GetBranches()
	if len(branches) == 0 {
		fmt.Println("No branches in session tree.")
		return nil
	}
	for _, b := range branches {
		fmt.Printf("Branch %s (parent: %s, entries: %d)\n", b.ID, b.ParentID, len(b.Entries))
	}
	return nil
}

func cmdFork(session *CodingSession, args string) error {
	entryID := strings.TrimSpace(args)
	if entryID == "" {
		return fmt.Errorf("usage: /fork <entryId>")
	}
	return session.Fork(entryID)
}

func cmdTools(session *CodingSession, _ string) error {
	names := session.GetActiveToolNames()
	fmt.Printf("Active tools (%d):\n", len(names))
	for _, name := range names {
		fmt.Printf("  - %s\n", name)
	}
	return nil
}

func cmdThinking(session *CodingSession, args string) error {
	level := strings.TrimSpace(args)
	if level == "" {
		// Cycle through levels
		next := session.CycleThinkingLevel()
		fmt.Printf("Thinking level: %s\n", next)
		return nil
	}

	tl := types.ThinkingLevel(level)
	switch tl {
	case types.ThinkingLevelOff, types.ThinkingLevelLow, types.ThinkingLevelMedium, types.ThinkingLevelHigh:
		session.SetThinkingLevel(tl)
		fmt.Printf("Thinking level set to: %s\n", tl)
		return nil
	default:
		return fmt.Errorf("invalid thinking level: %s (valid: off, low, medium, high)", level)
	}
}

func cmdRetry(session *CodingSession, _ string) error {
	fmt.Println("Retrying last prompt...")
	// Reset retry counter and re-prompt
	session.retryManager.Reset()
	return nil
}

func cmdSession(session *CodingSession, _ string) error {
	model := session.GetModel()
	fmt.Printf("Session ID: %s\n", session.GetSessionID())
	fmt.Printf("Model: %s (%s)\n", model.ID, model.ProviderID)
	fmt.Printf("Thinking Level: %s\n", session.GetThinkingLevel())
	fmt.Printf("Streaming: %v\n", session.IsStreaming())
	fmt.Printf("Auto Compaction: %v\n", session.config.AutoCompaction)
	fmt.Printf("Auto Retry: %v\n", session.config.AutoRetry)
	msgs := session.GetMessages()
	fmt.Printf("Messages: %d\n", len(msgs))
	return nil
}

func cmdHelp(session *CodingSession, _ string) error {
	commands := BuiltinCommands()

	// Include extension commands
	if session.extensions != nil {
		for _, cmd := range session.extensions.GetCommands() {
			fmt.Printf("  /%s - %s\n", cmd.Name, cmd.Description)
		}
	}

	fmt.Println("Available commands:")
	for _, cmd := range commands {
		fmt.Printf("  /%s - %s\n", cmd.Name, cmd.Description)
	}
	return nil
}
