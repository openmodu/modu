package main

import (
	"log"

	"github.com/crosszan/modu/pkg/mailbox/server"
)

func main() {
	srv := server.NewMailboxServer()
	addr := ":6380" // 避免和默认的 redis 6379 冲突

	if err := srv.ListenAndServe(addr); err != nil {
		log.Fatal(err)
	}
}
