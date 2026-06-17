package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
)

type configInputField struct {
	key         string
	label       string
	placeholder string
	required    bool
	secret      bool
}

type configMenuChoice struct {
	Key         string
	Label       string
	Description string
}

var configMenuChoices = []configMenuChoice{
	{Key: "use", Label: "Active Model", Description: "choose default model"},
	{Key: "provider", Label: "Provider", Description: "set model source and API key"},
}

var configAddFields = []configInputField{
	{key: "name", label: "Name", placeholder: "local-qwen", required: true},
	{key: "provider", label: "Provider", placeholder: "lmstudio", required: true},
	{key: "model", label: "Model", placeholder: "qwen/qwen3.6-35b-a3b", required: true},
	{key: "baseUrl", label: "Base URL", placeholder: "http://127.0.0.1:1234/v1", required: true},
	{key: "apiKey", label: "API key", placeholder: "blank to skip", secret: true},
	{key: "description", label: "Description", placeholder: "local coding model"},
}

var configProviderFields = []configInputField{
	{key: "provider", label: "ProviderName", placeholder: "openai", required: true},
	{key: "apiKey", label: "API Key", placeholder: "blank to keep existing", secret: true},
	{key: "baseUrl", label: "BaseURL (optional)", placeholder: "https://api.openai.com/v1"},
}

const configNewProviderChoice = "__new_provider__"

func (b *bubbleTUI) runConfigCommand(args string) tea.Cmd {
	switch strings.TrimSpace(args) {
	case "":
		b.openConfigMenu()
		return nil
	case "add":
		b.openConfigAdd()
		return nil
	case "provider", "source":
		return b.openConfigProviderMenu()
	case "use":
		return b.openConfigSelect("use")
	case "remove", "rm":
		return b.openConfigSelect("remove")
	default:
		b.openConfigMenu()
		b.model.setTransientStatus("use /config to open settings")
		return nil
	}
}

func (b *bubbleTUI) openConfigMenu() {
	b.configMenuChoices = append([]configMenuChoice(nil), configMenuChoices...)
	b.configMenuIdx = 0
	b.slashMatches = nil
	b.slashIndex = 0
	b.model.state = uiStateConfigMenu
	b.model.statusMsg = "config"
}

func (b *bubbleTUI) openConfigAdd() {
	if b.commandHooks.ConfigAdd == nil {
		b.model.setTransientStatus("config add is not available")
		return
	}
	b.configAction = "add"
	b.configFields = append([]configInputField(nil), configAddFields...)
	b.configFieldIdx = 0
	b.configInput = ConfigModelInput{}
	b.draft = ""
	b.cursor = 0
	b.slashMatches = nil
	b.slashIndex = 0
	b.prefillConfigInputDraft()
	b.model.state = uiStateConfigInput
	b.model.statusMsg = "config add"
}

func (b *bubbleTUI) openConfigProvider() {
	if b.commandHooks.ConfigSetProvider == nil {
		b.model.setTransientStatus("config provider is not available")
		return
	}
	b.configAction = "provider"
	b.configFields = append([]configInputField(nil), configProviderFields...)
	b.configFieldIdx = 0
	if b.configProviderInput.Type == "" {
		b.configProviderInput.Type = "openai-compatible"
	}
	b.draft = ""
	b.cursor = 0
	b.slashMatches = nil
	b.slashIndex = 0
	b.prefillConfigInputDraft()
	b.model.state = uiStateConfigInput
	b.model.statusMsg = "config provider"
}

