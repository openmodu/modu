package llm

import (
	"context"
	"time"
)

// LLM 大语言模型接口
type LLM interface {
	// Embed 生成文本的嵌入向量
	// isQuery: true表示查询文本，false表示文档文本
	Embed(text string, isQuery bool) ([]float32, error)

	// EmbedBatch 批量生成嵌入向量
	EmbedBatch(texts []string, isQuery bool) ([][]float32, error)

	// Rerank 重新排序文档
	Rerank(query string, docs []Document) ([]RerankResult, error)

	// Generate 生成文本（用于查询扩展等）
	Generate(prompt string, opts GenerateOptions) (string, error)

	// ExpandQuery 查询扩展，生成查询变体以提高检索召回率
	ExpandQuery(query string) ([]QueryExpansion, error)

	// Close 关闭并释放资源
	Close() error

	// IsLoaded 检查模型是否已加载
	IsLoaded(modelType ModelType) bool

	// SetModelPath 设置模型路径
	SetModelPath(modelType ModelType, path string)
}

// ModelType 模型类型
type ModelType string

const (
	ModelTypeEmbedding ModelType = "embedding" // 嵌入模型
	ModelTypeRerank    ModelType = "rerank"    // 重排模型
	ModelTypeGenerate  ModelType = "generate"  // 生成模型
)

// Document 用于重排的文档
type Document struct {
	ID      string
	Content string
	Title   string
}

// RerankResult 重排结果
type RerankResult struct {
	ID    string
	Score float64
	Index int
}

// GenerateOptions 生成选项
type GenerateOptions struct {
	Temperature float32
	TopK        int
	TopP        float32
	MaxTokens   int
	StopWords   []string
	Context     context.Context
}

// DefaultGenerateOptions 默认生成选项
func DefaultGenerateOptions() GenerateOptions {
	return GenerateOptions{
		Temperature: 0.7,
		TopK:        40,
		TopP:        0.9,
		MaxTokens:   512,
		Context:     context.Background(),
	}
}

// ModelConfig 模型配置
type ModelConfig struct {
	ModelPath   string        // 模型文件路径
	ContextSize int           // 上下文大小
	Threads     int           // 线程数
	BatchSize   int           // 批处理大小
	GPU         bool          // 是否使用GPU
	Timeout     time.Duration // 超时时间
	CacheDir    string        // 模型缓存目录
	LibPath     string        // yzma 库路径（YZMA_LIB）
}

// DefaultModelConfig 默认模型配置
func DefaultModelConfig() ModelConfig {
	return ModelConfig{
		ContextSize: 512,
		Threads:     4,
		BatchSize:   512,
		GPU:         false,
		Timeout:     5 * time.Minute,
	}
}

// EmbeddingInfo 嵌入信息
type EmbeddingInfo struct {
	Dimensions int    // 向量维度
	Model      string // 模型名称
	MaxTokens  int    // 最大token数
}

// QueryExpansion 查询扩展结果
type QueryExpansion struct {
	Type    string  // lex/vec/hyde
	Text    string  // 扩展后的文本
	Weight  float64 // 权重
	Metrics map[string]interface{}
}
