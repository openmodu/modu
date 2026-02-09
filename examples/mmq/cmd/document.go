package cmd

import (
	"fmt"
	"strings"

	"github.com/crosszan/modu/examples/mmq/format"
	"github.com/crosszan/modu/pkg/mmq"
	"github.com/spf13/cobra"
)

// ls 命令
var lsCmd = &cobra.Command{
	Use:   "ls [collection[/path]]",
	Short: "List collections or documents",
	Long: `List collections or files in a collection.

Examples:
  mmq ls                    # List all documents
  mmq ls docs               # List documents in 'docs' collection
  mmq ls docs/api           # List documents in 'docs/api' path
  mmq ls mmq://docs/2024    # List using mmq:// URI`,
	Args: cobra.MaximumNArgs(1),
	RunE: runLs,
}

// get 命令
var getCmd = &cobra.Command{
	Use:   "get <file>",
	Short: "Get document by path or docid",
	Long: `Get a document by its path or docid.

Examples:
  mmq get docs/readme.md       # Get by path
  mmq get mmq://docs/readme.md # Get by mmq:// URI
  mmq get "#abc123"            # Get by docid
  mmq get abc123               # Get by docid (# optional)`,
	Args: cobra.ExactArgs(1),
	RunE: runGet,
}

// multi-get 命令
var multiGetCmd = &cobra.Command{
	Use:   "multi-get <pattern>",
	Short: "Get multiple documents",
	Long: `Get multiple documents by pattern.

Supports:
  - Comma-separated docids: #abc123, #def456
  - Comma-separated paths: docs/a.md, docs/b.md
  - Glob patterns: docs/**/*.md

Examples:
  mmq multi-get "#abc123, #def456"
  mmq multi-get "docs/a.md, docs/b.md"
  mmq multi-get "docs/**/*.md"
  mmq multi-get "docs/*.md" -l 100`,
	Args: cobra.ExactArgs(1),
	RunE: runMultiGet,
}

var (
	fullContent  bool
	lineNumbers  bool
	maxLines     int
	maxBytes     int
)

func init() {
	// get 标志
	getCmd.Flags().BoolVar(&fullContent, "full", false, "Show full content")
	getCmd.Flags().BoolVar(&lineNumbers, "line-numbers", false, "Add line numbers")

	// multi-get 标志
	multiGetCmd.Flags().BoolVar(&fullContent, "full", false, "Show full content")
	multiGetCmd.Flags().BoolVar(&lineNumbers, "line-numbers", false, "Add line numbers")
	multiGetCmd.Flags().IntVarP(&maxLines, "lines", "l", 0, "Maximum lines per file")
	multiGetCmd.Flags().IntVar(&maxBytes, "max-bytes", 10240, "Skip files larger than this (0=no limit)")
}

func runLs(cmd *cobra.Command, args []string) error {
	var coll, path string

	if len(args) > 0 {
		// 解析 collection[/path]
		arg := strings.TrimPrefix(strings.TrimPrefix(args[0], "mmq://"), "qmd://")
		parts := strings.SplitN(arg, "/", 2)
		coll = parts[0]
		if len(parts) > 1 {
			path = parts[1]
		}
	}

	m, err := getMMQ()
	if err != nil {
		return err
	}
	defer m.Close()

	docs, err := m.ListDocuments(coll, path)
	if err != nil {
		return fmt.Errorf("failed to list documents: %w", err)
	}

	if len(docs) == 0 {
		if coll == "" {
			fmt.Println("No documents found")
			fmt.Println("Use 'mmq collection add' to create a collection")
		} else {
			fmt.Printf("No documents found in %s\n", args[0])
		}
		return nil
	}

	return format.OutputDocumentList(docs, format.Format(outputFormat))
}

func runGet(cmd *cobra.Command, args []string) error {
	identifier := args[0]

	m, err := getMMQ()
	if err != nil {
		return err
	}
	defer m.Close()

	var doc *mmq.DocumentDetail

	// 判断是 docid 还是路径
	if strings.HasPrefix(identifier, "#") || !strings.Contains(identifier, "/") {
		// 按 docid 获取
		d, err := m.GetDocumentByID(identifier)
		if err != nil {
			return fmt.Errorf("failed to get document: %w", err)
		}
		doc = d
	} else {
		// 按路径获取
		d, err := m.GetDocumentByPath(identifier)
		if err != nil {
			return fmt.Errorf("failed to get document: %w", err)
		}
		doc = d
	}

	return format.OutputDocumentDetail(doc, format.Format(outputFormat), fullContent, lineNumbers)
}

func runMultiGet(cmd *cobra.Command, args []string) error {
	pattern := args[0]

	m, err := getMMQ()
	if err != nil {
		return err
	}
	defer m.Close()

	docs, err := m.GetMultipleDocuments(pattern, maxBytes)
	if err != nil {
		return fmt.Errorf("failed to get documents: %w", err)
	}

	if len(docs) == 0 {
		fmt.Println("No documents found")
		return nil
	}

	// 限制行数
	if maxLines > 0 {
		for i := range docs {
			lines := strings.Split(docs[i].Content, "\n")
			if len(lines) > maxLines {
				docs[i].Content = strings.Join(lines[:maxLines], "\n") + "\n..."
			}
		}
	}

	return format.OutputDocumentDetails(docs, format.Format(outputFormat), fullContent, lineNumbers)
}
