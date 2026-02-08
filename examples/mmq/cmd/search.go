package cmd

import (
	"fmt"

	"github.com/crosszan/modu/examples/mmq/format"
	"github.com/crosszan/modu/pkg/mmq"
	"github.com/spf13/cobra"
)

// search 命令 - BM25 全文搜索
var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "BM25 full-text search",
	Long:  "Search documents using BM25 keyword search",
	Args:  cobra.ExactArgs(1),
	RunE:  runSearch,
}

// vsearch 命令 - 向量语义搜索
var vsearchCmd = &cobra.Command{
	Use:   "vsearch <query>",
	Short: "Vector semantic search",
	Long:  "Search documents using vector similarity (requires embeddings)",
	Args:  cobra.ExactArgs(1),
	RunE:  runVSearch,
}

// query 命令 - 混合搜索 + 重排
var queryCmd = &cobra.Command{
	Use:   "query <query>",
	Short: "Hybrid search with reranking",
	Long:  "Search using hybrid strategy (BM25 + Vector + LLM reranking) for best quality",
	Args:  cobra.ExactArgs(1),
	RunE:  runQuery,
}

var (
	numResults int
	minScore   float64
	showAll    bool
)

func init() {
	// search 标志
	searchCmd.Flags().IntVarP(&numResults, "num", "n", 10, "Number of results")
	searchCmd.Flags().Float64Var(&minScore, "min-score", 0.0, "Minimum score threshold")
	searchCmd.Flags().BoolVar(&showAll, "all", false, "Return all matches")
	searchCmd.Flags().BoolVar(&fullContent, "full", false, "Show full content")

	// vsearch 标志
	vsearchCmd.Flags().IntVarP(&numResults, "num", "n", 10, "Number of results")
	vsearchCmd.Flags().Float64Var(&minScore, "min-score", 0.0, "Minimum score threshold")
	vsearchCmd.Flags().BoolVar(&showAll, "all", false, "Return all matches")
	vsearchCmd.Flags().BoolVar(&fullContent, "full", false, "Show full content")

	// query 标志
	queryCmd.Flags().IntVarP(&numResults, "num", "n", 10, "Number of results")
	queryCmd.Flags().Float64Var(&minScore, "min-score", 0.0, "Minimum score threshold")
	queryCmd.Flags().BoolVar(&showAll, "all", false, "Return all matches")
	queryCmd.Flags().BoolVar(&fullContent, "full", false, "Show full content")
}

func runSearch(cmd *cobra.Command, args []string) error {
	query := args[0]

	m, err := getMMQ()
	if err != nil {
		return err
	}
	defer m.Close()

	limit := numResults
	if showAll {
		limit = 0 // 0 表示不限制
	}

	results, err := m.Search(query, mmq.SearchOptions{
		Limit:      limit,
		MinScore:   minScore,
		Collection: collectionFlag,
	})

	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}

	if len(results) == 0 {
		fmt.Println("No results found")
		return nil
	}

	fmt.Printf("Found %d result(s)\n\n", len(results))
	return format.OutputSearchResults(results, format.Format(outputFormat), fullContent)
}

func runVSearch(cmd *cobra.Command, args []string) error {
	query := args[0]

	m, err := getMMQ()
	if err != nil {
		return err
	}
	defer m.Close()

	// 检查是否有嵌入
	status, _ := m.Status()
	if status.NeedsEmbedding > 0 {
		fmt.Printf("Warning: %d documents need embeddings. Run 'mmq embed' first.\n\n", status.NeedsEmbedding)
	}

	limit := numResults
	if showAll {
		limit = 0
	}

	results, err := m.VectorSearch(query, mmq.SearchOptions{
		Limit:      limit,
		MinScore:   minScore,
		Collection: collectionFlag,
	})

	if err != nil {
		return fmt.Errorf("vector search failed: %w", err)
	}

	if len(results) == 0 {
		fmt.Println("No results found")
		fmt.Println("Make sure documents have embeddings (run 'mmq embed')")
		return nil
	}

	fmt.Printf("Found %d result(s)\n\n", len(results))
	return format.OutputSearchResults(results, format.Format(outputFormat), fullContent)
}

func runQuery(cmd *cobra.Command, args []string) error {
	query := args[0]

	m, err := getMMQ()
	if err != nil {
		return err
	}
	defer m.Close()

	// 检查是否有嵌入
	status, _ := m.Status()
	if status.NeedsEmbedding > 0 {
		fmt.Printf("Warning: %d documents need embeddings. Run 'mmq embed' for better results.\n\n", status.NeedsEmbedding)
	}

	limit := numResults
	if showAll {
		limit = 0
	}

	// 使用混合检索策略
	results, err := m.RetrieveContext(query, mmq.RetrieveOptions{
		Limit:      limit,
		MinScore:   minScore,
		Collection: collectionFlag,
		Strategy:   mmq.StrategyHybrid,
		Rerank:     true, // 启用重排 (支持 MockLLM 和 LlamaCpp)
	})

	if err != nil {
		return fmt.Errorf("hybrid search failed: %w", err)
	}

	// 转换为 SearchResult
	searchResults := make([]mmq.SearchResult, len(results))
	for i, ctx := range results {
		searchResults[i] = mmq.SearchResult{
			Score:      ctx.Relevance,
			Title:      getMetadata(ctx.Metadata, "title"),
			Content:    ctx.Text,
			Snippet:    getMetadata(ctx.Metadata, "snippet"),
			Source:     getMetadata(ctx.Metadata, "source"),
			Collection: getMetadata(ctx.Metadata, "collection"),
			Path:       getMetadata(ctx.Metadata, "path"),
		}
	}

	if len(searchResults) == 0 {
		fmt.Println("No results found")
		return nil
	}

	fmt.Printf("Found %d result(s) using hybrid search\n\n", len(searchResults))
	return format.OutputSearchResults(searchResults, format.Format(outputFormat), fullContent)
}

// getMetadata 从元数据中获取字符串值
func getMetadata(metadata map[string]interface{}, key string) string {
	if val, ok := metadata[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}
