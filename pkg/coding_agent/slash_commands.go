package coding_agent

import (
	"fmt"
	"strings"
)

// SlashCommand represents a built-in slash command.
type SlashCommand struct {
	Name        string
	Description string
	Handler     func(session *CodingSession, args string) error
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
			Name:        "settings",
			Description: "Show current settings",
			Handler:     cmdSettings,
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

func cmdSettings(session *CodingSession, _ string) error {
	cfg := session.config
	fmt.Printf("Thinking Level: %s\n", cfg.ThinkingLevel)
	fmt.Printf("Auto Compaction: %v\n", cfg.AutoCompaction)
	fmt.Printf("Default Provider: %s\n", cfg.DefaultProvider)
	fmt.Printf("Default Model: %s\n", cfg.DefaultModel)
	return nil
}

func cmdTools(session *CodingSession, _ string) error {
	names := session.GetActiveToolNames()
	fmt.Printf("Active tools (%d):\n", len(names))
	for _, name := range names {
		fmt.Printf("  - %s\n", name)
	}
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
