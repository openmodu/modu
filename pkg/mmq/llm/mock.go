//go:build !llama
// +build !llama

package llm

import (
	"crypto/rand"
	"fmt"
	"math"
	"sort"
)

// MockLLM 模拟LLM实现（用于测试和开发）
type MockLLM struct {
	dimensions int
	loaded     map[ModelType]bool
}

// NewMockLLM 创建模拟LLM实例
func NewMockLLM(dimensions int) *MockLLM {
	return &MockLLM{
		dimensions: dimensions,
		loaded:     make(map[ModelType]bool),
	}
}

// Embed 生成模拟嵌入向量
func (m *MockLLM) Embed(text string, isQuery bool) ([]float32, error) {
	if text == "" {
		return nil, fmt.Errorf("empty text")
	}

	m.loaded[ModelTypeEmbedding] = true

	// 生成确定性的伪随机向量
	embedding := make([]float32, m.dimensions)

	// 使用文本内容的哈希作为种子
	seed := uint32(0)
	for _, c := range text {
		seed = seed*31 + uint32(c)
	}

	// 生成向量
	for i := range embedding {
		seed = seed*1103515245 + 12345
		embedding[i] = float32(int32(seed)) / float32(math.MaxInt32)
	}

	// 归一化
	return normalizeVector(embedding), nil
}

// EmbedBatch 批量生成嵌入
func (m *MockLLM) EmbedBatch(texts []string, isQuery bool) ([][]float32, error) {
	embeddings := make([][]float32, len(texts))

	for i, text := range texts {
		embedding, err := m.Embed(text, isQuery)
		if err != nil {
			return nil, err
		}
		embeddings[i] = embedding
	}

	return embeddings, nil
}

// Rerank 模拟重排
func (m *MockLLM) Rerank(query string, docs []Document) ([]RerankResult, error) {
	m.loaded[ModelTypeRerank] = true

	results := make([]RerankResult, len(docs))

	// 简单的文本相似度模拟
	for i, doc := range docs {
		score := computeSimpleTextSimilarity(query, doc.Content)
		results[i] = RerankResult{
			ID:    doc.ID,
			Score: score,
			Index: i,
		}
	}

	// 排序
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results, nil
}

// Generate 生成文本
func (m *MockLLM) Generate(prompt string, opts GenerateOptions) (string, error) {
	m.loaded[ModelTypeGenerate] = true

	// 简单的模拟生成
	return fmt.Sprintf("Mock generated response for: %s", prompt), nil
}

// ExpandQuery 查询扩展（模拟实现）
func (m *MockLLM) ExpandQuery(query string) ([]QueryExpansion, error) {
	m.loaded[ModelTypeGenerate] = true

	// 返回模拟的查询扩展结果
	// 包含原始查询的不同变体
	return []QueryExpansion{
		{
			Type:   "lex",
			Text:   query,
			Weight: 1.0,
		},
		{
			Type:   "vec",
			Text:   query + " explanation",
			Weight: 0.8,
		},
		{
			Type:   "hyde",
			Text:   "This document explains " + query,
			Weight: 0.6,
		},
	}, nil
}

// Close 关闭
func (m *MockLLM) Close() error {
	m.loaded = make(map[ModelType]bool)
	return nil
}

// IsLoaded 检查模型是否已加载
func (m *MockLLM) IsLoaded(modelType ModelType) bool {
	return m.loaded[modelType]
}

// SetModelPath 设置模型路径（模拟实现）
func (m *MockLLM) SetModelPath(modelType ModelType, path string) {
	// 模拟无需设置路径
}

// computeSimpleTextSimilarity 计算简单的文本相似度
func computeSimpleTextSimilarity(query, doc string) float64 {
	// 简化版本：统计共同词汇
	queryWords := splitWords(query)
	docWords := splitWords(doc)

	common := 0
	for _, qw := range queryWords {
		for _, dw := range docWords {
			if qw == dw {
				common++
				break
			}
		}
	}

	if len(queryWords) == 0 {
		return 0
	}

	return float64(common) / float64(len(queryWords))
}

// splitWords 简单分词
func splitWords(text string) []string {
	var words []string
	var word []rune

	for _, r := range text {
		if r == ' ' || r == '\n' || r == '\t' {
			if len(word) > 0 {
				words = append(words, string(word))
				word = nil
			}
		} else {
			word = append(word, r)
		}
	}

	if len(word) > 0 {
		words = append(words, string(word))
	}

	return words
}

// generateRandomVector 生成随机向量
func generateRandomVector(dimensions int) []float32 {
	vec := make([]float32, dimensions)
	bytes := make([]byte, dimensions*4)

	rand.Read(bytes)

	for i := 0; i < dimensions; i++ {
		vec[i] = float32(int32(bytes[i*4])<<24|int32(bytes[i*4+1])<<16|
			int32(bytes[i*4+2])<<8|int32(bytes[i*4+3])) / float32(math.MaxInt32)
	}

	return normalizeVector(vec)
}
