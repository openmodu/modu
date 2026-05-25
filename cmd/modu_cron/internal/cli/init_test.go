package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openmodu/modu/cmd/modu_cron/internal/config"
)

func TestInitWritesRuntimeConfigAndTaskFile(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	workdir := t.TempDir()
	var out bytes.Buffer
	err := Init(cfgPath, InitOptions{
		WorkingDir:        workdir,
		ModelProvider:     "openai",
		Model:             "gpt-4o",
		ModelBaseURL:      "https://api.openai.com/v1",
		ModelAPIKeyEnv:    "OPENAI_API_KEY",
		TelegramTokenEnv:  "TG_TOKEN",
		TelegramChatIDEnv: "TG_CHAT",
	}, &out)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	runtimeCfg, err := config.LoadRuntime(cfgPath)
	if err != nil {
		t.Fatalf("LoadRuntime: %v", err)
	}
	if runtimeCfg.WorkingDir != workdir || runtimeCfg.TasksFile != "tasks.yaml" {
		t.Fatalf("unexpected runtime config: %+v", runtimeCfg)
	}
	if runtimeCfg.Model.Provider != "openai" || runtimeCfg.Model.APIKeyEnv != "OPENAI_API_KEY" {
		t.Fatalf("model config not written: %+v", runtimeCfg.Model)
	}
	if runtimeCfg.Channels["telegram-home"].TokenEnv != "TG_TOKEN" ||
		runtimeCfg.Channels["telegram-home"].ChatIDEnv != "TG_CHAT" {
		t.Fatalf("telegram channel not written: %+v", runtimeCfg.Channels)
	}
	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Tasks) != 0 {
		t.Fatalf("new task file should be empty: %+v", loaded.Tasks)
	}
	if !strings.Contains(out.String(), "wrote config") || !strings.Contains(out.String(), "wrote tasks") {
		t.Fatalf("init output missing paths: %s", out.String())
	}
}

func TestInitRefusesExistingConfigWithoutForce(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := Init(cfgPath, InitOptions{}, nil); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	err := Init(cfgPath, InitOptions{}, nil)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected already-exists error, got %v", err)
	}
}

func TestInitInteractivePrompts(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	workdir := t.TempDir()
	input := strings.Join([]string{
		workdir,
		"jobs.yaml",
		"openai",
		"",
		"",
		"",
		"y",
		"tg-main",
		"",
		"TG_TOKEN",
		"",
		"TG_CHAT",
	}, "\n") + "\n"
	var out bytes.Buffer
	err := Init(cfgPath, InitOptions{
		Interactive: true,
		In:          strings.NewReader(input),
	}, &out)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	cfg, err := config.LoadRuntime(cfgPath)
	if err != nil {
		t.Fatalf("LoadRuntime: %v", err)
	}
	if cfg.WorkingDir != workdir || cfg.TasksFile != "jobs.yaml" {
		t.Fatalf("interactive paths not used: %+v", cfg)
	}
	if cfg.Model.Provider != "openai" || cfg.Model.Model != "gpt-4o" ||
		cfg.Model.BaseURL != "https://api.openai.com/v1" || cfg.Model.APIKeyEnv != "OPENAI_API_KEY" {
		t.Fatalf("interactive model defaults not used: %+v", cfg.Model)
	}
	if cfg.Channels["tg-main"].TokenEnv != "TG_TOKEN" || cfg.Channels["tg-main"].ChatIDEnv != "TG_CHAT" {
		t.Fatalf("interactive telegram channel not used: %+v", cfg.Channels)
	}
	if !strings.Contains(out.String(), "Working directory") ||
		!strings.Contains(out.String(), "Telegram channel name") {
		t.Fatalf("expected prompts in output, got: %s", out.String())
	}
}

func TestInitInteractiveStoresDirectTelegramValues(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	input := strings.Join([]string{
		"", "", "", "", "", "",
		"y",
		"telegram-direct",
		"123456:ABC",
		"329251546",
	}, "\n") + "\n"
	if err := Init(cfgPath, InitOptions{
		Interactive: true,
		In:          strings.NewReader(input),
	}, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	cfg, err := config.LoadRuntime(cfgPath)
	if err != nil {
		t.Fatalf("LoadRuntime: %v", err)
	}
	ch := cfg.Channels["telegram-direct"]
	if ch.Token != "123456:ABC" || ch.ChatID != "329251546" {
		t.Fatalf("direct telegram values not stored: %+v", ch)
	}
	if ch.TokenEnv != "" || ch.ChatIDEnv != "" {
		t.Fatalf("env fields should be empty when direct values are entered: %+v", ch)
	}
}

func TestInitInteractiveCanSkipTelegram(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	input := strings.Repeat("\n", 6) + "n\n"
	if err := Init(cfgPath, InitOptions{
		Interactive: true,
		In:          strings.NewReader(input),
	}, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	cfg, err := config.LoadRuntime(cfgPath)
	if err != nil {
		t.Fatalf("LoadRuntime: %v", err)
	}
	if len(cfg.Channels) != 0 {
		t.Fatalf("expected no channels, got %+v", cfg.Channels)
	}
}
