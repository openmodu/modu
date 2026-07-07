package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"

	modutui "github.com/openmodu/modu/pkg/modu-tui"
)

type moduTUIConfigWizard struct {
	mu    sync.Mutex
	hooks CommandHooks
	send  func(tea.Msg)

	active    bool
	step      string
	providers []ConfigProviderEntry
	provider  ConfigProviderInput
	model     ConfigModelInput
	models    []ConfigModelEntry
}

func newModuTUIConfigWizard(hooks CommandHooks, send func(tea.Msg)) *moduTUIConfigWizard {
	return &moduTUIConfigWizard{hooks: hooks, send: send}
}

func (w *moduTUIConfigWizard) Start(ctx context.Context) {
	if w == nil {
		return
	}
	providers, err := w.loadProviders(ctx)
	if err != nil {
		w.post("Config\n\nerror: " + err.Error())
		return
	}
	w.mu.Lock()
	w.active = false
	w.step = ""
	w.providers = providers
	w.provider = ConfigProviderInput{}
	w.model = ConfigModelInput{}
	w.models = nil
	w.mu.Unlock()
	w.openMenu(ctx)
}

func (w *moduTUIConfigWizard) openMenu(ctx context.Context) {
	w.setStatus("config")
	choice := w.requestChoice(ctx, "Config", "Choose an action", []modutui.HumanPromptOption{
		{Label: "Setup with provider or add model manually", Value: "setup"},
		{Label: "Show config status", Value: "status"},
	})
	switch choice {
	case "setup":
		w.startProviderChoice(ctx)
	case "status":
		w.showStatus(ctx)
	default:
		w.cancel("config cancelled")
	}
}

func (w *moduTUIConfigWizard) HandleInput(ctx context.Context, input string) bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	active := w.active
	w.mu.Unlock()
	if !active {
		return false
	}
	go w.handleInput(ctx, input)
	return true
}

func (w *moduTUIConfigWizard) handleInput(ctx context.Context, input string) {
	input = strings.TrimSpace(input)
	if strings.EqualFold(input, "q") || strings.EqualFold(input, "quit") || strings.EqualFold(input, "cancel") {
		w.cancel("config cancelled")
		return
	}
	if strings.EqualFold(input, "back") {
		w.backToMenu(ctx)
		return
	}

	w.mu.Lock()
	step := w.step
	w.mu.Unlock()

	switch step {
	case "menu":
		w.handleMenu(ctx, input)
	case "provider-choice":
		w.handleProviderChoice(input)
	case "provider-name":
		w.handleProviderName(ctx, input)
	case "provider-api-key-env":
		w.handleProviderAPIKeyEnv(input)
	case "provider-base-url":
		w.handleProviderBaseURL(ctx, input)
	case "model-name":
		w.handleModelName(input)
	case "model-provider":
		w.handleModelProvider(input)
	case "model-id":
		w.handleModelID(input)
	case "model-base-url":
		w.handleModelBaseURL(input)
	case "model-description":
		w.handleModelDescription(ctx, input)
	case "active-choice":
		w.handleActiveChoice(ctx, input)
	default:
		w.backToMenu(ctx)
	}
}

func (w *moduTUIConfigWizard) loadProviders(ctx context.Context) ([]ConfigProviderEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if w.hooks.ConfigProviders == nil {
		return nil, fmt.Errorf("config providers are not available")
	}
	providers, err := w.hooks.ConfigProviders()
	if err != nil {
		return nil, err
	}
	return providers, nil
}

func (w *moduTUIConfigWizard) handleMenu(ctx context.Context, input string) {
	switch input {
	case "1":
		w.mu.Lock()
		w.step = "provider-choice"
		w.mu.Unlock()
		w.post(w.providerChoiceText())
	case "2":
		w.showStatus(ctx)
	default:
		w.post(w.menuText())
	}
}

