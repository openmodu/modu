package main

import (
	"context"
	"fmt"
	"strings"

	codetui "github.com/openmodu/modu/cmd/modu_code/internal/tui"
	modutui "github.com/openmodu/modu/pkg/modu-tui"
)

type moduTUIConfigWizard struct {
	hooks     CommandHooks
	dialog    codetui.Dialog
	providers []ConfigProviderEntry
}

func newModuTUIConfigWizard(hooks CommandHooks, client modutui.Client) *moduTUIConfigWizard {
	return &moduTUIConfigWizard{
		hooks:  hooks,
		dialog: codetui.NewDialog(client, moduTUITerminalStatusTTL),
	}
}

func (w *moduTUIConfigWizard) Start(ctx context.Context) {
	if w == nil {
		return
	}
	providers, err := w.loadProviders(ctx)
	if err != nil {
		w.dialog.Post("Config\n\nerror: " + err.Error())
		return
	}
	w.providers = providers
	for ctx.Err() == nil {
		w.dialog.Status("config")
		result, completed := w.run(ctx, codetui.Flow{
			ID: "config",
			Steps: []codetui.FlowStep{{
				ID:    "action",
				Kind:  codetui.FlowStepChoice,
				Title: "Config",
				Body:  "Choose an action",
				Options: []modutui.HumanPromptOption{
					{Label: "Setup provider", Value: "setup"},
					{Label: "Show config status", Value: "status"},
				},
			}},
		})
		if !completed {
			w.dialog.Status("config cancelled")
			w.dialog.Post("Config closed.")
			return
		}
		switch result.Value("action") {
		case "setup":
			w.configureProvider(ctx)
		case "status":
			w.showStatus()
		default:
			return
		}
	}
}

func (w *moduTUIConfigWizard) loadProviders(ctx context.Context) ([]ConfigProviderEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if w.hooks.ConfigProviders == nil {
		return nil, fmt.Errorf("config providers are not available")
	}
	return w.hooks.ConfigProviders()
}

func (w *moduTUIConfigWizard) configureProvider(ctx context.Context) {
	providers := configWizardSetupProviders(w.providers)
	options := make([]modutui.HumanPromptOption, 0, len(providers)+1)
	for _, provider := range providers {
		options = append(options, modutui.HumanPromptOption{
			Label: configWizardProviderLabel(provider.Name),
			Value: provider.Name,
		})
	}
	options = append(options, modutui.HumanPromptOption{
		Label: "Custom OpenAI-Compatible",
		Value: "custom",
	})
	selection, completed := w.run(ctx, codetui.Flow{
		ID: "config-provider-select",
		Steps: []codetui.FlowStep{{
			ID:      "provider",
			Kind:    codetui.FlowStepChoice,
			Title:   "Config: provider",
			Body:    "Choose a provider",
			Options: options,
		}},
	})
	if !completed {
		return
	}

	providerName := selection.Value("provider")
	var provider ConfigProviderInput
	if providerName == "custom" {
		custom, ok := w.run(ctx, codetui.Flow{
			ID: "config-custom-provider",
			Steps: []codetui.FlowStep{{
				ID:          "name",
				Kind:        codetui.FlowStepText,
				Title:       "Config: custom provider",
				Body:        "Provider name",
				Placeholder: "openrouter",
				Required:    true,
			}},
		})
		if !ok {
			return
		}
		provider = ConfigProviderInput{
			Provider: custom.Value("name"),
			Type:     "openai-compatible",
		}
	} else {
		selected, ok := configWizardFindProvider(providers, providerName)
		if !ok {
			w.dialog.Post("Config provider\n\nerror: selected provider is not available")
			return
		}
		provider = ConfigProviderInput{
			Provider:  selected.Name,
			Type:      valueOr(selected.Type, "openai-compatible"),
			BaseURL:   selected.BaseURL,
			APIKeyEnv: selected.APIKeyEnv,
		}
	}
	w.configureProviderDetails(ctx, provider)
}

