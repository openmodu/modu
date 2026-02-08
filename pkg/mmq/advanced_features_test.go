package mmq

import (
	"os"
	"testing"

	"github.com/crosszan/modu/pkg/mmq/llm"
	"github.com/crosszan/modu/pkg/mmq/rag"
	"github.com/crosszan/modu/pkg/mmq/store"
)

// TestQueryExpansion 测试查询扩展功能
func TestQueryExpansion(t *testing.T) {
	// 创建 Mock LLM
	mockLLM := llm.NewMockLLM(300)

	// 测试 ExpandQuery
	expansions, err := mockLLM.ExpandQuery("machine learning")
	if err != nil {
		t.Fatalf("ExpandQuery failed: %v", err)
	}

	if len(expansions) == 0 {
		t.Fatal("Expected at least one expansion")
	}

	t.Logf("Query expansions for 'machine learning':")
	for i, exp := range expansions {
		t.Logf("  [%d] Type: %s, Text: %s, Weight: %.2f", i+1, exp.Type, exp.Text, exp.Weight)
	}

	// 验证扩展类型
	types := make(map[string]bool)
	for _, exp := range expansions {
		types[exp.Type] = true
	}

	if !types["lex"] && !types["vec"] && !types["hyde"] {
		t.Error("Expected at least one of lex, vec, or hyde expansion types")
	}
}

// TestReranking 测试重排序功能
func TestReranking(t *testing.T) {
	mockLLM := llm.NewMockLLM(300)

	query := "deep learning"
	docs := []llm.Document{
		{ID: "1", Content: "Deep learning is a subset of machine learning", Title: "DL Intro"},
		{ID: "2", Content: "Python is a programming language", Title: "Python"},
		{ID: "3", Content: "Neural networks are used in deep learning", Title: "Neural Nets"},
	}

	results, err := mockLLM.Rerank(query, docs)
	if err != nil {
		t.Fatalf("Rerank failed: %v", err)
	}

	if len(results) != len(docs) {
		t.Errorf("Expected %d results, got %d", len(docs), len(results))
	}

	t.Logf("Rerank results for query '%s':", query)
	for i, res := range results {
		t.Logf("  [%d] ID: %s, Score: %.4f", i+1, res.ID, res.Score)
	}

	// 验证结果按分数降序排列
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Error("Results should be sorted by score in descending order")
		}
	}
}

// TestGeneration 测试文本生成功能
func TestGeneration(t *testing.T) {
	mockLLM := llm.NewMockLLM(300)

	prompt := "Explain what RAG is"
	opts := llm.DefaultGenerateOptions()

	result, err := mockLLM.Generate(prompt, opts)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if result == "" {
		t.Fatal("Expected non-empty generation result")
	}

	t.Logf("Generation result: %s", result)
}

