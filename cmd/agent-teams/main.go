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
	port := flag.Int("port", 8080, "HTTP port to listen on")
	workspace := flag.String("workspace", "./workspace", "Root directory for all runtime data (db, agent files, articles)")
	flag.Parse()

	if err := os.MkdirAll(*workspace, 0o755); err != nil {
		log.Fatalf("create workspace dir: %v", err)
	}
	dbPath := *workspace + "/mailbox.db"

	store, err := sqlitestore.New(dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer store.Close()

	hub := mailbox.NewHub(mailbox.WithStore(store))

	cfg := defaultContentConfig()
	cfg.WorkDir = *workspace
	addr := fmt.Sprintf(":%d", *port)
	srv := NewAgentTeamsServer(hub, cfg)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	startWechatTeam(ctx, hub, cfg)
	log.Printf("[wechat] content team active (model=%s api=%s)", cfg.ModelID, cfg.APIURL)

	log.Printf("Wechat Article Team on http://localhost%s", addr)
	if err := srv.Start(ctx, addr); err != nil {
		log.Fatalf("server: %v", err)
	}
}
