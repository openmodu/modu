package main

import (
	"fmt"
	"log"
	"time"

	"github.com/crosszan/modu/pkg/mmq"
	"github.com/crosszan/modu/pkg/mmq/memory"
)

func main() {
	fmt.Println("=== MMQ Memory (记忆管理) 演示 ===\n")

	// 创建MMQ实例
	m, err := mmq.NewWithDB("/tmp/mmq-memory-demo.db")
	if err != nil {
		log.Fatal(err)
	}
	defer m.Close()

	// 创建记忆管理器（通过子包直接使用）
	memMgr := memory.NewManager(m.GetStore(), m.GetEmbedding())
	convMem := memory.NewConversationMemory(memMgr)
	factMem := memory.NewFactMemory(memMgr)
	prefMem := memory.NewPreferenceMemory(memMgr)

	// 1. 对话记忆
	fmt.Println("1. 对话记忆演示...")
	fmt.Println("   存储对话轮次...")

	// 存储几轮对话
	conversations := []memory.ConversationTurn{
		{
			User:      "什么是Go语言？",
			Assistant: "Go是Google开发的静态类型编程语言，以简洁和高效并发著称。",
			SessionID: "session-001",
			Timestamp: time.Now().Add(-2 * time.Hour),
		},
		{
			User:      "Go适合什么场景？",
			Assistant: "Go特别适合构建微服务、API服务器、云原生应用和命令行工具。",
			SessionID: "session-001",
			Timestamp: time.Now().Add(-1 * time.Hour),
		},
		{
			User:      "如何学习Go？",
			Assistant: "可以从官方教程A Tour of Go开始，然后阅读Effective Go，最后通过实战项目提升。",
			SessionID: "session-001",
			Timestamp: time.Now(),
		},
	}

	for _, turn := range conversations {
		if err := convMem.StoreTurn(turn); err != nil {
			log.Printf("存储对话失败: %v", err)
		} else {
			fmt.Printf("   ✓ 存储: %s\n", turn.User)
		}
	}

	// 获取会话历史
	fmt.Println("\n   获取会话历史...")
	history, err := convMem.GetHistory("session-001", 10)
	if err != nil {
		log.Printf("获取历史失败: %v", err)
	} else {
		fmt.Printf("   会话 session-001 共 %d 轮对话:\n", len(history))
		for i, turn := range history {
			fmt.Printf("   [%d] 用户: %s\n", i+1, turn.User)
			fmt.Printf("       助手: %s\n", turn.Assistant)
		}
	}

	// 语义搜索历史
	fmt.Println("\n   语义搜索历史对话...")
	searchResults, err := convMem.SearchHistory("并发", 3)
	if err != nil {
		log.Printf("搜索失败: %v", err)
	} else {
		fmt.Printf("   搜索'并发'找到 %d 个相关对话:\n", len(searchResults))
		for i, turn := range searchResults {
			fmt.Printf("   [%d] %s\n", i+1, turn.User)
		}
	}

	// 2. 事实记忆
	fmt.Println("\n2. 事实记忆演示...")
	fmt.Println("   存储事实...")

	facts := []memory.Fact{
		{
			Subject:    "Go语言",
			Predicate:  "是",
			Object:     "静态类型编程语言",
			Confidence: 1.0,
			Source:     "官方文档",
			Timestamp:  time.Now(),
		},
		{
			Subject:    "Go语言",
			Predicate:  "由",
			Object:     "Google开发",
			Confidence: 1.0,
			Source:     "官方网站",
			Timestamp:  time.Now(),
		},
		{
			Subject:    "Go语言",
			Predicate:  "擅长",
			Object:     "并发编程",
			Confidence: 0.9,
			Source:     "技术博客",
			Timestamp:  time.Now(),
		},
	}

	for _, fact := range facts {
		if err := factMem.StoreFact(fact); err != nil {
			log.Printf("存储事实失败: %v", err)
		} else {
			fmt.Printf("   ✓ %s %s %s (置信度: %.1f)\n",
				fact.Subject, fact.Predicate, fact.Object, fact.Confidence)
		}
	}

	// 查询事实
	fmt.Println("\n   查询: Go语言 是...")
	results, err := factMem.QueryFact("Go语言", "是")
	if err != nil {
		log.Printf("查询失败: %v", err)
	} else {
		for _, fact := range results {
			fmt.Printf("   → %s (置信度: %.1f)\n", fact.Object, fact.Confidence)
		}
	}

	// 获取主体的所有事实
	fmt.Println("\n   关于'Go语言'的所有事实:")
	allFacts, err := factMem.GetFactsBySubject("Go语言")
	if err != nil {
		log.Printf("获取失败: %v", err)
	} else {
		for i, fact := range allFacts {
			fmt.Printf("   [%d] %s %s %s\n", i+1, fact.Subject, fact.Predicate, fact.Object)
		}
	}

	// 3. 偏好记忆
	fmt.Println("\n3. 偏好记忆演示...")
	fmt.Println("   记录用户偏好...")

	preferences := []memory.Preference{
		{
			Category:  "编程语言",
			Key:       "最喜欢",
			Value:     "Go",
			Source:    "user",
			Timestamp: time.Now(),
		},
		{
			Category:  "开发工具",
			Key:       "IDE",
			Value:     "VSCode",
			Source:    "user",
			Timestamp: time.Now(),
		},
		{
			Category:  "工作习惯",
			Key:       "代码风格",
			Value:     map[string]interface{}{"indent": "tab", "lineWidth": 100},
			Source:    "inferred",
			Timestamp: time.Now(),
		},
	}

	for _, pref := range preferences {
		if err := prefMem.RecordPreference(pref); err != nil {
			log.Printf("记录偏好失败: %v", err)
		} else {
			fmt.Printf("   ✓ %s / %s = %v\n", pref.Category, pref.Key, pref.Value)
		}
	}

	// 获取偏好
	fmt.Println("\n   获取偏好...")
	favLang, err := prefMem.GetPreference("编程语言", "最喜欢")
	if err == nil {
		fmt.Printf("   最喜欢的编程语言: %v\n", favLang)
	}

	// 获取分类下的所有偏好
	devPrefs, err := prefMem.GetPreferencesByCategory("开发工具")
	if err == nil {
		fmt.Println("   开发工具偏好:")
		for key, val := range devPrefs {
			fmt.Printf("   - %s: %v\n", key, val)
		}
	}

	// 获取所有偏好
	allPrefs, err := prefMem.GetAllPreferences()
	if err == nil {
		fmt.Println("\n   所有偏好:")
		for category, prefs := range allPrefs {
			fmt.Printf("   [%s]\n", category)
			for key, val := range prefs {
				fmt.Printf("      %s = %v\n", key, val)
			}
		}
	}

	// 导出偏好
	exported, err := prefMem.ExportPreferences()
	if err == nil {
		fmt.Println("\n   偏好JSON导出:")
		fmt.Println("   " + exported)
	}

	// 4. 统计信息
	fmt.Println("\n4. 记忆统计...")
	totalCount, _ := memMgr.Count()
	convCount, _ := memMgr.CountByType(memory.MemoryTypeConversation)
	factCount, _ := memMgr.CountByType(memory.MemoryTypeFact)
	prefCount, _ := memMgr.CountByType(memory.MemoryTypePreference)

	fmt.Printf("   总记忆数: %d\n", totalCount)
	fmt.Printf("   - 对话: %d\n", convCount)
	fmt.Printf("   - 事实: %d\n", factCount)
	fmt.Printf("   - 偏好: %d\n", prefCount)

	// 5. 时间衰减演示
	fmt.Println("\n5. 时间衰减演示...")

	// 存储不同时间的记忆
	now := time.Now()
	recentMem := memory.Memory{
		Type:       memory.MemoryTypeConversation,
		Content:    "最近的对话内容",
		Timestamp:  now,
		Importance: 0.5,
	}
	oldMem := memory.Memory{
		Type:       memory.MemoryTypeConversation,
		Content:    "很久以前的对话内容",
		Timestamp:  now.Add(-30 * 24 * time.Hour), // 30天前
		Importance: 0.5,
	}

	memMgr.Store(recentMem)
	memMgr.Store(oldMem)

	// 不应用衰减
	opts1 := memory.RecallOptions{
		Limit:      10,
		ApplyDecay: false,
	}
	memories1, _ := memMgr.Recall("对话", opts1)

	// 应用衰减（30天半衰期）
	opts2 := memory.RecallOptions{
		Limit:         10,
		ApplyDecay:    true,
		DecayHalflife: 30 * 24 * time.Hour,
	}
	memories2, _ := memMgr.Recall("对话", opts2)

	fmt.Printf("   不应用衰减: 找到 %d 个记忆\n", len(memories1))
	fmt.Printf("   应用衰减:   找到 %d 个记忆\n", len(memories2))
	if len(memories2) > 0 {
		fmt.Printf("   最相关: %s\n", memories2[0].Content[:min(20, len(memories2[0].Content))])
	}

	fmt.Println("\n✓ Memory演示完成")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
