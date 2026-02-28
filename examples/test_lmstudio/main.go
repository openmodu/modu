package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// 定义请求体结构
type RequestPayload struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
	Stream      bool      `json:"stream"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// 定义流式返回的数据结构 (简化版)
type ChatCompletionChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}

func main() {
	url := "http://localhost:1234/v1/chat/completions"

	// 构造请求数据，这里强制 Stream: true
	payload := RequestPayload{
		Model: "local-model", // LM Studio 默认会忽略这个字段并使用当前加载的模型
		Messages: []Message{
			{Role: "system", Content: "你是一个有用的AI助手。请简明扼要地回答。"},
			{Role: "user", Content: "请用大约200字解释一下什么是量子计算？"}, // 给它找点活干
		},
		Temperature: 0.7,
		Stream:      true, // 核心所在：开启流式输出！
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		fmt.Println("JSON 编码失败:", err)
		return
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		fmt.Println("创建请求失败:", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	fmt.Println("正在发送请求到 LM Studio...")
	startTime := time.Now()

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("请求发送失败，请检查 LM Studio Server 是否已启动:", err)
		return
	}
	defer resp.Body.Close()

	// 使用 bufio 读取流式响应 (Server-Sent Events)
	scanner := bufio.NewScanner(resp.Body)
	firstTokenReceived := false
	var firstTokenTime time.Duration

	fmt.Println("--------------------------------------------------")
	
	for scanner.Scan() {
		line := scanner.Text()

		// 流式数据以 "data: " 开头
		if strings.HasPrefix(line, "data: ") {
			dataStr := strings.TrimPrefix(line, "data: ")

			// [DONE] 表示流结束
			if dataStr == "[DONE]" {
				break
			}

			var chunk ChatCompletionChunk
			if err := json.Unmarshal([]byte(dataStr), &chunk); err != nil {
				continue
			}

			// 提取并打印内容
			if len(chunk.Choices) > 0 {
				content := chunk.Choices[0].Delta.Content
				if content != "" {
					// 记录首字延迟
					if !firstTokenReceived {
						firstTokenTime = time.Since(startTime)
						firstTokenReceived = true
					}
					// 实时打印，不换行，实现打字机效果
					fmt.Print(content)
				}
			}
		}
	}
	fmt.Println("\n--------------------------------------------------")

	if err := scanner.Err(); err != nil {
		fmt.Println("读取流报错:", err)
	}

	totalTime := time.Since(startTime)
	fmt.Printf("\n[性能统计]\n")
	fmt.Printf("首字延迟 (TTFT): %v\n", firstTokenTime)
	fmt.Printf("总计耗时: %v\n", totalTime)
}