func (w *moduTUIConfigWizard) startProviderChoice(ctx context.Context) {
	w.mu.Lock()
	providers := configWizardSetupProviders(w.providers)
	w.mu.Unlock()
	options := make([]modutui.HumanPromptOption, 0, len(providers)+1)
	for _, provider := range providers {
		options = append(options, modutui.HumanPromptOption{
			Label: configWizardProviderLabel(provider.Name),
			Value: provider.Name,
		})
	}
	options = append(options, modutui.HumanPromptOption{Label: "Custom OpenAI-Compatible", Value: "custom"})
	choice := w.requestChoice(ctx, "Config: provider", "Choose a provider", options)
	if choice == "" {
		w.openMenu(ctx)
		return
	}
	if choice == "custom" {
		name := w.requestText(ctx, modutui.HumanTextRequest{
			ID:          "config-provider-name",
			Title:       "Config: custom provider",
			Body:        "Provider name",
			Placeholder: "openrouter",
			Required:    true,
		})
		if configWizardIsBack(name) {
			w.startProviderChoice(ctx)
			return
		}
		w.mu.Lock()
		w.provider = ConfigProviderInput{Provider: strings.TrimSpace(name), Type: "openai-compatible"}
		w.mu.Unlock()
		w.configureProviderDetails(ctx)
		return
	}
	selected, ok := configWizardFindProvider(providers, choice)
	if !ok {
		w.startProviderChoice(ctx)
		return
	}
	w.mu.Lock()
	w.active = true
	w.provider = ConfigProviderInput{
		Provider:  selected.Name,
		Type:      valueOr(selected.Type, "openai-compatible"),
		BaseURL:   selected.BaseURL,
		APIKeyEnv: selected.APIKeyEnv,
	}
	w.mu.Unlock()
	w.configureProviderDetails(ctx)
}

func (w *moduTUIConfigWizard) handleProviderChoice(input string) {
	idx, err := strconv.Atoi(input)
	w.mu.Lock()
	providers := append([]ConfigProviderEntry(nil), w.providers...)
	w.mu.Unlock()
	if err != nil || idx < 1 || idx > len(providers)+1 {
		w.post(w.providerChoiceText())
		return
	}
	if idx == len(providers)+1 {
		w.mu.Lock()
		w.provider = ConfigProviderInput{Type: "openai-compatible"}
		w.step = "provider-name"
		w.mu.Unlock()
		w.post("Config: custom provider\n\nProvider name, e.g. openrouter")
		return
	}
	choice := providers[idx-1]
	w.mu.Lock()
	w.provider = ConfigProviderInput{
		Provider:  choice.Name,
		Type:      valueOr(choice.Type, "openai-compatible"),
		BaseURL:   choice.BaseURL,
		APIKeyEnv: choice.APIKeyEnv,
	}
	w.step = "provider-api-key-env"
	w.mu.Unlock()
	w.post(fmt.Sprintf("Config: %s\n\nAPI key env var name, or - to skip.\nDefault: %s", choice.Name, valueOr(choice.APIKeyEnv, "-")))
}

func (w *moduTUIConfigWizard) handleProviderName(ctx context.Context, input string) {
	if input == "" || input == "-" {
		w.post("Provider name is required.")
		return
	}
	w.mu.Lock()
	w.provider.Provider = input
	w.mu.Unlock()
	w.configureProviderDetails(ctx)
}

