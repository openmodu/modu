package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/crosszan/modu/pkg/mmq"
	"github.com/crosszan/modu/pkg/mmq/rag"
)

func main() {
	fmt.Println("=== MMQ RAG (检索增强生成) 演示 ===\n")

	// 创建MMQ实例
	m, err := mmq.NewWithDB("/tmp/mmq-rag-demo.db")
	if err != nil {
		log.Fatal(err)
	}
	defer m.Close()

	// 1. 索引技术文档
	fmt.Println("1. 索引技术文档...")
	docs := []mmq.Document{
		{
			Collection: "tech",
			Path:       "languages/go.md",
			Title:      "Go编程语言",
			Content: `Go是Google开发的开源编程语言。它具有以下特性：

**核心特性**：
- 静态类型和编译型语言
- 内置并发支持（goroutines和channels）
- 垃圾回收
- 快速编译
- 简洁的语法

**适用场景**：
- 微服务和API开发
- 云原生应用
- 分布式系统
- 命令行工具

**并发模型**：
Go使用goroutine实现轻量级并发，每个goroutine只占用几KB内存。
通过channel进行goroutine之间的通信，遵循"不要通过共享内存来通信，
而要通过通信来共享内存"的哲学。`,
			CreatedAt:  time.Now(),
			ModifiedAt: time.Now(),
		},
		{
			Collection: "tech",
			Path:       "languages/python.md",
			Title:      "Python编程语言",
			Content: `Python是一门广泛使用的解释型高级编程语言。

**核心特性**：
- 动态类型
- 简洁优雅的语法
- 丰富的标准库和第三方库
- 跨平台兼容性
- 强大的社区支持

**适用场景**：
- 数据科学和机器学习
- Web开发（Django, Flask）
- 自动化脚本
- 科学计算

**数据科学生态**：
Python在数据科学领域占据主导地位，拥有NumPy、Pandas、
Scikit-learn、TensorFlow、PyTorch等强大工具。`,
			CreatedAt:  time.Now(),
			ModifiedAt: time.Now(),
		},
		{
			Collection: "ai",
			Path:       "concepts/rag.md",
			Title:      "检索增强生成(RAG)",
			Content: `RAG（Retrieval-Augmented Generation）是一种结合检索和生成的AI技术。

**核心概念**：
RAG通过在生成回答前先检索相关文档，解决了LLM的几个关键问题：
1. 知识时效性：可以获取最新信息
2. 准确性：基于事实的文档而不是记忆
3. 可验证性：可以追溯信息来源

**系统架构**：
1. 文档索引：将知识库文档向量化并索引
2. 检索器：根据查询找到最相关的文档
3. 生成器：基于检索的文档生成答案

**检索方法**：
- BM25：传统的关键词匹配
- 向量搜索：基于语义相似度
- 混合搜索：结合两者优势`,
			CreatedAt:  time.Now(),
			ModifiedAt: time.Now(),
		},
		{
			Collection: "ai",
			Path:       "concepts/embeddings.md",
			Title:      "向量嵌入",
			Content: `向量嵌入是将文本转换为数值向量的技术。

**工作原理**：
文本通过嵌入模型转换为固定维度的向量（如300维或768维），
语义相似的文本会产生相似的向量。

**应用场景**：
- 语义搜索
- 文本分类
- 相似度计算
- 推荐系统

**常用模型**：
- BERT系列
- Sentence Transformers
- OpenAI Embeddings
- Google Gemma Embeddings`,
			CreatedAt:  time.Now(),
			ModifiedAt: time.Now(),
		},
	}

	for _, doc := range docs {
		if err := m.IndexDocument(doc); err != nil {
			log.Printf("索引失败 %s: %v", doc.Path, err)
		} else {
			fmt.Printf("   ✓ %s\n", doc.Title)
		}
	}

	// 2. 生成嵌入
	fmt.Println("\n2. 生成向量嵌入...")
	if err := m.GenerateEmbeddings(); err != nil {
		log.Fatal(err)
	}

	// 3. 演示三种检索策略
	queries := []struct {
		text     string
		strategy mmq.RetrievalStrategy
		desc     string
	}{
		{"Go并发编程", mmq.StrategyFTS, "BM25全文搜索"},
		{"机器学习工具", mmq.StrategyVector, "向量语义搜索"},
		{"如何实现语义搜索", mmq.StrategyHybrid, "混合搜索（推荐）"},
	}

	for _, q := range queries {
		fmt.Printf("\n3. 检索策略: %s\n", q.desc)
		fmt.Printf("   查询: \"%s\"\n", q.text)

		contexts, err := m.RetrieveContext(q.text, mmq.RetrieveOptions{
			Limit:    3,
			Strategy: q.strategy,
		})
		if err != nil {
			log.Printf("检索失败: %v", err)
			continue
		}

		fmt.Printf("   结果: %d个上下文\n", len(contexts))
		for i, ctx := range contexts {
			fmt.Printf("      [%d] %.1f%% - %s\n",
				i+1, ctx.Relevance*100, ctx.Source)
		}
	}

	// 4. 演示上下文构建
	fmt.Println("\n4. 上下文构建示例...")
	query := "RAG系统如何工作"

	contexts, err := m.RetrieveContext(query, mmq.RetrieveOptions{
		Limit:    2,
		Strategy: mmq.StrategyHybrid,
	})
	if err != nil {
		log.Fatal(err)
	}

	// Markdown格式
	builder := rag.NewContextBuilder(rag.ContextBuilderOptions{
		MaxTokens:     500,
		IncludeSource: true,
		IncludeScore:  true,
		Format:        rag.FormatMarkdown,
	})

	contextText := builder.Build(convertToRagContexts(contexts))
	fmt.Println("   Markdown格式:")
	fmt.Println("   " + strings.Repeat("─", 50))
	for _, line := range splitLines(contextText) {
		fmt.Println("   " + line)
	}
	fmt.Println("   " + strings.Repeat("─", 50))

	// 5. 混合搜索
	fmt.Println("\n5. 混合搜索演示...")
	results, err := m.HybridSearch("编程语言特性", mmq.SearchOptions{
		Limit:      3,
		Collection: "tech",
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("   找到 %d 个结果:\n", len(results))
	for i, res := range results {
		fmt.Printf("      [%d] %.2f - %s\n", i+1, res.Score, res.Title)
		fmt.Printf("          %s/%s\n", res.Collection, res.Path)
	}

	// 6. 集合过滤
	fmt.Println("\n6. 集合过滤示例...")
	techContexts, err := m.RetrieveContext("programming", mmq.RetrieveOptions{
		Limit:      5,
		Strategy:   mmq.StrategyHybrid,
		Collection: "tech",
	})
	if err != nil {
		log.Fatal(err)
	}

	aiContexts, err := m.RetrieveContext("programming", mmq.RetrieveOptions{
		Limit:      5,
		Strategy:   mmq.StrategyHybrid,
		Collection: "ai",
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("   tech集合: %d个结果\n", len(techContexts))
	fmt.Printf("   ai集合:   %d个结果\n", len(aiContexts))

	// 7. 完整RAG流程示例
	fmt.Println("\n7. 完整RAG流程示例...")
	userQuestion := "Go语言适合什么场景？"

	fmt.Printf("   用户问题: %s\n", userQuestion)

	// 检索相关上下文
	ragContexts, err := m.RetrieveContext(userQuestion, mmq.RetrieveOptions{
		Limit:    3,
		Strategy: mmq.StrategyHybrid,
	})
	if err != nil {
		log.Fatal(err)
	}

	// 构建提示
	systemPrompt := "你是一个专业的技术助手。基于检索到的上下文回答用户问题。"
	fullPrompt := builder.BuildPrompt(userQuestion, convertToRagContexts(ragContexts), systemPrompt)

	fmt.Println("\n   构建的完整提示:")
	fmt.Println("   " + strings.Repeat("=", 50))
	for _, line := range splitLines(fullPrompt) {
		if len(line) > 70 {
			line = line[:70] + "..."
		}
		fmt.Println("   " + line)
	}
	fmt.Println("   " + strings.Repeat("=", 50))

	fmt.Println("\n   (实际应用中，将此提示发送给LLM生成答案)")

	// 8. 查看最终状态
	fmt.Println("\n8. 最终状态...")
	status, err := m.Status()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("   总文档数: %d\n", status.TotalDocuments)
	fmt.Printf("   待嵌入数: %d\n", status.NeedsEmbedding)
	fmt.Printf("   集合列表: %v\n", status.Collections)

	fmt.Println("\n✓ RAG演示完成")
	fmt.Println("\n提示: 在实际应用中，将检索到的上下文和用户问题")
	fmt.Println("      一起发送给LLM，即可实现检索增强生成！")
}

// splitLines 分割文本为行
func splitLines(text string) []string {
	var lines []string
	var line []rune

	for _, r := range text {
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

// convertToRagContexts 转换mmq.Context到rag.Context
func convertToRagContexts(mmqContexts []mmq.Context) []rag.Context {
	ragContexts := make([]rag.Context, len(mmqContexts))
	for i, mc := range mmqContexts {
		ragContexts[i] = rag.Context{
			Text:      mc.Text,
			Source:    mc.Source,
			Relevance: mc.Relevance,
			Metadata:  mc.Metadata,
		}
	}
	return ragContexts
}