func (b *bubbleTUI) updateConfigMenuKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "esc":
		b.closeConfigMenu("config closed")
	case "up":
		b.moveConfigMenu(-1)
	case "down":
		b.moveConfigMenu(1)
	case "home":
		b.configMenuIdx = 0
	case "end":
		b.configMenuIdx = max(0, len(b.configMenuChoices)-1)
	case "enter", "ctrl+j":
		return b, b.confirmConfigMenu()
	default:
		runes := []rune(msg.Text)
		if len(runes) == 1 {
			switch runes[0] {
			case '\r', '\n', 'y', 'Y', 'l':
				return b, b.confirmConfigMenu()
			case 'j':
				b.moveConfigMenu(1)
				return b, nil
			case 'k':
				b.moveConfigMenu(-1)
				return b, nil
			case 'q', 'Q':
				b.closeConfigMenu("config closed")
				return b, nil
			case '1', '2', '3', '4', '5':
				idx := int(runes[0] - '1')
				if idx >= 0 && idx < len(b.configMenuChoices) {
					b.configMenuIdx = idx
					return b, b.confirmConfigMenu()
				}
			}
		}
	}
	return b, nil
}

func (b *bubbleTUI) moveConfigMenu(delta int) {
	if len(b.configMenuChoices) == 0 {
		return
	}
	b.configMenuIdx = (b.configMenuIdx + delta + len(b.configMenuChoices)) % len(b.configMenuChoices)
}

func (b *bubbleTUI) confirmConfigMenu() tea.Cmd {
	if len(b.configMenuChoices) == 0 || b.configMenuIdx >= len(b.configMenuChoices) {
		b.closeConfigMenu("config closed")
		return nil
	}
	choice := b.configMenuChoices[b.configMenuIdx]
	b.closeConfigMenu("")
	switch choice.Key {
	case "provider":
		return b.openConfigProviderMenu()
	case "use":
		return b.openConfigSelect("use")
	default:
		return nil
	}
}

func (b *bubbleTUI) openConfigProviderFor(name string) {
	b.configProviderInput = ConfigProviderInput{Type: "openai-compatible"}
	for _, provider := range b.configProviderAll {
		if provider.Name == name {
			b.configProviderInput = ConfigProviderInput{
				Provider:  provider.Name,
				Type:      provider.Type,
				BaseURL:   provider.BaseURL,
				APIKeyEnv: provider.APIKeyEnv,
			}
			break
		}
	}
	b.openConfigProvider()
}

func (b *bubbleTUI) closeConfigMenu(status string) {
	b.configMenuChoices = nil
	b.configMenuIdx = 0
	b.model.state = uiStateInput
	b.model.statusMsg = status
}

func (b *bubbleTUI) openConfigSelect(action string) tea.Cmd {
	if b.commandHooks.ConfigModels == nil {
		b.model.setTransientStatus("config model list is not available")
		return nil
	}
	return func() tea.Msg {
		models, err := b.commandHooks.ConfigModels()
		if err != nil {
			return bubbleConfigDoneMsg{err: err}
		}
		return bubbleConfigModelsMsg{action: action, models: models}
	}
}

func (b *bubbleTUI) openConfigProviderMenu() tea.Cmd {
	if b.commandHooks.ConfigProviders == nil {
		b.configProviderAll = nil
		b.configProviderChoices = nil
		return b.openConfigProviderSelect()
	}
	return func() tea.Msg {
		providers, err := b.commandHooks.ConfigProviders()
		if err != nil {
			return bubbleConfigDoneMsg{err: err}
		}
		return bubbleConfigProvidersMsg{providers: providers}
	}
}

type bubbleConfigProvidersMsg struct {
	providers []ConfigProviderEntry
}

func (b *bubbleTUI) handleConfigProviders(msg bubbleConfigProvidersMsg) {
	b.configProviderAll = append([]ConfigProviderEntry(nil), msg.providers...)
	b.openConfigProviderSelect()
}

func (b *bubbleTUI) openConfigProviderSelect() tea.Cmd {
	b.configAction = "provider-select"
	b.configProviderAll = append([]ConfigProviderEntry{{
		Name:    configNewProviderChoice,
		Type:    "openai-compatible",
		BaseURL: "custom OpenAI-compatible source",
	}}, b.configProviderAll...)
	b.configSearch = ""
	b.configSelectIdx = 0
	b.configSelectScroll = 0
	b.filterConfigChoices()
	b.model.state = uiStateConfigSelect
	b.model.statusMsg = "config provider"
	return nil
}

