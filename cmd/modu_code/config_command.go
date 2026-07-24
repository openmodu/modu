package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/provider"
)

func runConfigHook(args string, session *coding_agent.CodingSession) (string, error) {
	fields, parseErr := splitConfigArgs(args)
	if parseErr != nil {
		return "", parseErr
	}
	var out bytes.Buffer
	err := runConfigCommand(fields, &out, &out)
	if err == nil && len(fields) > 0 && session != nil {
		switch fields[0] {
		case "add":
			appendConfigSessionSync(&out, session)
		case "use":
			if len(fields) == 2 {
				if switchErr := session.SetModelByName(fields[1]); switchErr != nil {
					fmt.Fprintf(&out, "current session: unchanged (%v)\n", switchErr)
				} else {
					fmt.Fprintln(&out, "current session: switched")
				}
			}
		}
	}
	return out.String(), err
}

func configProviderEntries() ([]ConfigProviderEntry, error) {
	cfg, exists, err := provider.LoadConfigFile()
	if err != nil {
		return nil, err
	}
	if !exists {
		return configProviderPresetEntries(nil), nil
	}
	out := make([]ConfigProviderEntry, 0, len(cfg.Providers))
	seen := map[string]bool{}
	for name, pc := range cfg.Providers {
		seen[name] = true
		out = append(out, ConfigProviderEntry{
			Name:      name,
			Type:      pc.Type,
			BaseURL:   pc.BaseURL,
			APIKeyEnv: pc.APIKeyEnv,
		})
	}
	out = append(out, configProviderPresetEntries(seen)...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func configProviderPresetEntries(seen map[string]bool) []ConfigProviderEntry {
	presets := []ConfigProviderEntry{
		{Name: "deepseek", Type: "openai-compatible", BaseURL: "https://api.deepseek.com/v1", APIKeyEnv: "DEEPSEEK_API_KEY"},
		{Name: "lmstudio", Type: "openai-compatible", BaseURL: "http://127.0.0.1:1234/v1"},
		{Name: "ollama", Type: "openai-compatible", BaseURL: "http://127.0.0.1:11434/v1"},
		{Name: "openai", Type: "openai-compatible", BaseURL: "https://api.openai.com/v1", APIKeyEnv: "OPENAI_API_KEY"},
	}
	out := make([]ConfigProviderEntry, 0, len(presets))
	for _, preset := range presets {
		if seen != nil && seen[preset.Name] {
			continue
		}
		out = append(out, preset)
	}
	return out
}

func configSetProvider(input ConfigProviderInput, session *coding_agent.CodingSession) (string, error) {
	err := provider.UpsertProviderConfig(input.Provider, provider.ProviderConfig{
		Type:      input.Type,
		BaseURL:   input.BaseURL,
		APIKey:    input.APIKey,
		APIKeyEnv: input.APIKeyEnv,
	})
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	fmt.Fprintf(&out, "saved provider: %s\n", input.Provider)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	discovery, discoverErr := provider.DiscoverProviderModels(ctx, input.Provider)
	if discoverErr != nil {
		fmt.Fprintf(&out, "model discovery: failed (%v)\n", discoverErr)
	} else {
		fmt.Fprintf(&out, "model discovery: found=%d added=%d updated=%d\n", discovery.Found, discovery.Added, discovery.Updated)
	}
	fmt.Fprintf(&out, "config: %s\n", provider.ConfigPath())
	appendConfigSessionSync(&out, session)
	return out.String(), nil
}

func appendConfigSessionSync(out io.Writer, session *coding_agent.CodingSession) {
	if out == nil || session == nil {
		return
	}
	model, _ := provider.Resolve()
	if model == nil {
		fmt.Fprintln(out, "current session: configure an active model to start chatting")
		return
	}
	current := session.GetModel()
	if current != nil && current.ProviderID == model.ProviderID && current.ID == model.ID {
		fmt.Fprintln(out, "current session: active model already selected")
		return
	}
	session.SetModel(model)
	fmt.Fprintln(out, "current session: switched")
}

func runConfigCommand(args []string, stdout, stderr io.Writer) error {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if len(args) == 0 {
		return printConfigShow(stdout)
	}
	switch args[0] {
	case "show":
		if len(args) != 1 {
			return fmt.Errorf("usage: modu_code config show")
		}
		return printConfigShow(stdout)
	case "path":
		if len(args) != 1 {
			return fmt.Errorf("usage: modu_code config path")
		}
		_, err := fmt.Fprintln(stdout, provider.ConfigPath())
		return err
	case "list", "ls":
		if len(args) != 1 {
			return fmt.Errorf("usage: modu_code config list")
		}
		return printConfigList(stdout)
	case "example":
		if len(args) != 1 {
			return fmt.Errorf("usage: modu_code config example")
		}
		_, err := fmt.Fprint(stdout, provider.ExampleConfigTOML())
		return err
	case "init":
		force := len(args) > 1 && args[1] == "--force"
		if len(args) > 2 || (len(args) == 2 && !force) {
			return fmt.Errorf("usage: modu_code config init [--force]")
		}
		path, err := provider.InitConfig(force)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "wrote config: %s\n", path)
		return err
	case "add":
		entry, err := parseConfigAdd(args[1:])
		if err != nil {
			return err
		}
		created, err := provider.UpsertModelConfig(entry)
		if err != nil {
			return err
		}
		action := "updated"
		if created {
			action = "added"
		}
		fmt.Fprintf(stdout, "%s model: %s\n", action, entry.Name)
		fmt.Fprintf(stdout, "config: %s\n", provider.ConfigPath())
		return nil
	case "use":
		if len(args) != 2 {
			return fmt.Errorf("usage: modu_code config use <name|provider/model|provider:model|model>")
		}
		model, err := provider.SetActiveModel(args[1])
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "active: %s\n", modelLabel(model))
		fmt.Fprintf(stdout, "config: %s\n", provider.ConfigPath())
		return nil
	case "remove", "rm":
		if len(args) != 2 {
			return fmt.Errorf("usage: modu_code config remove <name|provider/model|provider:model|model>")
		}
		model, err := provider.RemoveModelConfig(args[1])
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "removed model: %s\n", modelLabel(model))
		fmt.Fprintf(stdout, "config: %s\n", provider.ConfigPath())
		return nil
	case "validate":
		if len(args) != 1 {
			return fmt.Errorf("usage: modu_code config validate")
		}
		result := provider.ValidateConfig()
		fmt.Fprintf(stdout, "config: %s\n", result.Path)
		fmt.Fprintf(stdout, "models: %d\n", result.ModelCount)
		if result.Active != "" {
			fmt.Fprintf(stdout, "active: %s\n", result.Active)
		}
		if len(result.Problems) == 0 {
			_, err := fmt.Fprintln(stdout, "status: ok")
			return err
		}
		fmt.Fprintf(stderr, "problems (%d):\n", len(result.Problems))
		for _, problem := range result.Problems {
			fmt.Fprintf(stderr, "  - %s\n", problem)
		}
		return fmt.Errorf("config validation failed")
	default:
		return fmt.Errorf("unknown config command %q; expected show, list, add, use, remove, example, init, or validate", strings.TrimSpace(args[0]))
	}
}

