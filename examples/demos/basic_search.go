package main

import (
	"fmt"
	"log"
	"time"

	"github.com/crosszan/modu/pkg/mmq"
)

func main() {
	// 创建MMQ实例
	m, err := mmq.NewWithDB("/tmp/mmq-demo.db")
	if err != nil {
		log.Fatal(err)
	}
	defer m.Close()

	// 索引示例文档
	docs := []mmq.Document{
		{
			Collection: "tech",
			Path:       "golang/intro.md",
			Title:      "Go语言简介",
			Content: `Go是由Google开发的一门静态类型、编译型编程语言。
它具有简洁的语法、高效的并发支持和快速的编译速度。
Go特别适合构建网络服务、分布式系统和云原生应用。
主要特性包括：goroutine、channel、接口、defer等。`,
			CreatedAt:  time.Now(),
			ModifiedAt: time.Now(),
		},
		{
			Collection: "tech",
			Path:       "python/intro.md",
			Title:      "Python语言简介",
			Content: `Python是一门高级的、解释型编程语言。
它以简洁优雅的语法著称，拥有丰富的第三方库生态。
Python广泛应用于数据科学、机器学习、Web开发等领域。
主要特性包括：动态类型、列表推导式、装饰器、生成器等。`,
			CreatedAt:  time.Now(),
			ModifiedAt: time.Now(),
		},
		{
			Collection: "ai",
			Path:       "rag/concepts.md",
			Title:      "RAG系统介绍",
			Content: `RAG（Retrieval-Augmented Generation）是一种结合检索和生成的AI技术。
它通过在生成回答前先检索相关文档，提高了LLM的准确性和时效性。
RAG系统通常包含三个核心组件：文档索引、检索器、生成器。
常用的检索方法有：BM25全文搜索、向量语义搜索、混合搜索。`,
			CreatedAt:  time.Now(),
			ModifiedAt: time.Now(),
		},
		{
			Collection: "ai",
			Path:       "llm/models.md",
			Title:      "大语言模型概述",
			Content: `大语言模型（LLM）是基于Transformer架构的深度学习模型。
代表性的模型包括：GPT系列、Claude、LLaMA、Qwen等。
LLM具有强大的理解和生成能力，可以完成问答、翻译、编程等任务。
本地部署可以使用GGUF格式的量化模型，配合llama.cpp进行推理。`,
			CreatedAt:  time.Now(),
			ModifiedAt: time.Now(),
		},
	}

	fmt.Println("正在索引文档...")
	for _, doc := range docs {
		if err := m.IndexDocument(doc); err != nil {
			log.Printf("索引文档失败 %s: %v", doc.Path, err)
		} else {
			fmt.Printf("✓ 已索引: %s\n", doc.Path)
		}
	}

	// 查看状态
	status, err := m.Status()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("\n=== 索引状态 ===\n")
	fmt.Printf("总文档数: %d\n", status.TotalDocuments)
	fmt.Printf("集合: %v\n", status.Collections)
	fmt.Printf("数据库: %s\n", status.DBPath)

	// 搜索示例
	queries := []string{
		"Go",
		"Python",
		"RAG",
		"llama",
	}

	for _, query := range queries {
		fmt.Printf("\n=== 搜索: %s ===\n", query)

		results, err := m.Search(query, mmq.SearchOptions{
			Limit: 3,
		})
		if err != nil {
			log.Printf("搜索失败: %v", err)
			continue
		}

		if len(results) == 0 {
			fmt.Println("未找到相关结果")
			continue
		}

		for i, result := range results {
			fmt.Printf("\n%d. [%.2f] %s\n", i+1, result.Score, result.Title)
			fmt.Printf("   路径: %s/%s\n", result.Collection, result.Path)
			fmt.Printf("   摘要: %s\n", truncate(result.Snippet, 100))
		}
	}

	// 集合过滤搜索
	fmt.Printf("\n=== 在tech集合中搜索: programming ===\n")
	results, err := m.Search("programming", mmq.SearchOptions{
		Limit:      5,
		Collection: "tech",
	})
	if err != nil {
		log.Fatal(err)
	}

	for i, result := range results {
		fmt.Printf("%d. [%.2f] %s (%s)\n",
			i+1, result.Score, result.Title, result.Path)
	}

	// 获取文档
	fmt.Printf("\n=== 获取文档详情 ===\n")
	doc, err := m.GetDocument("golang/intro.md")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("标题: %s\n", doc.Title)
	fmt.Printf("集合: %s\n", doc.Collection)
	fmt.Printf("路径: %s\n", doc.Path)
	fmt.Printf("创建时间: %s\n", doc.CreatedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("内容长度: %d字符\n", len(doc.Content))
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