type bubbleConfigModelsMsg struct {
	action string
	models []ConfigModelEntry
}

func (b *bubbleTUI) handleConfigModels(msg bubbleConfigModelsMsg) {
	if len(msg.models) == 0 {
		b.model.setTransientStatus("no configured models")
		return
	}
	b.configAction = msg.action
	b.configAllChoices = append([]ConfigModelEntry(nil), msg.models...)
	b.configSearch = ""
	b.configSelectIdx = 0
	for i, model := range b.configAllChoices {
		if model.Active {
			b.configSelectIdx = i
			break
		}
	}
	b.filterConfigChoices()
	b.adjustConfigSelectScroll()
	b.model.state = uiStateConfigSelect
	b.model.statusMsg = "config " + msg.action
}

func (b *bubbleTUI) updateConfigInputKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c", "esc":
		b.closeConfigInput("back to config")
	case "backspace", "ctrl+h":
		b.backspaceDraft()
	case "delete":
		b.deleteDraft()
	case "left":
		if b.cursor > 0 {
			b.cursor--
		}
	case "right":
		if b.cursor < len([]rune(b.draft)) {
			b.cursor++
		}
	case "home":
		b.cursor = 0
	case "end":
		b.cursor = len([]rune(b.draft))
	case "enter", "ctrl+j":
		return b.advanceConfigInput()
	default:
		for _, r := range msg.Text {
			if r >= 0x20 {
				b.insertRune(r)
			}
		}
	}
	return nil
}

func (b *bubbleTUI) prefillConfigInputDraft() {
	if b.configFieldIdx < 0 || b.configFieldIdx >= len(b.configFields) {
		return
	}
	field := b.configFields[b.configFieldIdx]
	value := ""
	if b.configAction == "provider" {
		switch field.key {
		case "provider":
			value = b.configProviderInput.Provider
		case "type":
			value = b.configProviderInput.Type
		case "baseUrl":
			value = b.configProviderInput.BaseURL
		case "apiKeyEnv":
			value = b.configProviderInput.APIKeyEnv
		}
	}
	b.draft = value
	b.cursor = len([]rune(value))
}

func (b *bubbleTUI) advanceConfigInput() tea.Cmd {
	if b.configFieldIdx < 0 || b.configFieldIdx >= len(b.configFields) {
		return nil
	}
	field := b.configFields[b.configFieldIdx]
	value := strings.TrimSpace(b.draft)
	if field.required && value == "" {
		b.model.setTransientStatus(strings.ToLower(field.label) + " is required")
		return nil
	}
	b.assignConfigField(field.key, value)
	b.configFieldIdx++
	b.draft = ""
	b.cursor = 0
	if b.configFieldIdx < len(b.configFields) {
		b.prefillConfigInputDraft()
		return nil
	}
	input := b.configInput
	if b.configAction == "provider" {
		providerInput := b.configProviderInput
		b.closeConfigInput("saving provider: " + providerInput.Provider)
		return func() tea.Msg {
			out, err := b.commandHooks.ConfigSetProvider(providerInput)
			return bubbleConfigDoneMsg{out: out, err: err}
		}
	}
	b.closeConfigInput("adding model: " + input.Name)
	return func() tea.Msg {
		out, err := b.commandHooks.ConfigAdd(input)
		return bubbleConfigDoneMsg{out: out, err: err}
	}
}

func (b *bubbleTUI) assignConfigField(key, value string) {
	switch key {
	case "name":
		b.configInput.Name = value
	case "provider":
		b.configInput.Provider = value
	case "model":
		b.configInput.Model = value
	case "baseUrl":
		b.configInput.BaseURL = value
	case "apiKey":
		b.configInput.APIKey = value
	case "description":
		b.configInput.Description = value
	case "type":
		if value == "" {
			value = "openai-compatible"
		}
		b.configProviderInput.Type = value
	case "apiKeyEnv":
		b.configProviderInput.APIKeyEnv = value
	}
	if b.configAction == "provider" {
		switch key {
		case "provider":
			b.configProviderInput.Provider = value
		case "baseUrl":
			b.configProviderInput.BaseURL = value
		case "apiKey":
			b.configProviderInput.APIKey = value
		}
	}
}

