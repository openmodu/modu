package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/crosszan/modu/pkg/mmq/llm"
	"github.com/spf13/cobra"
)

var (
	pullRefresh bool
	pullCache   string
)

func init() {
	rootCmd.AddCommand(pullCmd)

	pullCmd.Flags().BoolVar(&pullRefresh, "refresh", false, "Force refresh/re-download models")
	pullCmd.Flags().StringVar(&pullCache, "cache-dir", "", "Model cache directory (default: ~/.cache/modu/models)")
}

var pullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Download default models from HuggingFace",
	Long: `Download default GGUF models from HuggingFace Hub.

Models downloaded:
  - Embedding: ggml-org/embeddinggemma-300M-GGUF (300M parameters)
  - Rerank: Qwen/qwen3-reranker-0.6b-gguf (0.6B parameters)
  - Generate: Qwen/Qwen3-0.6B-GGUF (0.6B parameters)

Models are cached in ~/.cache/modu/models/ by default.
Use --refresh to force re-download even if models already exist.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Determine cache directory
		cacheDir := pullCache
		if cacheDir == "" {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("failed to get home directory: %w", err)
			}
			cacheDir = filepath.Join(homeDir, ".cache", "modu", "models")
		}

		fmt.Printf("Pulling models to: %s\n", cacheDir)
		fmt.Println()

		// Setup downloader options
		opts := llm.DefaultDownloadOptions()
		opts.CacheDir = cacheDir
		opts.ForceDownload = pullRefresh

		// Models to download
		models := map[string]llm.HFRef{
			"Embedding": llm.EmbeddingModelRef,
			"Rerank":    llm.RerankModelRef,
			"Generate":  llm.GenerateModelRef,
		}

		// Download each model
		for name, ref := range models {
			// Check if already exists
			localPath := filepath.Join(cacheDir, ref.Filename)
			if !pullRefresh {
				if _, err := os.Stat(localPath); err == nil {
					// File exists, check if up-to-date
					fmt.Printf("✓ %s model: %s (cached)\n", name, ref.Filename)
					info, _ := os.Stat(localPath)
					fmt.Printf("  Size: %s\n", formatBytes(info.Size()))
					fmt.Printf("  Path: %s\n", localPath)
					fmt.Println()
					continue
				}
			}

			// Download
			fmt.Printf("⬇ Downloading %s model: %s\n", name, ref.Filename)
			fmt.Printf("  From: https://huggingface.co/%s\n", ref.Repo)

			// Add progress callback
			var lastPct int
			opts.ProgressFunc = func(downloaded, total int64) {
				if total > 0 {
					pct := int(downloaded * 100 / total)
					if pct != lastPct && pct%10 == 0 {
						fmt.Printf("  Progress: %d%% (%s / %s)\n", pct, formatBytes(downloaded), formatBytes(total))
						lastPct = pct
					}
				}
			}

			downloader := llm.NewDownloader(opts)
			path, err := downloader.Download(ref)
			if err != nil {
				return fmt.Errorf("failed to download %s model: %w", name, err)
			}

			info, _ := os.Stat(path)
			fmt.Printf("✓ %s model downloaded\n", name)
			fmt.Printf("  Size: %s\n", formatBytes(info.Size()))
			fmt.Printf("  Path: %s\n", path)
			fmt.Println()
		}

		fmt.Println("All models ready!")
		return nil
	},
}

// formatBytes formats bytes as human-readable string
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
