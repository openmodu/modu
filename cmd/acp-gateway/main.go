// Command acp-gateway exposes ACP agents (Claude Code / Codex / Gemini) over
// a small HTTP API so remote clients (iOS, curl, any other HTTP caller) can
// dispatch tasks to the machine that owns the local file tree.
//
// Routes (all but /healthz require Bearer auth):
//
//	GET  /healthz
//	GET  /api/agents
//	GET  /api/workdir                return current default working directory
//	GET  /api/files?path=subdir      list files/dirs relative to workdir
//	POST /api/projects               {name, path}
//	POST /api/sessions               {projectId, agent, profileId, title}
//	POST /api/sessions/{id}/turns    {prompt} → {id, status, ...}
//	GET  /api/sessions/{id}/turns/{turnId}/stream
//	POST /api/sessions/{id}/turns/{turnId}/approve {toolCallId, optionId}
package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/openmodu/modu/pkg/acp/manager"
	"github.com/openmodu/modu/pkg/tokenkit"
)

func main() {
	var (
		addr    = flag.String("addr", ":7080", "HTTP listen address")
		cfgPath = flag.String("config", "", "path to acp.config.json (empty = default lookup)")
		workers = flag.Int("workers", 1, "workers per agent")
		dbPath  = flag.String("db", "acp-gateway.db", "SQLite database path (empty = no persistence)")
		cwd     = flag.String("cwd", "", "default working directory for tasks (overrides config workdir)")

		tokenkitSyncInterval = flag.Duration("tokenkit-sync-interval", 5*time.Minute, "background tokenkit sync interval (0 disables)")
		tokenkitTimezone     = flag.String("tokenkit-timezone", "", "IANA timezone for tokenkit local dates (empty = local timezone)")
		tokenkitCodexHome    = flag.String("tokenkit-codex-home", "", "Codex home for tokenkit sync (empty = ~/.codex)")
		tokenkitClaudeHome   = flag.String("tokenkit-claude-home", "", "Claude home for tokenkit sync (empty = ~/.claude)")
		tokenkitGeminiLog    = flag.String("tokenkit-gemini-log", "", "Gemini telemetry log for tokenkit sync (empty = auto-detect)")
	)
	flag.Parse()

	var (
		cfg            *manager.Config
		resolvedConfig string
		err            error
	)
	if *cfgPath != "" {
		cfg, resolvedConfig, err = manager.LoadConfigWithPath(*cfgPath)
	} else {
		cfg, resolvedConfig, err = manager.LoadConfigWithPath()
	}
	if err != nil {
		log.Fatalf("acp-gateway: load config: %v", err)
	}

	var db *sql.DB
	if *dbPath != "" {
		var dbErr error
		db, dbErr = openDB(*dbPath)
		if dbErr != nil {
			log.Fatalf("acp-gateway: %v", dbErr)
		}
		defer db.Close()
	}

	store := NewStore(128, db)
	if err := dbLoadAll(db, store); err != nil {
		log.Printf("[acp-gateway] warn: load data: %v", err)
	}
	var tokenkitStore *tokenkit.Store
	if db != nil {
		tokenkitStore = tokenkit.NewStore(db)
		if err := tokenkitStore.Init(context.Background()); err != nil {
			log.Fatalf("acp-gateway: init tokenkit: %v", err)
		}
	}
	// Resolve workdir: CLI flag > config file > process cwd.
	workdir := *cwd
	if workdir == "" {
		workdir = cfg.Workdir
	}
	if workdir == "" {
		workdir, _ = os.Getwd()
	}
	var tokenkitLocation *time.Location
	if *tokenkitTimezone != "" {
		var tzErr error
		tokenkitLocation, tzErr = time.LoadLocation(*tokenkitTimezone)
		if tzErr != nil {
			log.Fatalf("acp-gateway: invalid tokenkit timezone: %v", tzErr)
		}
	}

	mgr := manager.New(cfg, hooksFor(store))
	srv := NewServer(Options{
		Manager:     mgr,
		Store:       store,
		Tokenkit:    tokenkitStore,
		Token:       os.Getenv("MODU_ACP_TOKEN"),
		WorkersEach: *workers,
		Workdir:     workdir,
		ConfigPath:  resolvedConfig,
		TokenkitScannerOptions: tokenkit.ScannerOptions{
			CodexHome:          *tokenkitCodexHome,
			ClaudeHome:         *tokenkitClaudeHome,
			GeminiTelemetryLog: *tokenkitGeminiLog,
			Location:           tokenkitLocation,
		},
		TokenkitScanInterval: *tokenkitSyncInterval,
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