func (b *bubbleTUI) closeConfigInput(status string) {
	b.configAction = ""
	b.configFields = nil
	b.configFieldIdx = 0
	b.configInput = ConfigModelInput{}
	b.configProviderInput = ConfigProviderInput{}
	b.draft = ""
	b.cursor = 0
	b.openConfigMenu()
	if status != "" {
		b.model.statusMsg = status
	}
}

func (b *bubbleTUI) updateConfigSelectKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "esc":
		b.closeConfigSelect("back to config")
	case "up":
		b.moveConfigSelect(-1)
	case "down":
		b.moveConfigSelect(1)
	case "home":
		b.jumpConfigSelect(0)
	case "end":
		b.jumpConfigSelect(b.configSelectLen() - 1)
	case "pgup":
		b.jumpConfigSelect(b.configSelectIdx - modelSelectVisibleRows)
	case "pgdown":
		b.jumpConfigSelect(b.configSelectIdx + modelSelectVisibleRows)
	case "backspace", "ctrl+h":
		b.backspaceConfigSearch()
	case "enter", "ctrl+j":
		return b, b.confirmConfigSelect()
	default:
		runes := []rune(msg.Text)
		if len(runes) == 0 {
			return b, nil
		}
		if len(runes) == 1 {
			switch runes[0] {
			case '\r', '\n', 'y', 'Y', 'l':
				return b, b.confirmConfigSelect()
			case 'j':
				b.moveConfigSelect(1)
				return b, nil
			case 'k':
				b.moveConfigSelect(-1)
				return b, nil
			case 'q', 'Q':
				b.closeConfigSelect("back to config")
				return b, nil
			case '1', '2', '3', '4', '5', '6', '7', '8', '9':
				idx := b.configSelectScroll + int(runes[0]-'1')
				if idx >= 0 && idx < b.configSelectLen() {
					b.configSelectIdx = idx
					return b, b.confirmConfigSelect()
				}
				return b, nil
			}
		}
		for _, r := range runes {
			if r >= 0x20 {
				b.configSearch += string(r)
			}
		}
		b.configSelectIdx = 0
		b.filterConfigChoices()
	}
	return b, nil
}

func (b *bubbleTUI) filterConfigChoices() {
	if b.configAction == "provider-select" {
		b.filterConfigProviderChoices()
		return
	}
	query := strings.ToLower(strings.TrimSpace(b.configSearch))
	choices := b.configAllChoices
	if query != "" {
		filtered := make([]ConfigModelEntry, 0, len(choices))
		for _, model := range choices {
			haystack := strings.ToLower(model.Name + " " + model.Description + " " + model.Provider + " " + model.Model + " " + model.Provider + "/" + model.Model)
			if strings.Contains(haystack, query) {
				filtered = append(filtered, model)
			}
		}
		choices = filtered
	}
	b.configChoices = choices
	if b.configSelectIdx >= len(b.configChoices) {
		b.configSelectIdx = max(0, len(b.configChoices)-1)
	}
	b.adjustConfigSelectScroll()
}

func (b *bubbleTUI) filterConfigProviderChoices() {
	query := strings.ToLower(strings.TrimSpace(b.configSearch))
	choices := b.configProviderAll
	if query != "" {
		filtered := make([]ConfigProviderEntry, 0, len(choices))
		for _, provider := range choices {
			haystack := strings.ToLower(provider.Name + " " + provider.Type + " " + provider.BaseURL + " " + provider.APIKeyEnv)
			if provider.Name == configNewProviderChoice || strings.Contains(haystack, query) {
				filtered = append(filtered, provider)
			}
		}
		choices = filtered
	}
	b.configProviderChoices = choices
	if b.configSelectIdx >= len(b.configProviderChoices) {
		b.configSelectIdx = max(0, len(b.configProviderChoices)-1)
	}
	b.adjustConfigSelectScroll()
}

