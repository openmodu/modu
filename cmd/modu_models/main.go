// modu_models manages model routes shared by Codex and Claude Code.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/openmodu/modu/pkg/modelrouter"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "modu_models:", err)
		os.Exit(1)
	}
}

func configPath() string {
	if path := os.Getenv("MODU_MODELROUTER_CONFIG"); path != "" {
		return path
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".modu", "modelrouter", "config.json")
}

func gatewayURL() string {
	if url := os.Getenv("MODU_MODELROUTER_URL"); url != "" {
		return url
	}
	return "http://127.0.0.1:3425"
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usage(stdout)
	}
	path := configPath()
	cfg, err := modelrouter.Load(path)
	if err != nil {
		return err
	}
	switch args[0] {
	case "help", "--help", "-h":
		return usage(stdout)
	case "providers":
		for _, p := range cfg.Providers {
			fmt.Fprintf(stdout, "%s\t%s\t%s\n", p.ID, p.BaseURL, strings.Join(p.Models, ","))
		}
		return nil
	case "models":
		var models []string
		for _, p := range cfg.Providers {
			for _, name := range p.Models {
				models = append(models, p.ID+"/"+name)
			}
		}
		sort.Strings(models)
		for _, name := range models {
			fmt.Fprintln(stdout, name)
		}
		return nil
	case "provider":
		return providerCommand(cfg, path, args[1:], stdout, stderr)
	case "use":
		if len(args) != 3 {
			return fmt.Errorf("usage: modu_models use <codex|claude> <provider/model>")
		}
		manager := modelrouter.AgentManager{GatewayURL: gatewayURL(), Config: cfg}
		if err := manager.Use(args[1], args[2]); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s now uses %s; restart the agent to apply it\n", args[1], args[2])
		return nil
	case "restore":
		if len(args) != 2 {
			return fmt.Errorf("usage: modu_models restore <codex|claude>")
		}
		manager := modelrouter.AgentManager{GatewayURL: gatewayURL(), Config: cfg}
		if err := manager.Restore(args[1]); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s restored; restart the agent to apply it\n", args[1])
		return nil
	case "serve":
		if len(args) != 1 {
			return fmt.Errorf("usage: modu_models serve")
		}
		return serve(cfg, stdout)
	default:
		return fmt.Errorf("unknown command %q; run modu_models help", args[0])
	}
}

func providerCommand(cfg modelrouter.Config, path string, args []string, stdout, stderr io.Writer) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: modu_models provider add|rm <id> [options]")
	}
	switch args[0] {
	case "add":
		flags := flag.NewFlagSet("provider add", flag.ContinueOnError)
		flags.SetOutput(stderr)
		url := flags.String("url", "", "provider OpenAI-compatible base URL")
		models := flags.String("models", "", "comma-separated model IDs")
		keyEnv := flags.String("key-env", "", "environment variable containing the API key")
		if err := flags.Parse(args[2:]); err != nil {
			return err
		}
		if *url == "" || *models == "" || flags.NArg() != 0 {
			return fmt.Errorf("usage: modu_models provider add <id> --url <url> --models <id,...> [--key-env <name>]")
		}
		p := modelrouter.Provider{ID: args[1], BaseURL: *url, Models: strings.Split(*models, ","), APIKeyEnv: *keyEnv}
		found := false
		for i := range cfg.Providers {
			if cfg.Providers[i].ID == p.ID {
				cfg.Providers[i] = p
				found = true
				break
			}
		}
		if !found {
			cfg.Providers = append(cfg.Providers, p)
		}
		if err := modelrouter.Save(path, cfg); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "saved provider %s\n", p.ID)
		return nil
	case "rm":
		if len(args) != 2 {
			return fmt.Errorf("usage: modu_models provider rm <id>")
		}
		for i, p := range cfg.Providers {
			if p.ID == args[1] {
				cfg.Providers = append(cfg.Providers[:i], cfg.Providers[i+1:]...)
				if err := modelrouter.Save(path, cfg); err != nil {
					return err
				}
				fmt.Fprintf(stdout, "removed provider %s\n", args[1])
				return nil
			}
		}
		return fmt.Errorf("unknown provider %q", args[1])
	default:
		return fmt.Errorf("unknown provider command %q", args[0])
	}
}

func serve(cfg modelrouter.Config, stdout io.Writer) error {
	router, err := modelrouter.New(cfg)
	if err != nil {
		return err
	}
	host := strings.TrimPrefix(gatewayURL(), "http://")
	address, err := net.ResolveTCPAddr("tcp", host)
	if err != nil || address.IP == nil || !address.IP.IsLoopback() {
		return fmt.Errorf("gateway must bind to a loopback address")
	}
	server := &http.Server{Addr: host, Handler: router.Handler(), ReadHeaderTimeout: 10_000_000_000}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()
	fmt.Fprintf(stdout, "listening on %s\n", gatewayURL())
	err = server.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func usage(out io.Writer) error {
	_, err := fmt.Fprint(out, "modu_models providers | models | provider add <id> --url <url> --models <id,...> [--key-env <name>] | provider rm <id> | use <codex|claude> <provider/model> | restore <codex|claude> | serve\n")
	return err
}