func (w *moduTUIConfigWizard) configureProviderDetails(ctx context.Context) {
	w.mu.Lock()
	provider := w.provider
	w.active = false
	w.step = ""
	w.mu.Unlock()

	auth := w.requestChoice(ctx, "Config: "+provider.Provider, "How do you want to configure the API key?", []modutui.HumanPromptOption{
		{Label: "Paste API key", Value: "api-key"},
		{Label: "Use environment variable", Value: "env"},
		{Label: "Skip key", Value: "skip"},
		{Label: "Back", Value: "back"},
	})
	switch auth {
	case "api-key":
		key := w.requestText(ctx, modutui.HumanTextRequest{
			ID:          "config-api-key",
			Title:       "Config: API key",
			Body:        "Paste API key. It is masked and will not be added to transcript history.",
			Placeholder: "sk-...",
			Secret:      true,
			Required:    true,
		})
		if configWizardIsBack(key) {
			w.configureProviderDetails(ctx)
			return
		}
		provider.APIKey = strings.TrimSpace(key)
		provider.APIKeyEnv = ""
	case "env":
		envName := w.requestText(ctx, modutui.HumanTextRequest{
			ID:          "config-api-key-env",
			Title:       "Config: API key env",
			Body:        "Environment variable that contains the API key.",
			Placeholder: "OPENAI_API_KEY",
			Default:     provider.APIKeyEnv,
			Required:    true,
		})
		if configWizardIsBack(envName) {
			w.configureProviderDetails(ctx)
			return
		}
		provider.APIKeyEnv = strings.TrimSpace(envName)
		provider.APIKey = ""
	case "skip":
		provider.APIKey = ""
		provider.APIKeyEnv = ""
	case "back", "":
		w.startProviderChoice(ctx)
		return
	default:
		w.cancel("config cancelled")
		return
	}

	baseURL := w.requestText(ctx, modutui.HumanTextRequest{
		ID:          "config-base-url",
		Title:       "Config: base URL",
		Body:        "OpenAI-compatible API base URL.",
		Placeholder: "https://api.openai.com/v1",
		Default:     provider.BaseURL,
		Required:    true,
	})
	if configWizardIsBack(baseURL) {
		w.configureProviderDetails(ctx)
		return
	}
	provider.BaseURL = strings.TrimSpace(baseURL)
	w.saveProvider(ctx, provider)
}

func (w *moduTUIConfigWizard) saveProvider(ctx context.Context, provider ConfigProviderInput) {
	if w.hooks.ConfigSetProvider == nil {
		w.post("Config provider is not available.")
		return
	}
	w.setStatus("saving provider")
	out, err := w.hooks.ConfigSetProvider(provider)
	w.postResult("Config provider", out, err)
	if ctx.Err() != nil {
		return
	}
	w.backToMenu(ctx)
}

func (w *moduTUIConfigWizard) handleProviderAPIKeyEnv(input string) {
	w.mu.Lock()
	if input != "" && input != "-" {
		w.provider.APIKeyEnv = input
	}
	baseURL := w.provider.BaseURL
	w.step = "provider-base-url"
	w.mu.Unlock()
	w.post(fmt.Sprintf("Base URL, or - to use default.\nDefault: %s", valueOr(baseURL, "(required)")))
}

func (w *moduTUIConfigWizard) handleProviderBaseURL(ctx context.Context, input string) {
	w.mu.Lock()
	if input != "" && input != "-" {
		w.provider.BaseURL = input
	}
	provider := w.provider
	w.mu.Unlock()
	if strings.TrimSpace(provider.Provider) == "" || strings.TrimSpace(provider.BaseURL) == "" {
		w.post("Provider name and base URL are required. Type back to return.")
		return
	}
	if w.hooks.ConfigSetProvider == nil {
		w.post("Config provider is not available.")
		return
	}
	w.setStatus("saving provider")
	out, err := w.hooks.ConfigSetProvider(provider)
	w.postResult("Config provider", out, err)
	if ctx.Err() != nil {
		return
	}
	w.backToMenu(ctx)
}

func (w *moduTUIConfigWizard) handleModelName(input string) {
	if input == "" || input == "-" {
		w.post("Model name is required.")
		return
	}
	w.mu.Lock()
	w.model.Name = input
	w.step = "model-provider"
	w.mu.Unlock()
	w.post("Provider name for this model, e.g. lmstudio, openai, deepseek")
}

func (w *moduTUIConfigWizard) handleModelProvider(input string) {
	if input == "" || input == "-" {
		w.post("Provider is required.")
		return
	}
	w.mu.Lock()
	w.model.Provider = input
	w.step = "model-id"
	w.mu.Unlock()
	w.post("Model ID, e.g. gpt-4o, deepseek-chat, qwen/qwen3")
}