func (b *bubbleTUI) backspaceConfigSearch() {
	rs := []rune(b.configSearch)
	if len(rs) == 0 {
		return
	}
	b.configSearch = string(rs[:len(rs)-1])
	b.configSelectIdx = 0
	b.filterConfigChoices()
}

func (b *bubbleTUI) moveConfigSelect(delta int) {
	if b.configSelectLen() == 0 {
		return
	}
	n := b.configSelectLen()
	b.configSelectIdx = (b.configSelectIdx + delta + n) % n
	b.adjustConfigSelectScroll()
}

func (b *bubbleTUI) jumpConfigSelect(idx int) {
	if b.configSelectLen() == 0 {
		return
	}
	b.configSelectIdx = clampInt(idx, 0, b.configSelectLen()-1)
	b.adjustConfigSelectScroll()
}

func (b *bubbleTUI) adjustConfigSelectScroll() {
	choiceCount := b.configSelectLen()
	if choiceCount <= modelSelectVisibleRows {
		b.configSelectScroll = 0
		return
	}
	if b.configSelectIdx < b.configSelectScroll {
		b.configSelectScroll = b.configSelectIdx
	} else if b.configSelectIdx >= b.configSelectScroll+modelSelectVisibleRows {
		b.configSelectScroll = b.configSelectIdx - modelSelectVisibleRows + 1
	}
	if maxOffset := choiceCount - modelSelectVisibleRows; b.configSelectScroll > maxOffset {
		b.configSelectScroll = maxOffset
	}
	if b.configSelectScroll < 0 {
		b.configSelectScroll = 0
	}
}

func (b *bubbleTUI) configSelectLen() int {
	if b.configAction == "provider-select" {
		return len(b.configProviderChoices)
	}
	return len(b.configChoices)
}

func (b *bubbleTUI) confirmConfigSelect() tea.Cmd {
	if b.configAction == "provider-select" {
		return b.confirmConfigProviderSelect()
	}
	if len(b.configChoices) == 0 || b.configSelectIdx >= len(b.configChoices) {
		b.closeConfigSelect("config " + b.configAction + " unchanged")
		return nil
	}
	choice := b.configChoices[b.configSelectIdx]
	target := configChoiceTarget(choice)
	action := b.configAction
	b.closeConfigSelect("config " + action + ": " + target)
	return func() tea.Msg {
		var out string
		var err error
		switch action {
		case "use":
			out, err = b.commandHooks.ConfigUse(target)
		case "remove":
			out, err = b.commandHooks.ConfigRemove(target)
		default:
			err = fmt.Errorf("unknown config action: %s", action)
		}
		return bubbleConfigDoneMsg{out: out, err: err}
	}
}

func (b *bubbleTUI) confirmConfigProviderSelect() tea.Cmd {
	if len(b.configProviderChoices) == 0 || b.configSelectIdx >= len(b.configProviderChoices) {
		b.closeConfigSelect("back to config")
		return nil
	}
	choice := b.configProviderChoices[b.configSelectIdx]
	b.resetConfigSelect()
	if choice.Name == configNewProviderChoice {
		b.configProviderInput = ConfigProviderInput{Type: "openai-compatible"}
		b.openConfigProvider()
		return nil
	}
	b.openConfigProviderFor(choice.Name)
	return nil
}

func (b *bubbleTUI) closeConfigSelect(status string) {
	b.resetConfigSelect()
	b.openConfigMenu()
	if status != "" {
		b.model.statusMsg = status
	}
}

func (b *bubbleTUI) resetConfigSelect() {
	b.configAction = ""
	b.configChoices = nil
	b.configAllChoices = nil
	b.configProviderChoices = nil
	b.configSelectIdx = 0
	b.configSelectScroll = 0
	b.configSearch = ""
}

