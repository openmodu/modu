// +build llama

package llm

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	llama "github.com/go-skynet/go-llama.cpp"
)

// LlamaCpp llama.cpp实现
type LlamaCpp struct {
	// 模型路径
	embeddingModelPath string
	rerankModelPath    string
	generateModelPath  string

	// 模型实例
	embeddingModel *llama.LLama
	rerankModel    *llama.LLama
	generateModel  *llama.LLama

	// 配置
	config ModelConfig

	// 互斥锁
	mu sync.RWMutex

	// 最后使用时间
	lastUsed map[ModelType]time.Time

	// 超时定时器
	timers map[ModelType]*time.Timer
}

// NewLlamaCpp 创建LlamaCpp实例
func NewLlamaCpp(config ModelConfig) *LlamaCpp {
	return &LlamaCpp{
		config:   config,
		lastUsed: make(map[ModelType]time.Time),
		timers:   make(map[ModelType]*time.Timer),
	}
}

// SetModelPath 设置模型路径
func (l *LlamaCpp) SetModelPath(modelType ModelType, path string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	switch modelType {
	case ModelTypeEmbedding:
		l.embeddingModelPath = path
	case ModelTypeRerank:
		l.rerankModelPath = path
	case ModelTypeGenerate:
		l.generateModelPath = path
	}
}

// loadModel 加载模型
func (l *LlamaCpp) loadModel(modelType ModelType) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// 检查是否已加载
	if l.isModelLoaded(modelType) {
		l.lastUsed[modelType] = time.Now()
		return nil
	}

	var modelPath string
	switch modelType {
	case ModelTypeEmbedding:
		modelPath = l.embeddingModelPath
	case ModelTypeRerank:
		modelPath = l.rerankModelPath
	case ModelTypeGenerate:
		modelPath = l.generateModelPath
	default:
		return fmt.Errorf("unknown model type: %s", modelType)
	}

	if modelPath == "" {
		return fmt.Errorf("model path not set for type: %s", modelType)
	}

	// 检查文件是否存在
	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		return fmt.Errorf("model file not found: %s", modelPath)
	}

	// 加载模型
	model, err := llama.New(modelPath, llama.SetContext(l.config.ContextSize),
		llama.SetThreads(l.config.Threads),
		llama.EnableEmbeddings,
		llama.EnableF16Memory,
	)
	if err != nil {
		return fmt.Errorf("failed to load model: %w", err)
	}

	// 保存模型实例
	switch modelType {
	case ModelTypeEmbedding:
		l.embeddingModel = model
	case ModelTypeRerank:
		l.rerankModel = model
	case ModelTypeGenerate:
		l.generateModel = model
	}

	l.lastUsed[modelType] = time.Now()

	// 设置自动卸载定时器
	if l.config.Timeout > 0 {
		l.setUnloadTimer(modelType)
	}

	return nil
}

// isModelLoaded 检查模型是否已加载（无锁版本）
func (l *LlamaCpp) isModelLoaded(modelType ModelType) bool {
	switch modelType {
	case ModelTypeEmbedding:
		return l.embeddingModel != nil
	case ModelTypeRerank:
		return l.rerankModel != nil
	case ModelTypeGenerate:
		return l.generateModel != nil
	}
	return false
}

// IsLoaded 检查模型是否已加载（公开方法）
func (l *LlamaCpp) IsLoaded(modelType ModelType) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.isModelLoaded(modelType)
}

// setUnloadTimer 设置自动卸载定时器
func (l *LlamaCpp) setUnloadTimer(modelType ModelType) {
	// 取消旧定时器
	if timer, ok := l.timers[modelType]; ok {
		timer.Stop()
	}

	// 创建新定时器
	timer := time.AfterFunc(l.config.Timeout, func() {
		l.unloadModel(modelType)
	})
	l.timers[modelType] = timer
}

// unloadModel 卸载模型
func (l *LlamaCpp) unloadModel(modelType ModelType) {
	l.mu.Lock()
	defer l.mu.Unlock()

	switch modelType {
	case ModelTypeEmbedding:
		if l.embeddingModel != nil {
			l.embeddingModel.Free()
			l.embeddingModel = nil
		}
	case ModelTypeRerank:
		if l.rerankModel != nil {
			l.rerankModel.Free()
			l.rerankModel = nil
		}
	case ModelTypeGenerate:
		if l.generateModel != nil {
			l.generateModel.Free()
			l.generateModel = nil
		}
	}

	delete(l.lastUsed, modelType)
	if timer, ok := l.timers[modelType]; ok {
		timer.Stop()
		delete(l.timers, modelType)
	}
}

