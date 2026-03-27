package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/openmodu/modu/pkg/mailbox"
	"github.com/openmodu/modu/pkg/mailbox/sqlitestore"
)

func main() {
	port   := flag.Int("port", 8080, "HTTP port to listen on")
	dbPath := flag.String("db", "./data/mailbox.db", "SQLite database path")
	wechat := flag.Bool("wechat", false, "Start the WeChat content team (requires LM Studio)")
	flag.Parse()

	if err := os.MkdirAll("./data", 0o755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	store, err := sqlitestore.New(*dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer store.Close()

	hub := mailbox.NewHub(mailbox.WithStore(store))
	seedDemoTeam(hub)

	cfg := defaultContentConfig()
	addr := fmt.Sprintf(":%d", *port)
	srv := NewAgentTeamsServer(hub, cfg)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if *wechat {
		startWechatTeam(ctx, hub, cfg)
		log.Printf("[wechat] content team active (model=%s api=%s)", cfg.ModelID, cfg.APIURL)
	}

	log.Printf("Agent Teams on http://localhost%s  (wechat=%v)", addr, *wechat)
	if err := srv.Start(ctx, addr); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func seedDemoTeam(hub *mailbox.Hub) {
	demo := map[string]string{
		"planner":  "Planner",
		"writer":   "Copywriter",
		"designer": "Visual Designer",
		"reviewer": "Reviewer",
	}
	for id, role := range demo {
		hub.Register(id)
		_ = hub.SetAgentRole(id, role)
	}
}
