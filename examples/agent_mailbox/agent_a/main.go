package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"time"

	sdk "github.com/crosszan/modu/pkg/mailbox/client"
)

func main() {
	ctx := context.Background()
	addr := "localhost:6380"

	agentA := sdk.NewMailboxClient("agent-a", addr)

	if err := agentA.Register(ctx); err != nil {
		log.Fatalf("Agent A register failed: %v", err)
	}
	fmt.Println("Agent A registered successfully.")

	// 1. 轮询接收回复 (从 Agent B 等)
	go func() {
		for {
			msg, err := agentA.Recv(ctx)
			if err != nil {
				log.Printf("Agent A recv error: %v", err)
			} else if msg != "" {
				fmt.Printf("[Agent A] Received: %s\n", msg)
			}
			time.Sleep(500 * time.Millisecond)
		}
	}()

	// 2. 定期随机发送三种不同类型的消息
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	msgCount := 1

	for range ticker.C {
		scenario := rand.Intn(3) // 取值 0, 1, 2
		switch scenario {
		case 0:
			// 发给 agent-b
			msg := fmt.Sprintf("Direct to B, id: %d", msgCount)
			fmt.Printf("\n[Agent A] Sending to agent-b: %s\n", msg)
			err := agentA.Send(ctx, "agent-b", msg)
			if err != nil {
				fmt.Printf("[Agent A] Send to agent-b failed: %v\n", err)
			}
		case 1:
			// 发给 agent-c (不在线)
			msg := fmt.Sprintf("Direct to C, id: %d", msgCount)
			fmt.Printf("\n[Agent A] Sending to agent-c: %s\n", msg)
			err := agentA.Send(ctx, "agent-c", msg)
			if err != nil {
				// 预期会失败，因为 agent-c 没有注册
				fmt.Printf("[Agent A] Notice: agent-c is offline. (Error: %v)\n", err)
			} else {
				fmt.Printf("[Agent A] Successfully sent to agent-c (Unexpected!)\n")
			}
		case 2:
			// 全局广播
			msg := fmt.Sprintf("Broadcast Message, id: %d", msgCount)
			fmt.Printf("\n[Agent A] Broadcasting: %s\n", msg)
			err := agentA.Broadcast(ctx, msg)
			if err != nil {
				fmt.Printf("[Agent A] Broadcast failed: %v\n", err)
			}
		}
		msgCount++
	}
}