func printConfigShow(stdout io.Writer) error {
	cfg, exists, err := provider.LoadConfigFile()
	fmt.Fprintf(stdout, "config: %s\n", provider.ConfigPath())
	if err != nil {
		fmt.Fprintf(stdout, "status: invalid TOML: %v\n", err)
	} else if !exists {
		fmt.Fprintln(stdout, "status: missing")
	} else if len(cfg.Models) == 0 {
		fmt.Fprintln(stdout, "status: empty")
	} else {
		fmt.Fprintln(stdout, "status: configured")
		if cfg.Active != "" {
			fmt.Fprintf(stdout, "active: %s\n", cfg.Active)
		}
		fmt.Fprintf(stdout, "models: %d\n", len(cfg.Models))
		if len(cfg.ScopedModels) > 0 {
			fmt.Fprintf(stdout, "scopedModels: %s\n", strings.Join(cfg.ScopedModels, ", "))
		}
	}
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "TUI:")
	fmt.Fprintln(stdout, "  /config")
	fmt.Fprintln(stdout, "  /config add <name> <provider> <model> <baseUrl> [apiKey] [--description text]")
	fmt.Fprintln(stdout, "  /config use <name|provider/model|provider:model|model>")
	fmt.Fprintln(stdout, "  /config list")
	fmt.Fprintln(stdout, "  /config validate")
	return nil
}