// TestIntegratedRetrieval 测试集成的检索功能（查询扩展 + 重排序）
func TestIntegratedRetrieval(t *testing.T) {
	// 创建临时数据库
	dbPath := "/tmp/test_advanced_features.db"
	defer os.Remove(dbPath)

	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer st.Close()

	// 创建 Mock LLM 和嵌入生成器
	mockLLM := llm.NewMockLLM(300)
	embGen := llm.NewEmbeddingGenerator(mockLLM, "mock-embed", 300)

	// 添加测试文档
	err = st.IndexDocument(store.Document{
		Collection: "test",
		Path:       "doc1.md",
		Title:      "Machine Learning",
		Content:    "Machine learning is a subset of AI that enables systems to learn from data",
	})
	if err != nil {
		t.Fatalf("Failed to add document: %v", err)
	}

	err = st.IndexDocument(store.Document{
		Collection: "test",
		Path:       "doc2.md",
		Title:      "Deep Learning",
		Content:    "Deep learning uses neural networks with multiple layers to learn hierarchical representations",
	})
	if err != nil {
		t.Fatalf("Failed to add document: %v", err)
	}

	err = st.IndexDocument(store.Document{
		Collection: "test",
		Path:       "doc3.md",
		Title:      "Python Programming",
		Content:    "Python is a high-level programming language widely used in data science",
	})
	if err != nil {
		t.Fatalf("Failed to add document: %v", err)
	}

	// 生成嵌入
	docs, err := st.GetDocumentsNeedingEmbedding()
	if err != nil {
		t.Fatalf("Failed to get documents: %v", err)
	}

	for _, doc := range docs {
		chunks := llm.ChunkTextForEmbedding(doc.Content, 500, 50)
		for i, chunk := range chunks {
			emb, err := embGen.Generate(chunk, false)
			if err != nil {
				t.Fatalf("Failed to generate embedding: %v", err)
			}
			err = st.StoreEmbedding(doc.Hash, i, i*500, emb, "mock-embed")
			if err != nil {
				t.Fatalf("Failed to store embedding: %v", err)
			}
		}
	}

	// 创建检索器
	retriever := rag.NewRetriever(st, mockLLM, embGen)

	t.Run("WithoutExpansion", func(t *testing.T) {
		opts := rag.DefaultRetrieveOptions()
		opts.ExpandQuery = false
		opts.Rerank = false
		opts.Limit = 3

		results, err := retriever.Retrieve("machine learning algorithms", opts)
		if err != nil {
			t.Fatalf("Retrieve failed: %v", err)
		}

		t.Logf("Standard retrieval returned %d results", len(results))
		for i, ctx := range results {
			t.Logf("  [%d] Source: %s, Relevance: %.4f", i+1, ctx.Source, ctx.Relevance)
		}
	})

	t.Run("WithExpansion", func(t *testing.T) {
		opts := rag.DefaultRetrieveOptions()
		opts.ExpandQuery = true
		opts.Rerank = false
		opts.Limit = 3

		results, err := retriever.Retrieve("machine learning algorithms", opts)
		if err != nil {
			t.Fatalf("Retrieve with expansion failed: %v", err)
		}

		t.Logf("Retrieval with query expansion returned %d results", len(results))
		for i, ctx := range results {
			t.Logf("  [%d] Source: %s, Relevance: %.4f", i+1, ctx.Source, ctx.Relevance)
		}
	})

	t.Run("WithExpansionAndRerank", func(t *testing.T) {
		opts := rag.DefaultRetrieveOptions()
		opts.ExpandQuery = true
		opts.Rerank = true
		opts.Limit = 3

		results, err := retriever.Retrieve("deep learning", opts)
		if err != nil {
			t.Fatalf("Retrieve with expansion and rerank failed: %v", err)
		}

		t.Logf("Retrieval with expansion + rerank returned %d results", len(results))
		for i, ctx := range results {
			t.Logf("  [%d] Source: %s, Relevance: %.4f", i+1, ctx.Source, ctx.Relevance)
		}
	})
}

// TestCaching 测试 LLM 缓存功能
func TestCaching(t *testing.T) {
	dbPath := "/tmp/test_cache.db"
	defer os.Remove(dbPath)

	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer st.Close()

	// 测试缓存操作
	key := store.CacheKey("expandQuery", map[string]string{"query": "test"})

	// 首次查询应该为空
	cached, err := st.GetCachedResult(key)
	if err != nil {
		t.Fatalf("GetCachedResult failed: %v", err)
	}
	if cached != "" {
		t.Error("Expected empty cache on first query")
	}

	// 设置缓存
	testResult := `[{"type":"lex","text":"test query","weight":1.0}]`
	err = st.SetCachedResult(key, testResult)
	if err != nil {
		t.Fatalf("SetCachedResult failed: %v", err)
	}

	// 再次查询应该命中缓存
	cached, err = st.GetCachedResult(key)
	if err != nil {
		t.Fatalf("GetCachedResult failed: %v", err)
	}
	if cached != testResult {
		t.Errorf("Expected cached result %q, got %q", testResult, cached)
	}

	// 测试缓存统计
	count, err := st.GetCacheStats()
	if err != nil {
		t.Fatalf("GetCacheStats failed: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected cache count 1, got %d", count)
	}

	t.Logf("Cache stats: %d entries", count)

	// 清空缓存
	err = st.ClearCache()
	if err != nil {
		t.Fatalf("ClearCache failed: %v", err)
	}

	count, err = st.GetCacheStats()
	if err != nil {
		t.Fatalf("GetCacheStats failed: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected cache count 0 after clear, got %d", count)
	}
}
