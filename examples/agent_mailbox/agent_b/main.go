package main

import (
	"context"
	"fmt"
	"log"
	"time"

	sdk "github.com/openmodu/modu/pkg/mailbox/client"
)

func main() {
	ctx := context.Background()
	addr := "localhost:6380"

	agentB := sdk.NewMailboxClient("agent-b", addr)

	if err := agentB.Register(ctx); err != nil {
		log.Fatalf("Agent B register failed: %v", err)
	}
	fmt.Println("Agent B registered successfully.")

	// Listen for messages and reply
	for {
		msg, err := agentB.Recv(ctx)
		if err != nil {
			log.Printf("Agent B recv error: %v", err)
		} else if msg != "" {
			fmt.Printf("[Agent B] Received: %s\n", msg)
			
			// Reply back to agent-a
			replyStr := fmt.Sprintf("收到: %s", msg)
			fmt.Printf("[Agent B] Replying to agent-a: %s\n", replyStr)
			if err := agentB.Send(ctx, "agent-a", replyStr); err != nil {
				log.Printf("[Agent B] Failed to reply to agent-a: %v", err)
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
}
