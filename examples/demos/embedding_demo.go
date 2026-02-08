package main

import (
	"fmt"
	"log"
	"time"

	"github.com/crosszan/modu/pkg/mmq"
)

func main() {
	// 创建MMQ实例
	m, err := mmq.NewWithDB("/tmp/mmq-embedding-demo.db")
	if err != nil {
		log.Fatal(err)
	}
	defer m.Close()

	fmt.Println("=== MMQ 嵌入功能演示 ===\n")

	// 1. 索引文档
	fmt.Println("1. 索引文档...")
	docs := []mmq.Document{
		{
			Collection: "tech",
			Path:       "golang.md",
			Title:      "Go编程语言",
			Content: `Go是由Google开发的一门静态类型、编译型编程语言。
它具有简洁的语法、高效的并发支持和快速的编译速度。
Go特别适合构建网络服务、分布式系统和云原生应用。
主要特性包括：goroutine并发、channel通信、接口、defer语句等。`,
			CreatedAt:  time.Now(),
			ModifiedAt: time.Now(),
		},
		{
			Collection: "tech",
			Path:       "python.md",
			Title:      "Python编程语言",
			Content: `Python是一门高级的、解释型编程语言。
它以简洁优雅的语法著称，拥有丰富的第三方库生态。
Python广泛应用于数据科学、机器学习、Web开发等领域。
主要特性包括：动态类型、列表推导式、装饰器、生成器等。`,
			CreatedAt:  time.Now(),
			ModifiedAt: time.Now(),
		},
		{
			Collection: "ai",
			Path:       "rag.md",
			Title:      "RAG系统",
			Content: `RAG（Retrieval-Augmented Generation）是一种结合检索和生成的AI技术。
它通过在生成回答前先检索相关文档，提高了LLM的准确性和时效性。
RAG系统通常包含三个核心组件：文档索引、检索器、生成器。
常用的检索方法有：BM25全文搜索、向量语义搜索、混合搜索。`,
			CreatedAt:  time.Now(),
			ModifiedAt: time.Now(),
		},
	}

	for _, doc := range docs {
		if err := m.IndexDocument(doc); err != nil {
			log.Printf("索引失败 %s: %v", doc.Path, err)
		} else {
			fmt.Printf("   ✓ %s\n", doc.Path)
		}
	}

	// 2. 生成嵌入
	fmt.Println("\n2. 生成文档嵌入...")
	if err := m.GenerateEmbeddings(); err != nil {
		log.Fatal(err)
	}

	// 3. 查看状态
	fmt.Println("\n3. 查看索引状态...")
	status, err := m.Status()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("   总文档数: %d\n", status.TotalDocuments)
	fmt.Printf("   待嵌入数: %d\n", status.NeedsEmbedding)
	fmt.Printf("   集合列表: %v\n", status.Collections)

	// 4. 生成查询嵌入
	fmt.Println("\n4. 生成查询嵌入...")
	queries := []string{
		"并发编程",
		"机器学习",
		"检索增强",
	}

	for _, query := range queries {
		embedding, err := m.EmbedText(query)
		if err != nil {
			log.Printf("嵌入失败: %v", err)
			continue
		}

		fmt.Printf("   %s:\n", query)
		fmt.Printf("      维度: %d\n", len(embedding))
		fmt.Printf("      前5个值: [%.4f %.4f %.4f %.4f %.4f]\n",
			embedding[0], embedding[1], embedding[2], embedding[3], embedding[4])

		// 计算向量模长（应该接近1）
		var norm float32
		for _, v := range embedding {
			norm += v * v
		}
		norm = sqrtFloat32(norm)
		fmt.Printf("      模长: %.6f\n", norm)
	}

	// 5. 演示嵌入一致性
	fmt.Println("\n5. 验证嵌入一致性...")
	testText := "相同的文本"

	emb1, _ := m.EmbedText(testText)
	emb2, _ := m.EmbedText(testText)

	var maxDiff float32
	for i := range emb1 {
		diff := emb1[i] - emb2[i]
		if diff < 0 {
			diff = -diff
		}
		if diff > maxDiff {
			maxDiff = diff
		}
	}

	fmt.Printf("   文本: \"%s\"\n", testText)
	fmt.Printf("   两次生成的最大差异: %.10f\n", maxDiff)
	if maxDiff < 0.0001 {
		fmt.Println("   ✓ 嵌入生成是确定性的")
	}

	// 6. 演示语义相似度（简化版）
	fmt.Println("\n6. 计算语义相似度...")
	text1 := "Go语言并发编程"
	text2 := "Python异步编程"
	text3 := "机器学习算法"

	emb_t1, _ := m.EmbedText(text1)
	emb_t2, _ := m.EmbedText(text2)
	emb_t3, _ := m.EmbedText(text3)

	sim12 := cosineSimilarity(emb_t1, emb_t2)
	sim13 := cosineSimilarity(emb_t1, emb_t3)
	sim23 := cosineSimilarity(emb_t2, emb_t3)

	fmt.Printf("   \"%s\" <-> \"%s\": %.4f\n", text1, text2, sim12)
	fmt.Printf("   \"%s\" <-> \"%s\": %.4f\n", text1, text3, sim13)
	fmt.Printf("   \"%s\" <-> \"%s\": %.4f\n", text2, text3, sim23)

	fmt.Println("\n✓ 演示完成")
}

// sqrtFloat32 计算平方根
func sqrtFloat32(x float32) float32 {
	if x == 0 {
		return 0
	}
	z := x
	for i := 0; i < 10; i++ {
		z = (z + x/z) / 2
	}
	return z
}

// cosineSimilarity 计算余弦相似度
func cosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}

	var dotProduct, normA, normB float32
	for i := range a {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (sqrtFloat32(normA) * sqrtFloat32(normB))
}