// Embed 生成嵌入向量
func (l *LlamaCpp) Embed(text string, isQuery bool) ([]float32, error) {
	// 加载模型
	if err := l.loadModel(ModelTypeEmbedding); err != nil {
		return nil, err
	}

	l.mu.RLock()
	model := l.embeddingModel
	l.mu.RUnlock()

	if model == nil {
		return nil, fmt.Errorf("embedding model not loaded")
	}

	// 格式化文本
	formatted := formatTextForEmbedding(text, isQuery)

	// 生成嵌入
	embedding, err := model.Embeddings(formatted)
	if err != nil {
		return nil, fmt.Errorf("failed to generate embedding: %w", err)
	}

	// 更新最后使用时间
	l.mu.Lock()
	l.lastUsed[ModelTypeEmbedding] = time.Now()
	if l.config.Timeout > 0 {
		l.setUnloadTimer(ModelTypeEmbedding)
	}
	l.mu.Unlock()

	return embedding, nil
}

// EmbedBatch 批量生成嵌入向量
func (l *LlamaCpp) EmbedBatch(texts []string, isQuery bool) ([][]float32, error) {
	embeddings := make([][]float32, len(texts))

	for i, text := range texts {
		embedding, err := l.Embed(text, isQuery)
		if err != nil {
			return nil, fmt.Errorf("failed to embed text %d: %w", i, err)
		}
		embeddings[i] = embedding
	}

	return embeddings, nil
}

// Rerank 重新排序文档
func (l *LlamaCpp) Rerank(query string, docs []Document) ([]RerankResult, error) {
	// 加载重排模型
	if err := l.loadModel(ModelTypeRerank); err != nil {
		return nil, err
	}

	l.mu.RLock()
	model := l.rerankModel
	l.mu.RUnlock()

	if model == nil {
		return nil, fmt.Errorf("rerank model not loaded")
	}

	results := make([]RerankResult, len(docs))

	// 为每个文档计算相关性分数
	for i, doc := range docs {
		// 构建提示
		prompt := fmt.Sprintf("Query: %s\nDocument: %s\nRelevant:", query, doc.Content)

		// 生成并计算分数
		// 注意：这是一个简化实现，实际可能需要使用专门的重排模型API
		ctx := context.Background()
		score, err := l.computeRelevanceScore(model, prompt, ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to compute score for doc %d: %w", i, err)
		}

		results[i] = RerankResult{
			ID:    doc.ID,
			Score: score,
			Index: i,
		}
	}

	// 更新最后使用时间
	l.mu.Lock()
	l.lastUsed[ModelTypeRerank] = time.Now()
	if l.config.Timeout > 0 {
		l.setUnloadTimer(ModelTypeRerank)
	}
	l.mu.Unlock()

	// 按分数排序
	sortRerankResults(results)

	return results, nil
}

// computeRelevanceScore 计算相关性分数
func (l *LlamaCpp) computeRelevanceScore(model *llama.LLama, prompt string, ctx context.Context) (float64, error) {
	// 简化实现：使用模型生成"yes"/"no"的概率作为分数
	// 实际实现可能需要更复杂的逻辑
	_, err := model.Predict(prompt,
		llama.SetTokens(10),
		llama.SetTopK(1),
		llama.SetTopP(1.0),
		llama.SetTemperature(0.0),
	)
	if err != nil {
		return 0, err
	}

	// 这里应该从logprobs中提取相关性分数
	// 简化版本：返回固定分数
	// TODO: 实现真正的分数计算
	return 0.5, nil
}

// Generate 生成文本
func (l *LlamaCpp) Generate(prompt string, opts GenerateOptions) (string, error) {
	// 加载生成模型
	if err := l.loadModel(ModelTypeGenerate); err != nil {
		return "", err
	}

	l.mu.RLock()
	model := l.generateModel
	l.mu.RUnlock()

	if model == nil {
		return "", fmt.Errorf("generate model not loaded")
	}

	// 生成文本
	result, err := model.Predict(prompt,
		llama.SetTokens(opts.MaxTokens),
		llama.SetTopK(opts.TopK),
		llama.SetTopP(opts.TopP),
		llama.SetTemperature(opts.Temperature),
		llama.SetStopWords(opts.StopWords...),
	)
	if err != nil {
		return "", fmt.Errorf("failed to generate text: %w", err)
	}

	// 更新最后使用时间
	l.mu.Lock()
	l.lastUsed[ModelTypeGenerate] = time.Now()
	if l.config.Timeout > 0 {
		l.setUnloadTimer(ModelTypeGenerate)
	}
	l.mu.Unlock()

	return result, nil
}

