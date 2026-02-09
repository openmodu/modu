package cmd

import (
	"fmt"

	"github.com/crosszan/modu/examples/mmq/format"
	"github.com/spf13/cobra"
)

var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Manage contexts",
	Long:  "Add, list, check, and remove context information",
}

var contextAddCmd = &cobra.Command{
	Use:   "add [path] <content>",
	Short: "Add context for a path",
	Long: `Add context information for a path.

Examples:
  mmq context add "Global context"              # Add to current directory
  mmq context add / "Global context"            # Add global context
  mmq context add mmq://docs "Documentation"    # Add for collection
  mmq context add mmq://docs/api "API docs"     # Add for path`,
	Args: cobra.MinimumNArgs(1),
	RunE: runContextAdd,
}

var contextListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all contexts",
	RunE:  runContextList,
}

var contextCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Check for missing contexts",
	RunE:  runContextCheck,
}

var contextRmCmd = &cobra.Command{
	Use:   "rm <path>",
	Short: "Remove context",
	Args:  cobra.ExactArgs(1),
	RunE:  runContextRm,
}

func init() {
	contextCmd.AddCommand(contextAddCmd)
	contextCmd.AddCommand(contextListCmd)
	contextCmd.AddCommand(contextCheckCmd)
	contextCmd.AddCommand(contextRmCmd)
}

func runContextAdd(cmd *cobra.Command, args []string) error {
	var path, content string

	if len(args) == 1 {
		// mmq context add "content" -> 使用当前目录或"/"
		path = "/"
		content = args[0]
	} else {
		// mmq context add path "content"
		path = args[0]
		content = args[1]
	}

	m, err := getMMQ()
	if err != nil {
		return err
	}
	defer m.Close()

	err = m.AddContext(path, content)
	if err != nil {
		return fmt.Errorf("failed to add context: %w", err)
	}

	fmt.Printf("Added context for '%s'\n", path)
	return nil
}

func runContextList(cmd *cobra.Command, args []string) error {
	m, err := getMMQ()
	if err != nil {
		return err
	}
	defer m.Close()

	contexts, err := m.ListContexts()
	if err != nil {
		return fmt.Errorf("failed to list contexts: %w", err)
	}

	if len(contexts) == 0 {
		fmt.Println("No contexts found")
		fmt.Println("Use 'mmq context add <path> <content>' to create one")
		return nil
	}

	return format.OutputContexts(contexts, format.Format(outputFormat))
}

func runContextCheck(cmd *cobra.Command, args []string) error {
	m, err := getMMQ()
	if err != nil {
		return err
	}
	defer m.Close()

	missing, err := m.CheckMissingContexts()
	if err != nil {
		return fmt.Errorf("failed to check contexts: %w", err)
	}

	if len(missing) == 0 {
		fmt.Println("✓ All collections have contexts")
		return nil
	}

	fmt.Printf("Missing contexts for %d paths:\n\n", len(missing))
	for _, path := range missing {
		fmt.Printf("  - %s\n", path)
	}

	fmt.Println("\nUse 'mmq context add <path> <content>' to add context")
	return nil
}

func runContextRm(cmd *cobra.Command, args []string) error {
	path := args[0]

	m, err := getMMQ()
	if err != nil {
		return err
	}
	defer m.Close()

	err = m.RemoveContext(path)
	if err != nil {
		return fmt.Errorf("failed to remove context: %w", err)
	}

	fmt.Printf("Removed context for '%s'\n", path)
	return nil
}