func (w *moduTUIConfigWizard) handleModelID(input string) {
	if input == "" || input == "-" {
		w.post("Model ID is required.")
		return
	}
	w.mu.Lock()
	w.model.Model = input
	w.step = "model-base-url"
	w.mu.Unlock()
	w.post("Base URL for this model, e.g. http://127.0.0.1:1234/v1")
}

func (w *moduTUIConfigWizard) handleModelBaseURL(input string) {
	if input == "" || input == "-" {
		w.post("Base URL is required.")
		return
	}
	w.mu.Lock()
	w.model.BaseURL = input
	w.step = "model-description"
	w.mu.Unlock()
	w.post("Description, or - to skip.")
}

func (w *moduTUIConfigWizard) handleModelDescription(ctx context.Context, input string) {
	w.mu.Lock()
	if input != "-" {
		w.model.Description = input
	}
	model := w.model
	w.mu.Unlock()
	if w.hooks.ConfigAdd == nil {
		w.post("Config add is not available.")
		return
	}
	w.setStatus("saving model")
	out, err := w.hooks.ConfigAdd(model)
	w.postResult("Config add", out, err)
	if ctx.Err() != nil {
		return
	}
	w.backToMenu(ctx)
}

func (w *moduTUIConfigWizard) openActiveModelChoice(ctx context.Context) {
	if w.hooks.ConfigModels == nil {
		w.post("Config model list is not available.")
		return
	}
	models, err := w.hooks.ConfigModels()
	if err != nil {
		w.post("Config active model\n\nerror: " + err.Error())
		return
	}
	if ctx.Err() != nil {
		return
	}
	if len(models) == 0 {
		w.post("Config active model\n\nNo configured models yet. Choose 1 to set up a provider or 2 to add a model manually.")
		return
	}
	w.mu.Lock()
	w.models = models
	w.step = ""
	w.active = false
	w.mu.Unlock()
	options := make([]modutui.HumanPromptOption, 0, len(models))
	for i, model := range models {
		label := configWizardModelTarget(model) + "  " + model.Provider + "/" + model.Model
		if model.Active {
			label = "* " + label
		}
		options = append(options, modutui.HumanPromptOption{Label: label, Value: strconv.Itoa(i + 1)})
	}
	choice := w.requestChoice(ctx, "Config: active model", "Choose a model", options)
	if choice == "" {
		w.cancel("config cancelled")
		return
	}
	w.handleActiveChoice(ctx, choice)
}

func (w *moduTUIConfigWizard) handleActiveChoice(ctx context.Context, input string) {
	idx, err := strconv.Atoi(input)
	w.mu.Lock()
	models := append([]ConfigModelEntry(nil), w.models...)
	w.mu.Unlock()
	if err != nil || idx < 1 || idx > len(models) {
		w.post(w.activeChoiceText(models))
		return
	}
	if w.hooks.ConfigUse == nil {
		w.post("Config use is not available.")
		return
	}
	target := configWizardModelTarget(models[idx-1])
	w.setStatus("switching model")
	out, useErr := w.hooks.ConfigUse(target)
	w.postResult("Config active model", out, useErr)
	if ctx.Err() != nil {
		return
	}
	w.backToMenu(ctx)
}

func (w *moduTUIConfigWizard) showStatus(ctx context.Context) {
	if w.hooks.Config == nil {
		w.post("Config status is not available.")
		return
	}
	out, err := w.hooks.Config("")
	w.postResult("Config status", out, err)
	if ctx.Err() != nil {
		return
	}
	w.backToMenu(ctx)
}

func (w *moduTUIConfigWizard) backToMenu(ctx context.Context) {
	w.mu.Lock()
	w.active = false
	w.step = ""
	w.mu.Unlock()
	w.setStatus("config")
	w.openMenu(ctx)
}