func printConfigList(stdout io.Writer) error {
	cfg, exists, err := provider.LoadConfigFile()
	fmt.Fprintf(stdout, "config: %s\n", provider.ConfigPath())
	if err != nil {
		return fmt.Errorf("invalid TOML: %w", err)
	}
	if !exists || len(cfg.Models) == 0 {
		fmt.Fprintln(stdout, "models: none")
		return nil
	}
	models := append([]provider.ModelConfig(nil), cfg.Models...)
	sort.SliceStable(models, func(i, j int) bool {
		if models[i].Provider == models[j].Provider {
			return models[i].Model < models[j].Model
		}
		return models[i].Provider < models[j].Provider
	})
	fmt.Fprintf(stdout, "active: %s\n", valueOr(cfg.Active, "(first model)"))
	fmt.Fprintf(stdout, "models: %d\n", len(models))
	for _, model := range models {
		marker := " "
		if provider.ModelMatchesTarget(model, cfg.Active) {
			marker = "*"
		}
		line := fmt.Sprintf("%s %s  %s  %s", marker, modelLabel(model), model.Provider+"/"+model.Model, cfg.Providers[model.Provider].BaseURL)
		if model.Description != "" {
			line += "  " + model.Description
		}
		fmt.Fprintln(stdout, line)
	}
	return nil
}

func parseConfigAdd(args []string) (provider.ModelConfig, error) {
	desc := ""
	descIdx := -1
	for i, arg := range args {
		if arg == "--description" || arg == "--desc" {
			descIdx = i
			break
		}
	}
	base := args
	if descIdx >= 0 {
		base = args[:descIdx]
		if descIdx+1 >= len(args) {
			return provider.ModelConfig{}, fmt.Errorf("usage: modu_code config add <name> <provider> <model> <baseUrl> [apiKey] [--description text]")
		}
		desc = strings.Join(args[descIdx+1:], " ")
	}
	if len(base) < 4 || len(base) > 5 {
		return provider.ModelConfig{}, fmt.Errorf("usage: modu_code config add <name> <provider> <model> <baseUrl> [apiKey] [--description text]")
	}
	entry := provider.ModelConfig{
		Name:        base[0],
		Provider:    base[1],
		Model:       base[2],
		BaseURL:     base[3],
		Description: desc,
	}
	if len(base) == 5 {
		entry.APIKey = base[4]
	}
	return entry, nil
}

func modelLabel(model provider.ModelConfig) string {
	if model.Name != "" {
		return model.Name
	}
	return model.Provider + "/" + model.Model
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func splitConfigArgs(input string) ([]string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, nil
	}
	var args []string
	var b strings.Builder
	var quote rune
	escaped := false
	for _, r := range input {
		switch {
		case escaped:
			b.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t' || r == '\n':
			if b.Len() > 0 {
				args = append(args, b.String())
				b.Reset()
			}
		default:
			b.WriteRune(r)
		}
	}
	if escaped {
		b.WriteRune('\\')
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote in config command")
	}
	if b.Len() > 0 {
		args = append(args, b.String())
	}
	return args, nil
}