func configChoiceTarget(choice ConfigModelEntry) string {
	if strings.TrimSpace(choice.Name) != "" {
		return strings.TrimSpace(choice.Name)
	}
	return strings.TrimSpace(choice.Provider) + "/" + strings.TrimSpace(choice.Model)
}

func (b *bubbleTUI) renderConfigInput() string {
	if b.configFieldIdx < 0 || b.configFieldIdx >= len(b.configFields) {
		return ""
	}
	if b.configAction == "provider" {
		return b.renderConfigProviderInput()
	}
	field := b.configFields[b.configFieldIdx]
	value := b.draft
	if field.secret && value != "" {
		value = strings.Repeat("*", len([]rune(value)))
	}
	if value == "" {
		value = uiDimText.Render(field.placeholder)
	}
	required := ""
	if field.required {
		required = " required"
	}
	title := "Config add"
	if b.configAction == "provider" {
		title = "Config provider"
	}
	lines := []string{
		uiWhiteText.Render(selectorHeaderLine(selectorHeaderOptions{
			Title:    title,
			Selected: b.configFieldIdx,
			Visible:  len(b.configFields),
			Total:    len(b.configFields),
			Mode:     "field=" + field.key + required,
		})),
		uiDimText.Render("  " + field.label),
		"  " + value,
		uiDimText.Render("  enter next  esc cancel"),
	}
	return strings.Join(lines, "\n")
}

func (b *bubbleTUI) renderConfigProviderInput() string {
	field := b.configFields[b.configFieldIdx]
	required := ""
	if field.required {
		required = " required"
	}
	lines := []string{
		uiWhiteText.Render(selectorHeaderLine(selectorHeaderOptions{
			Title:    "Config provider",
			Selected: b.configFieldIdx,
			Visible:  len(b.configFields),
			Total:    len(b.configFields),
			Mode:     "field=" + field.key + required,
		})),
	}
	for i, f := range b.configFields {
		line := "  " + f.label + "  " + b.configProviderFieldDisplayValue(i, f)
		if i == b.configFieldIdx {
			line = uiPrimaryText.Render(line)
		} else {
			line = uiDimText.Render(line)
		}
		lines = append(lines, line)
	}
	lines = append(lines, uiDimText.Render("  enter next  esc cancel"))
	return strings.Join(lines, "\n")
}

func (b *bubbleTUI) configProviderFieldDisplayValue(idx int, field configInputField) string {
	value := ""
	if idx == b.configFieldIdx {
		value = b.draft
	} else {
		switch field.key {
		case "provider":
			value = b.configProviderInput.Provider
		case "apiKey":
			value = b.configProviderInput.APIKey
		case "baseUrl":
			value = b.configProviderInput.BaseURL
		}
	}
	if field.secret && value != "" {
		value = strings.Repeat("*", len([]rune(value)))
	}
	if value == "" {
		return uiDimText.Render(field.placeholder)
	}
	return value
}

func (b *bubbleTUI) renderConfigMenu() string {
	lines := []string{
		uiWhiteText.Render(selectorHeaderLine(selectorHeaderOptions{
			Title:    "Config",
			Selected: b.configMenuIdx,
			Visible:  len(b.configMenuChoices),
			Total:    len(b.configMenuChoices),
		})),
	}
	for i, choice := range b.configMenuChoices {
		line := fmt.Sprintf("%d   %s", i+1, choice.Label)
		if choice.Description != "" {
			line += "  " + choice.Description
		}
		if i == b.configMenuIdx {
			line = uiPrimaryText.Render(line)
		} else {
			line = uiDimText.Render(line)
		}
		lines = append(lines, line)
	}
	lines = append(lines, uiDimText.Render(b.configHint()))
	return strings.Join(lines, "\n")
}

