package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/crosszan/modu/pkg/mmq"
	"github.com/spf13/cobra"
)

var (
	// DefaultDBPath 默认数据库路径
	DefaultDBPath string

	// Version 版本号
	Version string

	// BuildTime 构建时间
	BuildTime string

	// 全局标志
	dbPath         string
	collectionFlag string
	outputFormat   string
)

// rootCmd represents the base command
var rootCmd = &cobra.Command{
	Use:   "mmq",
	Short: "Modu Memory & Query - RAG and memory management",
	Long: `MMQ is a local-first RAG engine and memory management system.
It provides hybrid search (BM25 + Vector + LLM reranking) and persistent memory storage.`,
	Version: Version,
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	// 全局标志
	rootCmd.PersistentFlags().StringVarP(&dbPath, "db", "d", DefaultDBPath, "Database path")
	rootCmd.PersistentFlags().StringVarP(&collectionFlag, "collection", "c", "", "Collection filter")
	rootCmd.PersistentFlags().StringVarP(&outputFormat, "format", "f", "text", "Output format (text|json|csv|md|xml)")

	// 添加子命令
	rootCmd.AddCommand(collectionCmd)
	rootCmd.AddCommand(contextCmd)
	rootCmd.AddCommand(lsCmd)
	rootCmd.AddCommand(getCmd)
	rootCmd.AddCommand(multiGetCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(embedCmd)
	rootCmd.AddCommand(searchCmd)
	rootCmd.AddCommand(vsearchCmd)
	rootCmd.AddCommand(queryCmd)

	// 版本模板
	rootCmd.SetVersionTemplate(fmt.Sprintf("mmq version %s (built %s)\n", Version, BuildTime))
}

// getMMQ 获取MMQ实例（辅助函数）
func getMMQ() (*mmq.MMQ, error) {
	// 确保数据库目录存在
	dbDir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	m, err := mmq.NewWithDB(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	return m, nil
}