func (w *moduTUIConfigWizard) configureProviderDetails(ctx context.Context, provider ConfigProviderInput) {
	auth, completed := w.run(ctx, codetui.Flow{
		ID: "config-provider-auth",
		Steps: []codetui.FlowStep{{
			ID:    "method",
			Kind:  codetui.FlowStepChoice,
			Title: "Config: " + provider.Provider,
			Body:  "How do you want to configure the API key?",
			Options: []modutui.HumanPromptOption{
				{Label: "Paste API key", Value: "api-key"},
				{Label: "Use environment variable", Value: "env"},
				{Label: "Skip key", Value: "skip"},
			},
		}},
	})
	if !completed {
		return
	}

	switch auth.Value("method") {
	case "api-key":
		key, ok := w.run(ctx, codetui.Flow{
			ID: "config-provider-api-key",
			Steps: []codetui.FlowStep{{
				ID:          "api-key",
				Kind:        codetui.FlowStepText,
				Title:       "Config: API key",
				Body:        "Paste API key. It is masked and will not be added to transcript history.",
				Placeholder: "sk-...",
				Secret:      true,
				Required:    true,
			}},
		})
		if !ok {
			return
		}
		provider.APIKey = strings.TrimSpace(key.Value("api-key"))
		provider.APIKeyEnv = ""
	case "env":
		env, ok := w.run(ctx, codetui.Flow{
			ID: "config-provider-api-key-env",
			Steps: []codetui.FlowStep{{
				ID:          "api-key-env",
				Kind:        codetui.FlowStepText,
				Title:       "Config: API key env",
				Body:        "Environment variable that contains the API key.",
				Placeholder: "OPENAI_API_KEY",
				Default:     provider.APIKeyEnv,
				Required:    true,
			}},
		})
		if !ok {
			return
		}
		provider.APIKeyEnv = env.Value("api-key-env")
		provider.APIKey = ""
	case "skip":
		provider.APIKey = ""
		provider.APIKeyEnv = ""
	default:
		return
	}

	baseURL, completed := w.run(ctx, codetui.Flow{
		ID: "config-provider-base-url",
		Steps: []codetui.FlowStep{{
			ID:          "base-url",
			Kind:        codetui.FlowStepText,
			Title:       "Config: base URL",
			Body:        "OpenAI-compatible API base URL.",
			Placeholder: "https://api.openai.com/v1",
			Default:     provider.BaseURL,
			Required:    true,
		}},
	})
	if !completed {
		return
	}
	provider.BaseURL = baseURL.Value("base-url")
	w.saveProvider(provider)
}

func (w *moduTUIConfigWizard) saveProvider(provider ConfigProviderInput) {
	if w.hooks.ConfigSetProvider == nil {
		w.dialog.Post("Config provider is not available.")
		return
	}
	w.dialog.Status("saving provider")
	out, err := w.hooks.ConfigSetProvider(provider)
	w.dialog.PostResult("Config provider", out, err)
}

func (w *moduTUIConfigWizard) showStatus() {
	if w.hooks.Config == nil {
		w.dialog.Post("Config status is not available.")
		return
	}
	out, err := w.hooks.Config("")
	w.dialog.PostResult("Config status", out, err)
}

func (w *moduTUIConfigWizard) run(ctx context.Context, flow codetui.Flow) (codetui.FlowResult, bool) {
	result, completed, err := w.dialog.RunFlow(ctx, flow)
	if err != nil && err != context.Canceled {
		w.dialog.Post(flow.ID + "\n\nerror: " + err.Error())
	}
	return result, completed
}

func configWizardSetupProviders(entries []ConfigProviderEntry) []ConfigProviderEntry {
	names := []string{"deepseek", "lmstudio", "ollama"}
	out := make([]ConfigProviderEntry, 0, len(names))
	for _, name := range names {
		if entry, ok := configWizardFindProvider(entries, name); ok {
			out = append(out, entry)
			continue
		}
		out = append(out, configWizardDefaultProvider(name))
	}
	return out
}

func configWizardFindProvider(entries []ConfigProviderEntry, name string) (ConfigProviderEntry, bool) {
	name = strings.TrimSpace(strings.ToLower(name))
	for _, entry := range entries {
		if strings.ToLower(strings.TrimSpace(entry.Name)) == name {
			return entry, true
		}
	}
	return ConfigProviderEntry{}, false
}

func configWizardDefaultProvider(name string) ConfigProviderEntry {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "deepseek":
		return ConfigProviderEntry{Name: "deepseek", Type: "openai-compatible", BaseURL: "https://api.deepseek.com/v1", APIKeyEnv: "DEEPSEEK_API_KEY"}
	case "lmstudio":
		return ConfigProviderEntry{Name: "lmstudio", Type: "openai-compatible", BaseURL: "http://127.0.0.1:1234/v1"}
	case "ollama":
		return ConfigProviderEntry{Name: "ollama", Type: "openai-compatible", BaseURL: "http://127.0.0.1:11434/v1"}
	default:
		return ConfigProviderEntry{Name: strings.TrimSpace(name), Type: "openai-compatible"}
	}
}

func configWizardProviderLabel(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "deepseek":
		return "DeepSeek"
	case "lmstudio":
		return "LMStudio"
	case "ollama":
		return "Ollama"
	default:
		return strings.TrimSpace(name)
	}
}