func (b *bubbleTUI) renderConfigSelect() string {
	action := b.configAction
	title := "Config " + action
	if action == "provider-select" {
		title = "Config Provider"
	}
	query := b.configSearch
	if query == "" {
		query = "type to search"
	}
	visible := len(b.configChoices)
	total := len(b.configAllChoices)
	if action == "provider-select" {
		visible = len(b.configProviderChoices)
		total = len(b.configProviderAll)
	}
	lines := []string{
		uiWhiteText.Render(selectorHeaderLine(selectorHeaderOptions{
			Title:    title,
			Selected: b.configSelectIdx,
			Visible:  visible,
			Total:    total,
			Query:    b.configSearch,
		})),
		uiDimText.Render("  search: " + query),
	}
	end := b.configSelectScroll + modelSelectVisibleRows
	if end > visible {
		end = visible
	}
	if visible == 0 {
		noMatch := "  no matching models"
		if action == "provider-select" {
			noMatch = "  no matching providers"
		}
		lines = append(lines, uiDimText.Render(noMatch))
	}
	for i := b.configSelectScroll; i < end; i++ {
		line := ""
		if action == "provider-select" {
			line = configProviderChoiceLine(b.configProviderChoices[i], i == b.configSelectIdx)
		} else {
			line = configChoiceLine(b.configChoices[i], i == b.configSelectIdx)
		}
		if n := i - b.configSelectScroll + 1; n >= 1 && n <= 9 {
			line = fmt.Sprintf("%d %s", n, line)
		} else {
			line = "  " + line
		}
		if i == b.configSelectScroll && b.configSelectScroll > 0 {
			line += "  ^"
		} else if i == end-1 && end < visible {
			line += "  v"
		}
		if i == b.configSelectIdx {
			line = uiPrimaryText.Render(line)
		} else {
			line = uiDimText.Render(line)
		}
		lines = append(lines, line)
	}
	lines = append(lines, uiDimText.Render(b.configHint()))
	return strings.Join(lines, "\n")
}

func configProviderChoiceLine(choice ConfigProviderEntry, selected bool) string {
	name := choice.Name
	if name == configNewProviderChoice {
		name = "Custom OpenAI-compatible"
	}
	line := name
	if choice.Type != "" {
		line += "  " + choice.Type
	}
	if choice.BaseURL != "" {
		line += "  " + choice.BaseURL
	}
	if choice.APIKeyEnv != "" {
		line += "  key env: " + choice.APIKeyEnv
	} else if choice.APIKeySet {
		line += "  key: set"
	}
	if selected {
		return lipgloss.NewStyle().Bold(true).Render(line)
	}
	return line
}

func configChoiceLine(choice ConfigModelEntry, selected bool) string {
	name := choice.Name
	if name == "" {
		name = choice.Provider + "/" + choice.Model
	}
	line := name + "  " + choice.Provider + "/" + choice.Model
	if choice.Active {
		line += "  [active]"
	}
	if choice.Description != "" {
		line += "  " + choice.Description
	}
	if selected {
		return lipgloss.NewStyle().Bold(true).Render(line)
	}
	return line
}

func (b *bubbleTUI) configHint() string {
	switch b.model.state {
	case uiStateConfigMenu:
		if b.configAction == "provider-select" {
			return "up/down or j/k select  enter/y confirm  esc/q back"
		}
		return "up/down or j/k select  enter/y confirm  esc/q close"
	case uiStateConfigInput:
		return "enter next  esc back"
	case uiStateConfigSelect:
		return "up/down or j/k select  enter/y confirm  esc/q back"
	default:
		return ""
	}
}

func (b *bubbleTUI) appendConfigDoneBlock(out string, err error) tea.Cmd {
	content := strings.TrimSpace(out)
	if err != nil {
		if content != "" {
			content += "\n"
		}
		content += "error: " + err.Error()
	}
	if content == "" {
		content = "config command completed"
	}
	block := uiBlock{Kind: "section", Title: "Config", Content: content, Timestamp: time.Now()}
	b.appendBlock(block)
	return b.printBlockCmd(block)
}
