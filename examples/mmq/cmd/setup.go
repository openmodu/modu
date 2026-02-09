package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/crosszan/modu/pkg/mmq/llm"
	"github.com/spf13/cobra"
)

// fallback 版本号，当 yzma 无法自动获取 latest 时使用
const llamaCppFallbackVersion = "b7974"

func init() {
	rootCmd.AddCommand(setupCmd)
}

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Install yzma library and download models",
	Long: `Setup the local inference environment:

1. Check/install yzma library (llama.cpp via purego FFI)
2. Download embedding models from HuggingFace

After setup, set YZMA_LIB to the library path:
  export YZMA_LIB=~/.cache/modu/lib`,
	RunE: runSetup,
}

func runSetup(cmd *cobra.Command, args []string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	libDir := filepath.Join(homeDir, ".cache", "modu", "lib")
	modelsDir := filepath.Join(homeDir, ".cache", "modu", "models")

	// Step 1: 检查 YZMA_LIB
	fmt.Println("=== Step 1: Check yzma library ===")
	libPath := os.Getenv("YZMA_LIB")
	if libPath != "" {
		if hasLlamaLib(libPath) {
			fmt.Printf("  YZMA_LIB is set: %s\n", libPath)
		} else {
			fmt.Printf("  YZMA_LIB is set but no library found at: %s\n", libPath)
			libPath = ""
		}
	}

	if libPath == "" && hasLlamaLib(libDir) {
		libPath = libDir
		fmt.Printf("  Found library at: %s\n", libPath)
	}

	if libPath == "" {
		fmt.Println("  Library not found. Installing yzma...")

		// 检查 yzma 命令是否可用
		yzmaBin, err := exec.LookPath("yzma")
		if err != nil {
			fmt.Println("  Installing yzma CLI tool...")
			installCmd := exec.Command("go", "install", "github.com/hybridgroup/yzma/cmd/yzma@latest")
			installCmd.Stdout = os.Stdout
			installCmd.Stderr = os.Stderr
			if err := installCmd.Run(); err != nil {
				return fmt.Errorf("failed to install yzma: %w\n\nManual install:\n  go install github.com/hybridgroup/yzma/cmd/yzma@latest", err)
			}
			yzmaBin = "yzma"
		}

		// 使用 yzma install 下载 llama.cpp 库
		fmt.Printf("  Downloading llama.cpp library to %s...\n", libDir)
		if err := os.MkdirAll(libDir, 0755); err != nil {
			return fmt.Errorf("failed to create lib directory: %w", err)
		}

		// 检测 processor（macOS 优先 metal）
		processor := "cpu"
		if runtime.GOOS == "darwin" {
			processor = "metal"
		}

		// 先尝试不指定版本（自动获取 latest）
		installArgs := []string{"install", "--lib", libDir, "--processor", processor}
		installCmd := exec.Command(yzmaBin, installArgs...)
		installCmd.Stdout = os.Stdout
		installCmd.Stderr = os.Stderr
		if err := installCmd.Run(); err != nil {
			// fallback：指定版本号（GitHub API rate limit 时）
			fmt.Printf("  Auto-detect failed, trying fallback version %s...\n", llamaCppFallbackVersion)
			installArgs = append(installArgs, "--version", llamaCppFallbackVersion)
			installCmd = exec.Command(yzmaBin, installArgs...)
			installCmd.Stdout = os.Stdout
			installCmd.Stderr = os.Stderr
			if err := installCmd.Run(); err != nil {
				return fmt.Errorf("failed to download llama.cpp library: %w", err)
			}
		}

		if !hasLlamaLib(libDir) {
			return fmt.Errorf("library installed but libllama not found in %s", libDir)
		}
		libPath = libDir
	}

	fmt.Printf("  Library ready: %s\n", libPath)
	fmt.Println()

	// Step 2: 下载模型
	fmt.Println("=== Step 2: Download models ===")
	opts := llm.DefaultDownloadOptions()
	opts.CacheDir = modelsDir

	models := []struct {
		name string
		ref  llm.HFRef
	}{
		{"Embedding", llm.EmbeddingModelRef},
	}

	for _, m := range models {
		localPath := filepath.Join(modelsDir, m.ref.Filename)
		if _, err := os.Stat(localPath); err == nil {
			info, _ := os.Stat(localPath)
			fmt.Printf("  %s: %s (cached, %s)\n", m.name, m.ref.Filename, formatBytes(info.Size()))
			continue
		}

		fmt.Printf("  Downloading %s: %s\n", m.name, m.ref.Filename)
		downloader := llm.NewDownloader(opts)
		path, err := downloader.Download(m.ref)
		if err != nil {
			return fmt.Errorf("failed to download %s model: %w", m.name, err)
		}
		info, _ := os.Stat(path)
		fmt.Printf("  %s downloaded (%s)\n", m.name, formatBytes(info.Size()))
	}
	fmt.Println()

	// Step 3: 输出配置指引
	fmt.Println("=== Setup complete! ===")
	fmt.Println()
	fmt.Println("Add to your shell profile:")
	fmt.Printf("  export YZMA_LIB=%s\n", libPath)
	fmt.Println()
	fmt.Println("Then run:")
	fmt.Println("  mmq update    # index documents")
	fmt.Println("  mmq embed     # generate embeddings")
	fmt.Println("  mmq vsearch \"query\"  # vector search")

	return nil
}

// hasLlamaLib 检查目录中是否存在 libllama 动态库
func hasLlamaLib(dir string) bool {
	var pattern string
	switch runtime.GOOS {
	case "darwin":
		pattern = "libllama*.dylib"
	case "linux":
		pattern = "libllama*.so"
	default:
		pattern = "llama*.dll"
	}

	matches, _ := filepath.Glob(filepath.Join(dir, pattern))
	return len(matches) > 0
}
