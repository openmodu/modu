// Command acp-gateway exposes ACP agents (Claude Code / Codex / Gemini) over
// a small HTTP API so remote clients (iOS, curl, any other HTTP caller) can
// dispatch tasks to the machine that owns the local file tree.
//
// Routes (all but /healthz require Bearer auth):
//
//	GET  /healthz
//	GET  /api/agents
//	POST /api/tasks                  {agent, prompt, cwd}  → {id, status, ...}
//	GET  /api/tasks/{id}
//	GET  /api/tasks/{id}/stream      Server-Sent Events
//	POST /api/tasks/{id}/approve     {toolCallId, optionId}
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/openmodu/modu/pkg/acp/manager"
)

func main() {
	var (
		addr     = flag.String("addr", ":7080", "HTTP listen address")
		cfgPath  = flag.String("config", "", "path to acp.config.json (empty = default lookup)")
		workers  = flag.Int("workers", 1, "workers per agent")
	)
	flag.Parse()

	var (
		cfg *manager.Config
		err error
	)
	if *cfgPath != "" {
		cfg, err = manager.LoadConfig(*cfgPath)
	} else {
		cfg, err = manager.LoadConfig()
	}
	if err != nil {
		log.Fatalf("acp-gateway: load config: %v", err)
	}

	store := NewStore(128)
	mgr := manager.New(cfg, hooksFor(store))
	srv := NewServer(Options{
		Manager:     mgr,
		Store:       store,
		Token:       os.Getenv("MODU_ACP_TOKEN"),
		WorkersEach: *workers,
	})

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("[acp-gateway] listening on %s (agents: %v)", *addr, mgr.List())
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("acp-gateway: serve: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Println("[acp-gateway] shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
	_ = srv.Close()
}