// ExpandQuery 查询扩展，生成查询变体
func (l *LlamaCpp) ExpandQuery(query string) ([]QueryExpansion, error) {
	// 构建提示词（参考 qmd 的实现）
	prompt := fmt.Sprintf(`Expand this search query into different variations. Output each on a new line in the format "type: content" where type is lex, vec, or hyde.

Query: %s

Expansions:`, query)

	// 设置生成选项
	opts := GenerateOptions{
		Temperature: 0.7,
		TopK:        20,
		TopP:        0.8,
		MaxTokens:   600,
		StopWords:   []string{"\n\n"},
	}

	// 生成扩展查询
	result, err := l.Generate(prompt, opts)
	if err != nil {
		// 出错时返回原始查询作为后备
		return []QueryExpansion{
			{Type: "vec", Text: query, Weight: 1.0},
		}, nil
	}

	// 解析结果
	expansions := parseQueryExpansions(result, query)
	if len(expansions) == 0 {
		// 如果解析失败，返回默认扩展
		return []QueryExpansion{
			{Type: "lex", Text: query, Weight: 1.0},
			{Type: "vec", Text: query, Weight: 0.9},
			{Type: "hyde", Text: fmt.Sprintf("Information about %s", query), Weight: 0.7},
		}, nil
	}

	return expansions, nil
}

// parseQueryExpansions 解析查询扩展结果
func parseQueryExpansions(text, originalQuery string) []QueryExpansion {
	var expansions []QueryExpansion
	queryLower := toLower(originalQuery)
	queryTerms := splitIntoTerms(queryLower)

	lines := splitLines(text)
	for _, line := range lines {
		line = trimSpace(line)
		if line == "" {
			continue
		}

		// 查找冒号分隔符
		colonIdx := -1
		for i, r := range line {
			if r == ':' {
				colonIdx = i
				break
			}
		}

		if colonIdx == -1 {
			continue
		}

		typ := trimSpace(line[:colonIdx])
		content := trimSpace(line[colonIdx+1:])

		// 验证类型
		if typ != "lex" && typ != "vec" && typ != "hyde" {
			continue
		}

		// 验证内容包含查询词
		if !hasAnyTerm(content, queryTerms) {
			continue
		}

		// 根据类型设置权重
		weight := 1.0
		switch typ {
		case "lex":
			weight = 1.0
		case "vec":
			weight = 0.8
		case "hyde":
			weight = 0.6
		}

		expansions = append(expansions, QueryExpansion{
			Type:   typ,
			Text:   content,
			Weight: weight,
		})
	}

	return expansions
}

// 辅助函数

func toLower(s string) string {
	result := make([]rune, 0, len(s))
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			result = append(result, r+32)
		} else {
			result = append(result, r)
		}
	}
	return string(result)
}

func trimSpace(s string) string {
	start := 0
	end := len(s)

	runes := []rune(s)
	for start < end && (runes[start] == ' ' || runes[start] == '\t' || runes[start] == '\n' || runes[start] == '\r') {
		start++
	}
	for end > start && (runes[end-1] == ' ' || runes[end-1] == '\t' || runes[end-1] == '\n' || runes[end-1] == '\r') {
		end--
	}

	return string(runes[start:end])
}

func splitLines(s string) []string {
	var lines []string
	var line []rune

	for _, r := range s {
		if r == '\n' {
			lines = append(lines, string(line))
			line = nil
		} else {
			line = append(line, r)
		}
	}

	if len(line) > 0 {
		lines = append(lines, string(line))
	}

	return lines
}

func splitIntoTerms(s string) []string {
	var terms []string
	var term []rune

	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			term = append(term, r)
		} else if len(term) > 0 {
			terms = append(terms, string(term))
			term = nil
		}
	}

	if len(term) > 0 {
		terms = append(terms, string(term))
	}

	return terms
}

func hasAnyTerm(text string, terms []string) bool {
	if len(terms) == 0 {
		return true
	}

	textLower := toLower(text)
	for _, term := range terms {
		if contains(textLower, term) {
			return true
		}
	}
	return false
}

func contains(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	if len(s) < len(substr) {
		return false
	}

	for i := 0; i <= len(s)-len(substr); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			if s[i+j] != substr[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// Close 关闭并释放所有资源
func (l *LlamaCpp) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// 停止所有定时器
	for _, timer := range l.timers {
		timer.Stop()
	}
	l.timers = make(map[ModelType]*time.Timer)

	// 释放所有模型
	if l.embeddingModel != nil {
		l.embeddingModel.Free()
		l.embeddingModel = nil
	}
	if l.rerankModel != nil {
		l.rerankModel.Free()
		l.rerankModel = nil
	}
	if l.generateModel != nil {
		l.generateModel.Free()
		l.generateModel = nil
	}

	l.lastUsed = make(map[ModelType]time.Time)

	return nil
}

// formatTextForEmbedding 格式化文本用于嵌入
func formatTextForEmbedding(text string, isQuery bool) string {
	if isQuery {
		return fmt.Sprintf("task: search result | query: %s", text)
	}
	return fmt.Sprintf("title: none | text: %s", text)
}

// sortRerankResults 按分数排序重排结果
func sortRerankResults(results []RerankResult) {
	// 使用简单的冒泡排序（实际应该用更高效的排序算法）
	n := len(results)
	for i := 0; i < n-1; i++ {
		for j := 0; j < n-i-1; j++ {
			if results[j].Score < results[j+1].Score {
				results[j], results[j+1] = results[j+1], results[j]
			}
		}
	}
}