func (w *moduTUIConfigWizard) cancel(status string) {
	w.mu.Lock()
	w.active = false
	w.step = ""
	w.mu.Unlock()
	w.setStatus(status)
	w.post("Config closed.")
}

func (w *moduTUIConfigWizard) menuText() string {
	return `Config

Choose an action:
  1. Setup with provider or add model manually
  2. Show config status

Type 1-2, or q to close.`
}

func (w *moduTUIConfigWizard) providerChoiceText() string {
	w.mu.Lock()
	providers := configWizardSetupProviders(w.providers)
	w.mu.Unlock()
	var b strings.Builder
	b.WriteString("Config: provider\n\nChoose a provider:\n")
	for i, provider := range providers {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, configWizardProviderLabel(provider.Name))
	}
	fmt.Fprintf(&b, "  %d. Custom OpenAI-Compatible\n\nType a number, back, or q.", len(providers)+1)
	return b.String()
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

func configWizardIsBack(value string) bool {
	value = strings.TrimSpace(value)
	return value == "" || strings.EqualFold(value, "back")
}

func (w *moduTUIConfigWizard) activeChoiceText(models []ConfigModelEntry) string {
	var b strings.Builder
	b.WriteString("Config: active model\n\nChoose a model:\n")
	for i, model := range models {
		marker := " "
		if model.Active {
			marker = "*"
		}
		fmt.Fprintf(&b, "  %d. %s%s  %s/%s\n", i+1, marker, configWizardModelTarget(model), model.Provider, model.Model)
	}
	b.WriteString("\nType a number, back, or q.")
	return b.String()
}

func configWizardModelTarget(model ConfigModelEntry) string {
	if strings.TrimSpace(model.Name) != "" {
		return strings.TrimSpace(model.Name)
	}
	return strings.TrimSpace(model.Provider) + "/" + strings.TrimSpace(model.Model)
}

func (w *moduTUIConfigWizard) postResult(title, out string, err error) {
	text := strings.TrimSpace(out)
	if err != nil {
		if text != "" {
			text += "\n"
		}
		text += "error: " + err.Error()
	}
	if text == "" {
		text = "completed"
	}
	w.post(title + "\n\n" + text)
}

func (w *moduTUIConfigWizard) requestChoice(ctx context.Context, title string, body string, options []modutui.HumanPromptOption) string {
	if w == nil || w.send == nil {
		return ""
	}
	ch := make(chan string, 1)
	w.send(modutui.RequestHumanPromptMsg{
		Request: modutui.HumanPromptRequest{
			ID:           "config",
			Title:        title,
			Body:         body,
			Options:      options,
			DefaultIndex: -1,
		},
		Respond: ch,
	})
	select {
	case value := <-ch:
		return strings.TrimSpace(value)
	case <-ctx.Done():
		w.send(modutui.CancelHumanPromptMsg{ID: "config"})
		return ""
	}
}

func (w *moduTUIConfigWizard) requestText(ctx context.Context, req modutui.HumanTextRequest) string {
	if w == nil || w.send == nil {
		return ""
	}
	ch := make(chan string, 1)
	w.send(modutui.RequestHumanTextMsg{
		Request: req,
		Respond: ch,
	})
	select {
	case value := <-ch:
		return value
	case <-ctx.Done():
		w.send(modutui.CancelHumanTextMsg{ID: req.ID})
		return ""
	}
}

func (w *moduTUIConfigWizard) post(text string) {
	if w == nil || w.send == nil {
		return
	}
	w.send(modutui.AppendMessageMsg{Message: modutui.Message{
		Role:         modutui.RoleAssistant,
		Text:         text,
		Preformatted: true,
	}})
}

func (w *moduTUIConfigWizard) setStatus(status string) {
	if w == nil || w.send == nil {
		return
	}
	w.send(modutui.SetStatusMsg{Status: status, TransientFor: moduTUITerminalStatusTTL})
}
